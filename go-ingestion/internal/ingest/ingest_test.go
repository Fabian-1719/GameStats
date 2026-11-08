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

func TestGameConsoleGameEventsAreAcceptedByRealCatalog(t *testing.T) {
	f := newFixture(t)
	events := `[
		{"eventId":"e-c4-start","timestamp":"2026-10-10T15:00:00Z","gameId":"connect-four","gameVersion":"1.0","matchId":"m-c4","type":"MATCH_STARTED","action":"START","data":{"mode":"VS_AI","players":["p-c4","cpu-prolog"]}},
		{"eventId":"e-c4-join","timestamp":"2026-10-10T15:00:00.001Z","gameId":"connect-four","gameVersion":"1.0","matchId":"m-c4","playerId":"p-c4","type":"PLAYER_JOINED","action":"JOIN","data":{"name":"Alice"}},
		{"eventId":"e-c4-move","timestamp":"2026-10-10T15:00:01Z","gameId":"connect-four","gameVersion":"1.0","matchId":"m-c4","playerId":"p-c4","type":"MOVE","action":"DROP_DISC","data":{"column":3,"row":5,"turn":1}},
		{"eventId":"e-c4-ai","timestamp":"2026-10-10T15:00:02Z","gameId":"connect-four","gameVersion":"1.0","matchId":"m-c4","playerId":"cpu-prolog","type":"AI_DECISION","action":"DROP_DISC","data":{"column":3,"level":2,"nodes":450,"timeMs":8,"score":0}},
		{"eventId":"e-c4-won","timestamp":"2026-10-10T15:00:03Z","gameId":"connect-four","gameVersion":"1.0","matchId":"m-c4","playerId":"p-c4","type":"PLAYER_WON","action":"WIN","data":{"playerId":"p-c4"}},
		{"eventId":"e-c4-finish","timestamp":"2026-10-10T15:00:04Z","gameId":"connect-four","gameVersion":"1.0","matchId":"m-c4","type":"MATCH_FINISHED","action":"FINISH","data":{"winnerId":"p-c4","result":"WIN","durationMs":4000,"moves":1}},
		{"eventId":"e-snake-start","timestamp":"2026-10-10T15:01:00Z","gameId":"snake-arena","gameVersion":"1.0","matchId":"m-snake","type":"MATCH_STARTED","action":"START","data":{"mode":"SOLO","players":["p-snake"]}},
		{"eventId":"e-snake-food","timestamp":"2026-10-10T15:01:01Z","gameId":"snake-arena","gameVersion":"1.0","matchId":"m-snake","playerId":"p-snake","type":"FOOD_EATEN","action":"EAT","data":{"score":10,"length":4}},
		{"eventId":"e-snake-death","timestamp":"2026-10-10T15:01:02Z","gameId":"snake-arena","gameVersion":"1.0","matchId":"m-snake","playerId":"p-snake","type":"PLAYER_DIED","action":"DIE","data":{"cause":"WALL","score":10}},
		{"eventId":"e-snake-finish","timestamp":"2026-10-10T15:01:03Z","gameId":"snake-arena","gameVersion":"1.0","matchId":"m-snake","type":"MATCH_FINISHED","action":"FINISH","data":{"winnerId":null,"result":"LOSS","durationMs":3000,"score":10,"length":4}},
		{"eventId":"e-memory-start","timestamp":"2026-10-10T15:02:00Z","gameId":"memory-match","gameVersion":"1.0","matchId":"m-memory","type":"MATCH_STARTED","action":"START","data":{"mode":"SOLO","players":["p-memory"]}},
		{"eventId":"e-memory-flip","timestamp":"2026-10-10T15:02:01Z","gameId":"memory-match","gameVersion":"1.0","matchId":"m-memory","playerId":"p-memory","type":"CARD_FLIPPED","action":"FLIP","data":{"cardIndex":2}},
		{"eventId":"e-memory-pair","timestamp":"2026-10-10T15:02:02Z","gameId":"memory-match","gameVersion":"1.0","matchId":"m-memory","playerId":"p-memory","type":"PAIR_MATCHED","action":"MATCH","data":{"pairId":"pair-A","attempts":1}},
		{"eventId":"e-memory-finish","timestamp":"2026-10-10T15:02:03Z","gameId":"memory-match","gameVersion":"1.0","matchId":"m-memory","type":"MATCH_FINISHED","action":"FINISH","data":{"winnerId":"p-memory","result":"WIN","durationMs":3000,"pairs":1,"attempts":1}}
	]`

	rec, resp := f.post(events)
	if rec.Code != http.StatusAccepted || resp.Received != 14 || resp.Accepted != 14 || resp.Rejected != 0 {
		t.Fatalf("GameConsole events should all pass the real GameStats catalog: status=%d response=%+v body=%s",
			rec.Code, resp, rec.Body.String())
	}
	if stats := f.proc.Stats(); stats.Accepted != 14 || stats.Rejected != 0 {
		t.Fatalf("unexpected ingestion totals: %+v", stats)
	}
	if rejected := f.rejectedLines(t); len(rejected) != 0 {
		t.Fatalf("valid GameConsole events were written to the rejection log: %+v", rejected)
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
