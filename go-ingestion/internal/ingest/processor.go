// Package ingest recibe eventos (HTTP o archivo NDJSON), los valida y los
// entrega al siguiente paso del flujo. No calcula estadísticas: eso es de Scala.
package ingest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"sync/atomic"

	"gamestats/go-ingestion/internal/model"
	"gamestats/go-ingestion/internal/validator"
)

// Sink recibe cada evento válido. En el Día 1 es un stub; en el Día 2 será el
// envío al router (channel) que reparte por partida.
type Sink func(model.Event)

// Rejecter guarda un evento rechazado con su motivo (lo implementa dlq.Writer).
type Rejecter interface {
	Reject(raw []byte, reason, source string) error
}

// Stats resume cuántos eventos se procesaron.
type Stats struct {
	Total    int64 `json:"total"`
	Accepted int64 `json:"accepted"`
	Rejected int64 `json:"rejected"`
}

// Processor es el punto único por donde pasa todo evento, sin importar si
// llegó por HTTP o por archivo.
type Processor struct {
	validator *validator.Validator
	dlq       Rejecter
	sink      Sink

	accepted atomic.Int64
	rejected atomic.Int64
}

func NewProcessor(v *validator.Validator, d Rejecter, s Sink) *Processor {
	return &Processor{validator: v, dlq: d, sink: s}
}

// Stats devuelve los contadores acumulados desde que arrancó el proceso.
func (p *Processor) Stats() Stats {
	a, r := p.accepted.Load(), p.rejected.Load()
	return Stats{Total: a + r, Accepted: a, Rejected: r}
}

// Process decodifica y valida un evento crudo. Devuelve nil si fue aceptado
// (y entregado al sink) o un error con el motivo si fue rechazado (y enviado a
// la DLQ). source describe el origen: "http", "archivo:línea 7", etc.
func (p *Processor) Process(raw []byte, source string) error {
	var ev model.Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		return p.reject(raw, source, &validator.Error{
			Code:   "BAD_JSON",
			Reason: "JSON inválido o con tipos incorrectos: " + err.Error(),
		})
	}
	if err := p.validator.Validate(ev); err != nil {
		return p.reject(raw, source, err)
	}
	p.accepted.Add(1)
	p.sink(ev)
	return nil
}

func (p *Processor) reject(raw []byte, source string, err error) error {
	p.rejected.Add(1)
	if derr := p.dlq.Reject(raw, err.Error(), source); derr != nil {
		// No perdemos el rechazo en silencio: lo devolvemos junto al fallo de la DLQ.
		return fmt.Errorf("%w (además falló escribir en la DLQ: %v)", err, derr)
	}
	return err
}

// LoadNDJSON procesa un flujo NDJSON (un evento por línea) y devuelve el
// resumen de esa carga.
func (p *Processor) LoadNDJSON(r io.Reader, name string) (Stats, error) {
	var st Stats
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024) // líneas de hasta 4 MB
	line := 0
	for sc.Scan() {
		line++
		raw := sc.Bytes()
		if len(trimSpace(raw)) == 0 {
			continue
		}
		st.Total++
		// raw se reutiliza en la siguiente iteración: copiamos antes de procesar.
		cp := append([]byte(nil), raw...)
		if err := p.Process(cp, fmt.Sprintf("%s:línea %d", name, line)); err != nil {
			st.Rejected++
		} else {
			st.Accepted++
		}
	}
	return st, sc.Err()
}

func trimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\r' || b[start] == '\n') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\r' || b[end-1] == '\n') {
		end--
	}
	return b[start:end]
}
