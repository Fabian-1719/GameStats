package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const maxBodyBytes = 10 << 20 // 10 MB por petición

// RejectInfo describe un evento rechazado dentro de una petición.
type RejectInfo struct {
	Index   int    `json:"index"`
	EventID string `json:"eventId,omitempty"`
	Reason  string `json:"reason"`
}

// EventsResponse es la respuesta de POST /v1/events.
type EventsResponse struct {
	Received int          `json:"received"`
	Accepted int          `json:"accepted"`
	Rejected int          `json:"rejected"`
	Errors   []RejectInfo `json:"errors,omitempty"`
}

// NewMux registra las rutas del módulo.
//
//	POST /v1/events  -> uno o varios eventos (objeto JSON o arreglo)
//	GET  /health     -> estado del servicio y contadores
func NewMux(p *Processor) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/events", p.handleEvents)
	mux.HandleFunc("GET /health", p.handleHealth)
	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (p *Processor) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "stats": p.Stats()})
}

// handleEvents responde:
//
//	202 si todos los eventos fueron aceptados,
//	207 si hubo aceptados y rechazados,
//	422 si todos fueron rechazados,
//	400 si el cuerpo no es JSON válido o está vacío.
func (p *Processor) handleEvents(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "cuerpo demasiado grande"})
			return
		}
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "no se pudo leer el cuerpo"})
		return
	}

	items, err := splitEvents(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	resp := EventsResponse{Received: len(items)}
	for i, raw := range items {
		if err := p.Process(raw, fmt.Sprintf("http:evento %d", i)); err != nil {
			resp.Rejected++
			resp.Errors = append(resp.Errors, RejectInfo{Index: i, EventID: peekEventID(raw), Reason: err.Error()})
		} else {
			resp.Accepted++
		}
	}

	switch {
	case resp.Rejected == 0:
		writeJSON(w, http.StatusAccepted, resp)
	case resp.Accepted == 0:
		writeJSON(w, http.StatusUnprocessableEntity, resp)
	default:
		writeJSON(w, http.StatusMultiStatus, resp)
	}
}

// splitEvents acepta un objeto único o un arreglo de objetos.
func splitEvents(body []byte) ([]json.RawMessage, error) {
	b := trimSpace(body)
	if len(b) == 0 {
		return nil, errors.New("cuerpo vacío")
	}
	if b[0] == '[' {
		var items []json.RawMessage
		if err := json.Unmarshal(b, &items); err != nil {
			return nil, fmt.Errorf("arreglo JSON inválido: %w", err)
		}
		if len(items) == 0 {
			return nil, errors.New("el arreglo no contiene eventos")
		}
		return items, nil
	}
	if !json.Valid(b) {
		return nil, errors.New("JSON inválido")
	}
	return []json.RawMessage{json.RawMessage(b)}, nil
}

// peekEventID intenta leer el eventId aunque el evento sea inválido.
func peekEventID(raw []byte) string {
	var p struct {
		EventID string `json:"eventId"`
	}
	_ = json.Unmarshal(raw, &p)
	return p.EventID
}
