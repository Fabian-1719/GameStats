// Package dlq guarda los eventos rechazados en un archivo NDJSON junto con el
// motivo, para poder revisarlos después.
package dlq

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Record es una línea de rejected.ndjson.
type Record struct {
	RejectedAt time.Time `json:"rejectedAt"`
	Source     string    `json:"source"`
	Reason     string    `json:"reason"`
	Raw        string    `json:"raw"`
}

// Writer escribe rechazos; es seguro para uso concurrente (varios handlers HTTP).
type Writer struct {
	mu  sync.Mutex
	f   *os.File
	enc *json.Encoder
}

// Open abre (o crea) el archivo en modo append.
func Open(path string) (*Writer, error) {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &Writer{f: f, enc: json.NewEncoder(f)}, nil
}

// Reject registra un evento rechazado. raw es el contenido original tal como llegó.
func (w *Writer) Reject(raw []byte, reason, source string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.enc.Encode(Record{
		RejectedAt: time.Now().UTC(),
		Source:     source,
		Reason:     reason,
		Raw:        string(raw),
	})
}

func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.f.Close()
}
