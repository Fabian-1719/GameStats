package sender_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gamestats/go-ingestion/internal/model"
	"gamestats/go-ingestion/internal/sender"
)

func sampleBatch(matchID string) model.Batch {
	return model.Batch{
		MatchID: matchID,
		GameID:  "combat-arena",
		Status:  model.StatusComplete,
		Events: []model.Event{
			{
				EventID:   "e-1",
				Timestamp: "2026-10-10T12:00:00Z",
				GameID:    "combat-arena",
				MatchID:   matchID,
				Type:      "MATCH_STARTED",
			},
			{
				EventID:   "e-2",
				Timestamp: "2026-10-10T12:01:00Z",
				GameID:    "combat-arena",
				MatchID:   matchID,
				Type:      "MATCH_FINISHED",
			},
		},
	}
}

func TestSenderSuccess(t *testing.T) {
	var received int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/analyze" {
			t.Errorf("ruta inesperada: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		var b model.Batch
		if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
			t.Errorf("error decodificando batch: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		atomic.AddInt32(&received, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pool := sender.New(sender.Config{
		ScalaURL: server.URL,
		Workers:  2,
	})

	batches := make(chan model.Batch, 5)
	for i := 1; i <= 5; i++ {
		batches <- sampleBatch("m-" + string(rune('0'+i)))
	}
	close(batches)

	pool.Run(context.Background(), batches)

	if got := atomic.LoadInt32(&received); got != 5 {
		t.Fatalf("se esperaban 5 lotes recibidos, se recibieron %d", got)
	}

	st := pool.Stats()
	if st.Sent != 5 || st.Failed != 0 {
		t.Fatalf("stats incorrectos: %+v", st)
	}
}

func TestSenderRetryAndSuccess(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		att := atomic.AddInt32(&attempts, 1)
		if att == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	pool := sender.New(sender.Config{
		ScalaURL:       server.URL,
		Workers:        1,
		MaxRetries:     2,
		InitialBackoff: 10 * time.Millisecond,
	})

	batches := make(chan model.Batch, 1)
	batches <- sampleBatch("m-retry")
	close(batches)

	pool.Run(context.Background(), batches)

	if got := atomic.LoadInt32(&attempts); got != 2 {
		t.Fatalf("se esperaban 2 intentos, se hicieron %d", got)
	}

	st := pool.Stats()
	if st.Sent != 1 || st.Retries != 1 || st.Failed != 0 {
		t.Fatalf("stats inesperados: %+v", st)
	}
}

func TestSenderPermanentFailureSavesToDisk(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "gamestats-failed-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tempDir)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer server.Close()

	pool := sender.New(sender.Config{
		ScalaURL:       server.URL,
		Workers:        1,
		MaxRetries:     1,
		InitialBackoff: 5 * time.Millisecond,
		FailedBatchDir: tempDir,
	})

	batches := make(chan model.Batch, 1)
	batches <- sampleBatch("m-failed")
	close(batches)

	pool.Run(context.Background(), batches)

	st := pool.Stats()
	if st.Sent != 0 || st.Failed != 1 {
		t.Fatalf("se esperaba 1 fallo registrado: %+v", st)
	}

	entries, err := os.ReadDir(tempDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("se esperaba 1 archivo en %s, hay %d", tempDir, len(entries))
	}

	content, err := os.ReadFile(filepath.Join(tempDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var saved struct {
		Reason string      `json:"reason"`
		Batch  model.Batch `json:"batch"`
	}
	if err := json.Unmarshal(content, &saved); err != nil {
		t.Fatalf("error parseando archivo guardado: %v", err)
	}
	if saved.Batch.MatchID != "m-failed" {
		t.Errorf("partida guardada inesperada: %s", saved.Batch.MatchID)
	}
}
