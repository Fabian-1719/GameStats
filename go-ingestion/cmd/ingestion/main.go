// Comando ingestion: recibe eventos por HTTP o carga un archivo NDJSON.
//
// Configuración por variables de entorno:
//
//	PORT             puerto HTTP (por defecto 8080)
//	CATALOG_PATH     catálogo de eventos (por defecto config/catalog.json)
//	REJECTED_PATH    archivo de rechazados (por defecto rejected.ndjson)
//	IDLE_TIMEOUT     sin eventos durante este tiempo, la partida se envía (30s)
//	FINISH_GRACE     espera tras MATCH_FINISHED por eventos que llegan tarde (2s)
//	BATCH_DIR        si se define, guarda cada lote como <gameId>_<matchId>.json (diagnóstico)
//	SCALA_URL        URL base de Scala (ejemplo: http://localhost:8081)
//	SENDER_WORKERS   número de workers para enviar a Scala (por defecto 4)
//	SENDER_TIMEOUT   timeout por envío HTTP a Scala (por defecto 5s)
//	FAILED_BATCH_DIR directorio donde guardar lotes que fallen definitivamente (failed-batches)
//
// Uso:
//
//	ingestion                   # levanta el servidor HTTP
//	ingestion -file datos.ndjson   # carga un archivo y termina
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"gamestats/go-ingestion/internal/dlq"
	"gamestats/go-ingestion/internal/ingest"
	"gamestats/go-ingestion/internal/match"
	"gamestats/go-ingestion/internal/model"
	"gamestats/go-ingestion/internal/router"
	"gamestats/go-ingestion/internal/sender"
	"gamestats/go-ingestion/internal/validator"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		log.Fatalf("%s=%q no es un entero positivo válido", key, v)
	}
	return n
}

func envDuration(key string, fallback time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		log.Fatalf("%s=%q no es una duración válida (ejemplo: 30s)", key, v)
	}
	return d
}

func main() {
	file := flag.String("file", "", "archivo NDJSON a cargar (si se omite, levanta el servidor HTTP)")
	flag.Parse()

	catalog, err := validator.LoadCatalog(env("CATALOG_PATH", "config/catalog.json"))
	if err != nil {
		log.Fatal(err)
	}
	rejected, err := dlq.Open(env("REJECTED_PATH", "rejected.ndjson"))
	if err != nil {
		log.Fatalf("abriendo DLQ: %v", err)
	}
	defer rejected.Close()

	// Un evento tardío (su partida ya se envió) no se pierde en silencio: va a la DLQ.
	onLate := func(ev model.Event, reason string) {
		raw, _ := json.Marshal(ev)
		if err := rejected.Reject(raw, "LATE_EVENT: "+reason, "router"); err != nil {
			log.Printf("no se pudo registrar el evento tardío %s: %v", ev.EventID, err)
		}
	}

	rt := router.New(match.Config{
		IdleTimeout: envDuration("IDLE_TIMEOUT", 30*time.Second),
		FinishGrace: envDuration("FINISH_GRACE", 2*time.Second),
	}, onLate)

	batchDir := os.Getenv("BATCH_DIR")
	if batchDir != "" {
		if err := os.MkdirAll(batchDir, 0o755); err != nil {
			log.Fatal(err)
		}
	}

	routerCtx, stopRouter := context.WithCancel(context.Background())
	routerDone := make(chan struct{})
	go func() { rt.Run(routerCtx); close(routerDone) }()

	// Worker pool del sender hacia Scala (Día 3)
	senderPool := sender.New(sender.Config{
		ScalaURL:       env("SCALA_URL", ""),
		Workers:        envInt("SENDER_WORKERS", 4),
		Timeout:        envDuration("SENDER_TIMEOUT", 5*time.Second),
		FailedBatchDir: env("FAILED_BATCH_DIR", "failed-batches"),
	})

	// Canal puente: permite guardar copia opcional en BATCH_DIR y llevar contadores
	senderBatches := make(chan model.Batch, 16)
	byStatus := map[string]int{}
	bridgeDone := make(chan struct{})

	go func() {
		defer close(bridgeDone)
		defer close(senderBatches)
		for b := range rt.Batches() {
			byStatus[b.Status]++
			if batchDir != "" {
				data, err := json.MarshalIndent(b, "", "  ")
				if err == nil {
					_ = os.WriteFile(filepath.Join(batchDir, b.GameID+"_"+b.MatchID+".json"), data, 0o644)
				}
			}
			senderBatches <- b
		}
	}()

	senderDone := make(chan struct{})
	go func() {
		senderPool.Run(context.Background(), senderBatches)
		close(senderDone)
	}()

	// Cada evento válido pasa por el router, que lo reparte a la goroutine de su partida.
	sink := func(ev model.Event) {
		if !rt.Submit(ev) {
			onLate(ev, "servicio apagándose")
		}
	}

	// shutdown detiene el router (las partidas en curso envían lo que tienen) y
	// espera a que todos los lotes hayan sido despachados por el sender pool.
	shutdown := func() {
		stopRouter()
		<-routerDone
		<-bridgeDone
		<-senderDone
	}

	proc := ingest.NewProcessor(validator.New(catalog), rejected, sink)

	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		st, err := proc.LoadNDJSON(f, *file)
		if err != nil {
			log.Fatalf("leyendo archivo: %v", err)
		}
		shutdown()
		sStats := senderPool.Stats()
		fmt.Printf("total=%d aceptados=%d rechazados=%d | partidas=%d lotes=%v enviados=%d fallidos=%d tardíos=%d\n",
			st.Total, st.Accepted, st.Rejected, rt.Stats().Matches, byStatus, sStats.Sent, sStats.Failed, rt.Stats().Late)
		return
	}

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           ingest.NewMux(proc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("ingestión escuchando en %s (Scala URL: %q)", srv.Addr, env("SCALA_URL", "<disco local>"))
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("apagando...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("error al apagar servidor HTTP: %v", err)
	}
	shutdown() // el servidor HTTP ya no acepta eventos: ahora se procesa y envía lo que quedaba en curso
	st := proc.Stats()
	sStats := senderPool.Stats()
	log.Printf("final: total=%d aceptados=%d rechazados=%d partidas=%d lotes=%v enviados=%d fallidos=%d tardíos=%d",
		st.Total, st.Accepted, st.Rejected, rt.Stats().Matches, byStatus, sStats.Sent, sStats.Failed, rt.Stats().Late)
}
