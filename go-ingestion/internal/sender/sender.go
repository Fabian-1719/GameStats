// Package sender implementa el envío de lotes de partidas a Scala
// mediante un worker pool concurrente con reintentos y persistencia de fallos.
package sender

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gamestats/go-ingestion/internal/model"
)

// Config define las opciones de configuración para el sender.
type Config struct {
	ScalaURL       string        // URL base de Scala (ej: "http://localhost:8081")
	Workers        int           // Número de goroutines de envío (por defecto 4)
	Timeout        time.Duration // Timeout por petición HTTP (por defecto 5s)
	MaxRetries     int           // Reintentos antes de descartar a disco (por defecto 3)
	InitialBackoff time.Duration // Tiempo de espera inicial para reintentos (por defecto 200ms)
	FailedBatchDir string        // Directorio donde guardar lotes fallidos
}

// Stats contiene métricas acumuladas del worker pool.
type Stats struct {
	Sent    int64
	Retries int64
	Failed  int64
}

// Client define la interfaz para realizar las peticiones HTTP (facilita testing).
type HTTPPoster interface {
	Do(req *http.Request) (*http.Response, error)
}

// Pool administra las goroutines que consumen lotes y los envían a Scala.
type Pool struct {
	cfg        Config
	httpClient HTTPPoster
	failedDir  string

	mu    sync.Mutex
	stats Stats
}

// New crea un nuevo Pool de envío.
func New(cfg Config) *Pool {
	if cfg.Workers <= 0 {
		cfg.Workers = 4
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 5 * time.Second
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 3
	}
	if cfg.InitialBackoff <= 0 {
		cfg.InitialBackoff = 200 * time.Millisecond
	}
	if cfg.FailedBatchDir == "" {
		cfg.FailedBatchDir = "failed-batches"
	}

	return &Pool{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		failedDir: cfg.FailedBatchDir,
	}
}

// Stats devuelve una copia de las estadísticas actuales.
func (p *Pool) Stats() Stats {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stats
}

func (p *Pool) incSent() {
	p.mu.Lock()
	p.stats.Sent++
	p.mu.Unlock()
}

func (p *Pool) incRetries() {
	p.mu.Lock()
	p.stats.Retries++
	p.mu.Unlock()
}

func (p *Pool) incFailed() {
	p.mu.Lock()
	p.stats.Failed++
	p.mu.Unlock()
}

// Run inicia las goroutines consumidoras que leen de batches hasta que se cierre.
// Bloquea hasta que todos los trabajadores hayan procesado los lotes pendientes.
func (p *Pool) Run(ctx context.Context, batches <-chan model.Batch) {
	var wg sync.WaitGroup

	for i := 0; i < p.cfg.Workers; i++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for b := range batches {
				p.processBatch(ctx, b)
			}
		}(i + 1)
	}

	wg.Wait()
}

// processBatch intenta enviar un lote. Si falla tras MaxRetries o si ScalaURL no está
// configurada, lo almacena en failedDir.
func (p *Pool) processBatch(ctx context.Context, b model.Batch) {
	if p.cfg.ScalaURL == "" {
		// Modo sin destino configurado: se registra como fallido/omitido a disco
		log.Printf("sender: SCALA_URL no configurada; guardando lote %s en disco", b.MatchID)
		if err := p.saveFailedBatch(b, "SCALA_URL no configurada"); err != nil {
			log.Printf("sender: error guardando lote en disco: %v", err)
		}
		p.incFailed()
		return
	}

	endpoint := fmt.Sprintf("%s/v1/analyze", p.cfg.ScalaURL)
	payload, err := json.Marshal(b)
	if err != nil {
		log.Printf("sender: error serializando lote %s: %v", b.MatchID, err)
		_ = p.saveFailedBatch(b, fmt.Sprintf("json.Marshal error: %v", err))
		p.incFailed()
		return
	}

	backoff := p.cfg.InitialBackoff
	var lastErr error

	for attempt := 0; attempt <= p.cfg.MaxRetries; attempt++ {
		if attempt > 0 {
			p.incRetries()
			select {
			case <-ctx.Done():
				lastErr = ctx.Err()
				break
			case <-time.After(backoff):
				backoff *= 2 // Exponential backoff
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
		if err != nil {
			lastErr = err
			break
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := p.httpClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}

		resp.Body.Close()

		// Consideramos exitoso cualquier 2xx
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			p.incSent()
			log.Printf("sender: lote enviado con éxito: partida=%s juego=%s estado=%s eventos=%d",
				b.MatchID, b.GameID, b.Status, len(b.Events))
			return
		}

		// Si el error es 4xx que no sea 429, normalmente es error del cliente, no reintentamos
		if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != http.StatusTooManyRequests {
			lastErr = fmt.Errorf("status HTTP no reintentable: %d", resp.StatusCode)
			break
		}

		lastErr = fmt.Errorf("status HTTP temporal: %d", resp.StatusCode)
	}

	p.incFailed()
	reason := "desconocida"
	if lastErr != nil {
		reason = lastErr.Error()
	}
	log.Printf("sender: fallo definitivo enviando lote %s tras %d intentos: %s",
		b.MatchID, p.cfg.MaxRetries+1, reason)

	if err := p.saveFailedBatch(b, reason); err != nil {
		log.Printf("sender: error guardando lote fallido %s en disco: %v", b.MatchID, err)
	}
}

// saveFailedBatch escribe el lote con el motivo del fallo en el directorio de fallidos.
func (p *Pool) saveFailedBatch(b model.Batch, reason string) error {
	if err := os.MkdirAll(p.failedDir, 0o755); err != nil {
		return err
	}

	type failedWrapper struct {
		Reason    string      `json:"reason"`
		Timestamp string      `json:"timestamp"`
		Batch     model.Batch `json:"batch"`
	}

	wrapped := failedWrapper{
		Reason:    reason,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Batch:     b,
	}

	data, err := json.MarshalIndent(wrapped, "", "  ")
	if err != nil {
		return err
	}

	filename := fmt.Sprintf("%s_%s_%d.json", b.GameID, b.MatchID, time.Now().UnixNano())
	path := filepath.Join(p.failedDir, filename)
	return os.WriteFile(path, data, 0o644)
}
