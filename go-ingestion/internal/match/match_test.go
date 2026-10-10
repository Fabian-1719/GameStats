package match

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"gamestats/go-ingestion/internal/model"
)

var t0 = time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC)

func mk(id, typ, player string, offset time.Duration) model.Event {
	return model.Event{
		EventID: id, Timestamp: t0.Add(offset).Format(time.RFC3339Nano),
		GameID: "combat-arena", GameVersion: "1.0", MatchID: "m-1",
		PlayerID: player, Type: typ,
	}
}

func ids(evs []model.Event) []string {
	out := make([]string, len(evs))
	for i, e := range evs {
		out[i] = e.EventID
	}
	return out
}

func TestStateDropsDuplicates(t *testing.T) {
	s := NewState()
	if !s.Add(mk("a", "SHOT_FIRED", "p1", 0)) {
		t.Fatal("el primero debe aceptarse")
	}
	if s.Add(mk("a", "SHOT_FIRED", "p1", 0)) {
		t.Fatal("el repetido debe descartarse")
	}
	if s.EventCount() != 1 || s.Duplicates() != 1 {
		t.Fatalf("count=%d dups=%d", s.EventCount(), s.Duplicates())
	}
}

func TestStateSortsByTimestampWithDeterministicTieBreak(t *testing.T) {
	s := NewState()
	s.Add(mk("c", "SHOT_FIRED", "p1", 3*time.Second))
	s.Add(mk("b", "SHOT_FIRED", "p1", time.Second))
	s.Add(mk("a2", "SHOT_FIRED", "p1", 2*time.Second))
	s.Add(mk("a1", "SHOT_FIRED", "p1", 2*time.Second)) // mismo instante que a2
	want := []string{"b", "a1", "a2", "c"}
	if got := ids(s.Events()); !reflect.DeepEqual(got, want) {
		t.Fatalf("orden = %v, esperaba %v", got, want)
	}
}

func TestStatePhaseDoesNotDependOnArrivalOrder(t *testing.T) {
	// MATCH_FINISHED llega antes que MATCH_STARTED: debe quedar FINISHED.
	s := NewState()
	if s.Phase() != PhaseWaiting {
		t.Fatalf("fase inicial = %s", s.Phase())
	}
	s.Add(mk("x", "SHOT_FIRED", "p1", time.Second)) // antes de MATCH_STARTED
	if s.Phase() != PhaseWaiting {
		t.Fatalf("un evento previo no inicia la partida: %s", s.Phase())
	}
	s.Add(mk("f", "MATCH_FINISHED", "", 10*time.Second))
	s.Add(mk("s", "MATCH_STARTED", "", 0))
	if s.Phase() != PhaseFinished {
		t.Fatalf("fase = %s, esperaba FINISHED", s.Phase())
	}
}

func TestStateActivePlayersUnderDisorder(t *testing.T) {
	s := NewState()
	// p2 sale a los 5 s pero ese evento llega ANTES que su PLAYER_JOINED.
	s.Add(mk("l2", "PLAYER_LEFT", "p2", 5*time.Second))
	s.Add(mk("j1", "PLAYER_JOINED", "p1", time.Second))
	s.Add(mk("j2", "PLAYER_JOINED", "p2", 2*time.Second))
	s.Add(mk("j3", "PLAYER_JOINED", "p3", 3*time.Second))
	if got, want := s.ActivePlayers(), []string{"p1", "p3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("activos = %v, esperaba %v", got, want)
	}
}

func runWorker(cfg Config, flush <-chan struct{}) (*Worker, chan model.Batch, chan struct{}) {
	out := make(chan model.Batch, 1)
	w := NewWorker("combat-arena", "m-1", cfg, out)
	done := make(chan struct{})
	go func() { w.Run(flush); close(done) }()
	return w, out, done
}

func waitBatch(t *testing.T, out <-chan model.Batch) model.Batch {
	t.Helper()
	select {
	case b := <-out:
		return b
	case <-time.After(3 * time.Second):
		t.Fatal("no llegó el lote a tiempo")
		return model.Batch{}
	}
}

func TestWorkerFlushesAfterGraceAndKeepsLateArrivals(t *testing.T) {
	w, out, _ := runWorker(Config{IdleTimeout: time.Minute, FinishGrace: 150 * time.Millisecond}, make(chan struct{}))
	w.In() <- mk("fin", "MATCH_FINISHED", "", 10*time.Second)
	// Llega después de MATCH_FINISHED, pero dentro del período de gracia.
	w.In() <- mk("early", "SHOT_FIRED", "p1", time.Second)

	b := waitBatch(t, out)
	if b.Status != model.StatusComplete {
		t.Fatalf("status = %s", b.Status)
	}
	if got, want := ids(b.Events), []string{"early", "fin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("eventos = %v, esperaba %v", got, want)
	}
}

func TestWorkerIdleTimeout(t *testing.T) {
	w, out, _ := runWorker(Config{IdleTimeout: 100 * time.Millisecond, FinishGrace: time.Minute}, make(chan struct{}))
	w.In() <- mk("s", "MATCH_STARTED", "", 0)
	if b := waitBatch(t, out); b.Status != model.StatusTimeout || len(b.Events) != 1 {
		t.Fatalf("lote = %+v", b)
	}
}

func TestWorkerIdleTimerResetsOnEachEvent(t *testing.T) {
	w, out, _ := runWorker(Config{IdleTimeout: 200 * time.Millisecond, FinishGrace: time.Minute}, make(chan struct{}))
	for i := 0; i < 4; i++ { // 4 x 100 ms > 200 ms, pero nunca hay 200 ms de silencio
		w.In() <- mk(fmt.Sprintf("e%d", i), "SHOT_FIRED", "p1", time.Duration(i)*time.Second)
		time.Sleep(100 * time.Millisecond)
	}
	select {
	case <-out:
		t.Fatal("el timeout disparó aunque seguían llegando eventos")
	default:
	}
	if b := waitBatch(t, out); len(b.Events) != 4 {
		t.Fatalf("eventos = %d", len(b.Events))
	}
}

func TestWorkerFlushOnShutdown(t *testing.T) {
	flush := make(chan struct{})
	w, out, _ := runWorker(Config{IdleTimeout: time.Minute, FinishGrace: time.Minute}, flush)
	w.In() <- mk("s", "MATCH_STARTED", "", 0)
	close(flush)
	if b := waitBatch(t, out); b.Status != model.StatusShutdown {
		t.Fatalf("status = %s", b.Status)
	}
}

func TestWorkerRejectsNothingAfterFinished(t *testing.T) {
	w, out, done := runWorker(Config{IdleTimeout: 50 * time.Millisecond}, make(chan struct{}))
	w.In() <- mk("s", "MATCH_STARTED", "", 0)
	waitBatch(t, out)
	<-done
	select {
	case <-w.Finished():
	default:
		t.Fatal("Finished debe estar cerrado")
	}
}
