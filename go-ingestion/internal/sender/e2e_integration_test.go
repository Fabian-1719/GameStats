package sender_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gamestats/go-ingestion/internal/dlq"
	"gamestats/go-ingestion/internal/ingest"
	"gamestats/go-ingestion/internal/match"
	"gamestats/go-ingestion/internal/model"
	"gamestats/go-ingestion/internal/router"
	"gamestats/go-ingestion/internal/sender"
	"gamestats/go-ingestion/internal/validator"
)

// TestEndToEndPipeline verifica el flujo completo:
// Eventos HTTP -> Processor/Validator -> Router -> Worker de partida -> Sender Pool -> Servidor Scala.
func TestEndToEndPipeline(t *testing.T) {
	var scalaBatchesReceived int32
	var mu sync.Mutex
	receivedMatches := make(map[string]model.Batch)

	// 1. Mock de Scala
	scalaServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/analyze" && r.Method == http.MethodPost {
			var b model.Batch
			if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			mu.Lock()
			receivedMatches[b.MatchID] = b
			mu.Unlock()
			atomic.AddInt32(&scalaBatchesReceived, 1)
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer scalaServer.Close()

	// 2. Componentes de Go
	cat, err := validator.LoadCatalog("../../config/catalog.json")
	if err != nil {
		t.Fatalf("cargando catálogo: %v", err)
	}

	val := validator.New(cat)
	devNullDLQ, err := dlq.Open(t.TempDir() + "/rejected.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	defer devNullDLQ.Close()

	rt := router.New(match.Config{
		IdleTimeout: 500 * time.Millisecond,
		FinishGrace: 50 * time.Millisecond,
	}, func(ev model.Event, reason string) {})

	routerCtx, cancelRouter := context.WithCancel(context.Background())
	routerDone := make(chan struct{})
	go func() {
		rt.Run(routerCtx)
		close(routerDone)
	}()

	senderPool := sender.New(sender.Config{
		ScalaURL:       scalaServer.URL,
		Workers:        2,
		FailedBatchDir: t.TempDir(),
	})

	senderDone := make(chan struct{})
	go func() {
		senderPool.Run(context.Background(), rt.Batches())
		close(senderDone)
	}()

	proc := ingest.NewProcessor(val, devNullDLQ, func(ev model.Event) {
		rt.Submit(ev)
	})

	ingestServer := httptest.NewServer(ingest.NewMux(proc))
	defer ingestServer.Close()

	// 3. Enviar 2 partidas completas (una combate, una carreras)
	events := []model.Event{
		// Partida 1: combate
		{EventID: "e-1", Timestamp: "2026-10-10T12:00:00Z", GameID: "combat-arena", GameVersion: "1.0", MatchID: "m-e2e-1", Type: "MATCH_STARTED"},
		{EventID: "e-2", Timestamp: "2026-10-10T12:00:01Z", GameID: "combat-arena", GameVersion: "1.0", MatchID: "m-e2e-1", PlayerID: "p-1", Type: "PLAYER_JOINED"},
		{EventID: "e-3", Timestamp: "2026-10-10T12:00:02Z", GameID: "combat-arena", GameVersion: "1.0", MatchID: "m-e2e-1", Type: "MATCH_FINISHED", Data: map[string]any{"winnerId": "p-1", "scores": map[string]any{"p-1": 100}}},
		// Partida 2: carreras
		{EventID: "e-4", Timestamp: "2026-10-10T12:00:00Z", GameID: "racing-circuit", GameVersion: "1.0", MatchID: "m-e2e-2", Type: "MATCH_STARTED", Data: map[string]any{"track": "Monza", "laps": 1}},
		{EventID: "e-5", Timestamp: "2026-10-10T12:00:01Z", GameID: "racing-circuit", GameVersion: "1.0", MatchID: "m-e2e-2", PlayerID: "p-2", Type: "PLAYER_JOINED"},
		{EventID: "e-6", Timestamp: "2026-10-10T12:00:10Z", GameID: "racing-circuit", GameVersion: "1.0", MatchID: "m-e2e-2", Type: "MATCH_FINISHED", Data: map[string]any{"classification": []any{"p-2"}}},
	}

	for _, ev := range events {
		body, _ := json.Marshal(ev)
		resp, err := http.Post(ingestServer.URL+"/v1/events", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatalf("error posteando evento: %v", err)
		}
		if resp.StatusCode != http.StatusAccepted {
			t.Fatalf("status inesperado: %d", resp.StatusCode)
		}
		resp.Body.Close()
	}

	// 4. Apagar ordenadamente el router para que despache todo
	time.Sleep(100 * time.Millisecond) // Esperar FinishGrace
	cancelRouter()
	<-routerDone
	<-senderDone

	// 5. Verificar que Scala haya recibido exactamente las 2 partidas
	if got := atomic.LoadInt32(&scalaBatchesReceived); got != 2 {
		t.Fatalf("se esperaban 2 lotes en Scala, recibidos: %d", got)
	}

	mu.Lock()
	defer mu.Unlock()
	if b, ok := receivedMatches["m-e2e-1"]; !ok || b.Status != model.StatusComplete || len(b.Events) != 3 {
		t.Errorf("partida m-e2e-1 recibida con formato incorrecto: %+v", b)
	}
	if b, ok := receivedMatches["m-e2e-2"]; !ok || b.Status != model.StatusComplete || len(b.Events) != 3 {
		t.Errorf("partida m-e2e-2 recibida con formato incorrecto: %+v", b)
	}
}
