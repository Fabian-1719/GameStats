package ingest

import (
	"bufio"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gamestats/go-ingestion/internal/dlq"
	"gamestats/go-ingestion/internal/model"
	"gamestats/go-ingestion/internal/validator"
)

const validHit = `{"eventId":"e-1","timestamp":"2026-10-10T15:04:05.120Z","gameId":"combat-arena","gameVersion":"1.0","matchId":"m-1","playerId":"p-1","type":"SHOT_HIT","action":"RIFLE","data":{"targetId":"p-2","damage":27}}`

// Falta data.damage.
const invalidHit = `{"eventId":"e-2","timestamp":"2026-10-10T15:04:06Z","gameId":"combat-arena","gameVersion":"1.0","matchId":"m-1","playerId":"p-1","type":"SHOT_HIT","data":{"targetId":"p-2"}}`

type fixture struct {
	proc     *Processor
	mux      http.Handler
	rejected string // ruta de rejected.ndjson
	mu       sync.Mutex
	got      []model.Event
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	catalog, err := validator.LoadCatalog("../../config/catalog.json")
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{rejected: filepath.Join(t.TempDir(), "rejected.ndjson")}
	d, err := dlq.Open(f.rejected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { d.Close() })
	f.proc = NewProcessor(validator.New(catalog), d, func(e model.Event) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.got = append(f.got, e)
	})
	f.mux = NewMux(f.proc)
	return f
}

func (f *fixture) post(body string) (*httptest.ResponseRecorder, EventsResponse) {
	req := httptest.NewRequest(http.MethodPost, "/v1/events", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, req)
	var resp EventsResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	return rec, resp
}

func (f *fixture) rejectedLines(t *testing.T) []dlq.Record {
	t.Helper()
	file, err := os.Open(f.rejected)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	var out []dlq.Record
	sc := bufio.NewScanner(file)
	for sc.Scan() {
		var r dlq.Record
		if err := json.Unmarshal(sc.Bytes(), &r); err != nil {
			t.Fatalf("línea de DLQ inválida: %v", err)
		}
		out = append(out, r)
	}
	return out
}

func TestHTTPValidAndInvalidGetDifferentResponses(t *testing.T) {
	f := newFixture(t)

	recOK, respOK := f.post(validHit)
	if recOK.Code != http.StatusAccepted || respOK.Accepted != 1 {
		t.Fatalf("evento válido: status=%d resp=%+v", recOK.Code, respOK)
	}

	recBad, respBad := f.post(invalidHit)
	if recBad.Code != http.StatusUnprocessableEntity || respBad.Rejected != 1 {
		t.Fatalf("evento inválido: status=%d resp=%+v", recBad.Code, respBad)
	}
	if len(respBad.Errors) != 1 || respBad.Errors[0].EventID != "e-2" || !strings.Contains(respBad.Errors[0].Reason, "damage") {
		t.Fatalf("el rechazo debe indicar eventId y motivo: %+v", respBad.Errors)
	}

	if len(f.got) != 1 || f.got[0].EventID != "e-1" {
		t.Fatalf("solo el válido debe llegar al sink: %+v", f.got)
	}
	if lines := f.rejectedLines(t); len(lines) != 1 || !strings.Contains(lines[0].Reason, "damage") {
		t.Fatalf("la DLQ debe tener el rechazado con su motivo: %+v", lines)
	}
}

func TestHTTPBatchMixedReturns207(t *testing.T) {
	f := newFixture(t)
	rec, resp := f.post("[" + validHit + "," + invalidHit + "]")
	if rec.Code != http.StatusMultiStatus {
		t.Fatalf("status = %d, esperaba 207", rec.Code)
	}
	if resp.Received != 2 || resp.Accepted != 1 || resp.Rejected != 1 || resp.Errors[0].Index != 1 {
		t.Fatalf("resumen incorrecto: %+v", resp)
	}
}

func TestHTTPBadBodies(t *testing.T) {
	f := newFixture(t)
	for name, body := range map[string]string{
		"vacío":           "",
		"no es JSON":      "hola",
		"arreglo vacío":   "[]",
		"arreglo cortado": `[{"eventId":`,
		"objeto cortado":  `{"eventId":"e-1"`,
	} {
		rec, _ := f.post(body)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, esperaba 400", name, rec.Code)
		}
	}
}

func TestHTTPWrongTypesInsideEventAreRejectedNotCrash(t *testing.T) {
	f := newFixture(t)
	// timestamp numérico: JSON válido pero tipo incorrecto para Event.
	rec, resp := f.post(`{"eventId":"e-9","timestamp":12345}`)
	if rec.Code != http.StatusUnprocessableEntity || resp.Rejected != 1 {
		t.Fatalf("status=%d resp=%+v", rec.Code, resp)
	}
}

func TestHealth(t *testing.T) {
	f := newFixture(t)
	rec := httptest.NewRecorder()
	f.mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"status":"ok"`) {
		t.Fatalf("health: %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoadNDJSON(t *testing.T) {
	f := newFixture(t)
	data := validHit + "\n\n" + invalidHit + "\nesto no es json\n" + validHit + "\n"
	st, err := f.proc.LoadNDJSON(strings.NewReader(data), "prueba.ndjson")
	if err != nil {
		t.Fatal(err)
	}
	if st.Total != 4 || st.Accepted != 2 || st.Rejected != 2 {
		t.Fatalf("stats = %+v", st)
	}
	lines := f.rejectedLines(t)
	if len(lines) != 2 {
		t.Fatalf("DLQ con %d líneas, esperaba 2", len(lines))
	}
	// La línea 3 del archivo (contando la vacía) es el inválido; la 4 no es JSON.
	if !strings.Contains(lines[0].Source, "línea 3") || !strings.Contains(lines[1].Source, "línea 4") {
		t.Fatalf("la DLQ debe indicar la línea de origen: %+v", lines)
	}
}

// Los handlers HTTP corren en goroutines distintas: este test, con -race,
// comprueba que contadores y DLQ resisten escrituras concurrentes.
func TestConcurrentIngestion(t *testing.T) {
	f := newFixture(t)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f.post(validHit)
			f.post(invalidHit)
		}()
	}
	wg.Wait()
	st := f.proc.Stats()
	if st.Accepted != 50 || st.Rejected != 50 {
		t.Fatalf("stats = %+v", st)
	}
	if n := len(f.rejectedLines(t)); n != 50 {
		t.Fatalf("DLQ con %d líneas, esperaba 50", n)
	}
}
