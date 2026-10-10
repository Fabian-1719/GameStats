package router

import (
	"context"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
	"time"

	"gamestats/go-ingestion/internal/match"
	"gamestats/go-ingestion/internal/model"
)

var t0 = time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)

func mk(id, game, matchID, typ, player string, offset time.Duration) model.Event {
	return model.Event{
		EventID: id, Timestamp: t0.Add(offset).Format(time.RFC3339Nano),
		GameID: game, GameVersion: "1.0", MatchID: matchID,
		PlayerID: player, Type: typ,
	}
}

// matchEvents genera la partida completa: inicio, 2 jugadores, n disparos y fin.
func matchEvents(game, matchID string, shots int) []model.Event {
	evs := []model.Event{
		mk(matchID+"-start", game, matchID, "MATCH_STARTED", "", 0),
		mk(matchID+"-j1", game, matchID, "PLAYER_JOINED", "p1", 100*time.Millisecond),
		mk(matchID+"-j2", game, matchID, "PLAYER_JOINED", "p2", 200*time.Millisecond),
	}
	for i := 0; i < shots; i++ {
		evs = append(evs, mk(fmt.Sprintf("%s-s%02d", matchID, i), game, matchID, "SHOT_FIRED", "p1",
			time.Duration(300+i*100)*time.Millisecond))
	}
	evs = append(evs, mk(matchID+"-end", game, matchID, "MATCH_FINISHED", "", time.Duration(300+shots*100)*time.Millisecond))
	return evs
}

// start arranca el router y un consumidor que junta todos los lotes.
func start(t *testing.T, cfg match.Config, onLate LateFunc) (r *Router, stop func() []model.Batch) {
	t.Helper()
	r = New(cfg, onLate)
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { r.Run(ctx); close(runDone) }()

	var mu sync.Mutex
	var batches []model.Batch
	consumed := make(chan struct{})
	go func() {
		for b := range r.Batches() {
			mu.Lock()
			batches = append(batches, b)
			mu.Unlock()
		}
		close(consumed)
	}()

	return r, func() []model.Batch {
		cancel()
		select {
		case <-runDone:
		case <-time.After(5 * time.Second):
			t.Fatal("Run no terminó: posible deadlock en el apagado")
		}
		<-consumed
		mu.Lock()
		defer mu.Unlock()
		return batches
	}
}

func idsOf(evs []model.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventID
	}
	return out
}

// 100 partidas simultáneas de dos juegos, con desorden y ~10 % de duplicados,
// alimentadas desde 8 goroutines. Ejecutar con -race.
func TestHundredConcurrentMatches(t *testing.T) {
	const numMatches, shots, producers = 100, 20, 8

	rng := rand.New(rand.NewSource(42))
	want := map[string][]string{} // matchId -> eventIds esperados, en orden temporal
	var stream []model.Event
	for i := 0; i < numMatches; i++ {
		game := "combat-arena"
		if i%2 == 1 {
			game = "racing-circuit"
		}
		matchID := fmt.Sprintf("m-%03d", i)
		evs := matchEvents(game, matchID, shots)
		want[matchID] = idsOf(evs)
		stream = append(stream, evs...)
		for _, e := range evs { // duplicados
			if rng.Intn(10) == 0 {
				stream = append(stream, e)
			}
		}
	}
	rng.Shuffle(len(stream), func(i, j int) { stream[i], stream[j] = stream[j], stream[i] })

	var late []model.Event
	var lateMu sync.Mutex
	r, stop := start(t, match.Config{IdleTimeout: time.Minute, FinishGrace: time.Minute},
		func(ev model.Event, _ string) { lateMu.Lock(); late = append(late, ev); lateMu.Unlock() })

	var wg sync.WaitGroup
	for p := 0; p < producers; p++ {
		wg.Add(1)
		go func(p int) {
			defer wg.Done()
			for i := p; i < len(stream); i += producers {
				if !r.Submit(stream[i]) {
					t.Error("Submit rechazó un evento con el router activo")
				}
			}
		}(p)
	}
	wg.Wait()
	batches := stop()

	if len(batches) != numMatches {
		t.Fatalf("lotes = %d, esperaba %d", len(batches), numMatches)
	}
	if len(late) != 0 {
		t.Fatalf("no debía haber eventos tardíos: %d", len(late))
	}
	seen := map[string]bool{}
	for _, b := range batches {
		if seen[b.MatchID] {
			t.Fatalf("partida %s enviada dos veces", b.MatchID)
		}
		seen[b.MatchID] = true
		if b.Status != model.StatusComplete {
			t.Errorf("%s: status = %s", b.MatchID, b.Status)
		}
		if got := idsOf(b.Events); !reflect.DeepEqual(got, want[b.MatchID]) {
			t.Fatalf("%s: eventos incorrectos (sin duplicados y ordenados)\n got=%v\nwant=%v", b.MatchID, got, want[b.MatchID])
		}
		for _, e := range b.Events {
			if e.MatchID != b.MatchID || e.GameID != b.GameID {
				t.Fatalf("evento de otra partida en el lote %s", b.MatchID)
			}
		}
	}
	if st := r.Stats(); st.Matches != numMatches || st.Late != 0 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestSameMatchIDInDifferentGamesAreDifferentMatches(t *testing.T) {
	r, stop := start(t, match.Config{}, nil)
	r.Submit(mk("a", "combat-arena", "m-1", "MATCH_STARTED", "", 0))
	r.Submit(mk("b", "racing-circuit", "m-1", "MATCH_STARTED", "", 0))
	batches := stop()
	if len(batches) != 2 {
		t.Fatalf("lotes = %d, esperaba 2", len(batches))
	}
	games := []string{batches[0].GameID, batches[1].GameID}
	sort.Strings(games)
	if !reflect.DeepEqual(games, []string{"combat-arena", "racing-circuit"}) {
		t.Fatalf("juegos = %v", games)
	}
}

func TestMatchesFlushThemselvesAfterFinishWithoutShutdown(t *testing.T) {
	r := New(match.Config{IdleTimeout: time.Minute, FinishGrace: 200 * time.Millisecond}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	for i := 0; i < 10; i++ {
		for _, e := range matchEvents("combat-arena", fmt.Sprintf("m-%d", i), 5) {
			r.Submit(e)
		}
	}
	got := 0
	timeout := time.After(5 * time.Second)
	for got < 10 {
		select {
		case b := <-r.Batches():
			if b.Status != model.StatusComplete || len(b.Events) != 9 {
				t.Fatalf("lote %s: status=%s eventos=%d", b.MatchID, b.Status, len(b.Events))
			}
			got++
		case <-timeout:
			t.Fatalf("solo llegaron %d de 10 lotes", got)
		}
	}
}

func TestNeverEndingMatchIsSentByIdleTimeout(t *testing.T) {
	r := New(match.Config{IdleTimeout: 150 * time.Millisecond, FinishGrace: time.Minute}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	r.Submit(mk("a", "combat-arena", "m-1", "MATCH_STARTED", "", 0))
	r.Submit(mk("b", "combat-arena", "m-1", "PLAYER_JOINED", "p1", time.Second))
	select {
	case b := <-r.Batches():
		if b.Status != model.StatusTimeout || len(b.Events) != 2 {
			t.Fatalf("lote = %+v", b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("la partida que nunca termina no se envió")
	}
}

func TestLateEventAfterMatchWasSentIsReportedNotDuplicated(t *testing.T) {
	var mu sync.Mutex
	var lateIDs []string
	r := New(match.Config{IdleTimeout: time.Minute, FinishGrace: 50 * time.Millisecond},
		func(ev model.Event, reason string) { mu.Lock(); lateIDs = append(lateIDs, ev.EventID); mu.Unlock() })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	for _, e := range matchEvents("combat-arena", "m-1", 2) {
		r.Submit(e)
	}
	select {
	case <-r.Batches():
	case <-time.After(3 * time.Second):
		t.Fatal("no llegó el lote")
	}

	r.Submit(mk("tarde", "combat-arena", "m-1", "SHOT_FIRED", "p1", time.Second))

	// Submit vuelve cuando el router recibe el evento; el despacho ocurre un
	// instante después, así que esperamos a que se registre.
	deadline := time.Now().Add(2 * time.Second)
	for r.Stats().Late < 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !reflect.DeepEqual(lateIDs, []string{"tarde"}) || r.Stats().Late != 1 {
		t.Fatalf("tardíos = %v, stats = %+v", lateIDs, r.Stats())
	}
	select {
	case b := <-r.Batches():
		t.Fatalf("no debía crearse un segundo lote: %+v", b)
	case <-time.After(300 * time.Millisecond):
	}
}

func TestShutdownFlushesInProgressMatchesAndSubmitStopsAccepting(t *testing.T) {
	r, stop := start(t, match.Config{IdleTimeout: time.Minute, FinishGrace: time.Minute}, nil)
	for i := 0; i < 3; i++ {
		id := fmt.Sprintf("m-%d", i)
		r.Submit(mk(id+"-a", "combat-arena", id, "MATCH_STARTED", "", 0))
		r.Submit(mk(id+"-b", "combat-arena", id, "PLAYER_JOINED", "p1", time.Second))
	}
	batches := stop()
	if len(batches) != 3 {
		t.Fatalf("lotes = %d, esperaba 3", len(batches))
	}
	for _, b := range batches {
		if b.Status != model.StatusShutdown || len(b.Events) != 2 {
			t.Fatalf("lote = %+v", b)
		}
	}
	if r.Submit(mk("z", "combat-arena", "m-9", "MATCH_STARTED", "", 0)) {
		t.Fatal("Submit debe devolver false tras el apagado")
	}
}
