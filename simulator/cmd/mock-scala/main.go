// Servidor mock de Scala para pruebas de extremo a extremo de Go.
// Escucha en :8081 y simula el endpoint POST /v1/analyze.
package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
)

type Batch struct {
	MatchID string           `json:"matchId"`
	GameID  string           `json:"gameId"`
	Status  string           `json:"status"`
	Events  []map[string]any `json:"events"`
}

func main() {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"UP"}`)
	})

	http.HandleFunc("/v1/analyze", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "método no permitido", http.StatusMethodNotAllowed)
			return
		}
		var b Batch
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		log.Printf("[mock-scala] lote recibido: partida=%s juego=%s estado=%s eventos=%d",
			b.MatchID, b.GameID, b.Status, len(b.Events))
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, `{"status":"SUCCESS"}`)
	})

	log.Printf("mock de Scala escuchando en :%s", port)
	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}
}
