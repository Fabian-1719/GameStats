// Package match mantiene el estado de una partida y la goroutine que lo gestiona.
package match

import (
	"sort"
	"time"

	"gamestats/go-ingestion/internal/model"
)

// Phase es el estado básico de una partida.
type Phase string

const (
	PhaseWaiting  Phase = "WAITING"  // llegaron eventos pero aún no se vio MATCH_STARTED
	PhaseStarted  Phase = "STARTED"  // se vio MATCH_STARTED
	PhaseFinished Phase = "FINISHED" // se vio MATCH_FINISHED
)

type stamped struct {
	ev model.Event
	t  time.Time
}

// State acumula los eventos de UNA partida. No usa locks: lo toca solo la
// goroutine de su worker (o un test, que es secuencial).
//
// Los eventos llegan desordenados, así que ninguna decisión depende del orden
// de llegada: la fase y los jugadores activos se calculan a partir de los
// eventos, y la lista final se ordena por timestamp.
type State struct {
	seen   map[string]struct{}
	events []stamped
	phase  Phase
	dups   int
}

func NewState() *State {
	return &State{seen: make(map[string]struct{}), phase: PhaseWaiting}
}

// Add incorpora un evento. Devuelve false si era un duplicado (mismo eventId).
func (s *State) Add(ev model.Event) bool {
	if _, dup := s.seen[ev.EventID]; dup {
		s.dups++
		return false
	}
	s.seen[ev.EventID] = struct{}{}
	t, _ := ev.Time() // ya validado antes de llegar aquí
	s.events = append(s.events, stamped{ev: ev, t: t})

	switch ev.Type {
	case "MATCH_STARTED":
		// Nunca "retrocede": si MATCH_FINISHED llegó antes, la fase sigue FINISHED.
		if s.phase == PhaseWaiting {
			s.phase = PhaseStarted
		}
	case "MATCH_FINISHED":
		s.phase = PhaseFinished
	}
	return true
}

func (s *State) Phase() Phase    { return s.phase }
func (s *State) EventCount() int { return len(s.events) }
func (s *State) Duplicates() int { return s.dups }

func (s *State) sorted() []stamped {
	out := make([]stamped, len(s.events))
	copy(out, s.events)
	sort.SliceStable(out, func(i, j int) bool {
		if !out[i].t.Equal(out[j].t) {
			return out[i].t.Before(out[j].t)
		}
		return out[i].ev.EventID < out[j].ev.EventID // desempate determinista
	})
	return out
}

// Events devuelve los eventos ordenados por timestamp.
func (s *State) Events() []model.Event {
	st := s.sorted()
	out := make([]model.Event, len(st))
	for i, x := range st {
		out[i] = x.ev
	}
	return out
}

// ActivePlayers devuelve los jugadores que entraron y no han salido. Se calcula
// recorriendo los eventos en orden temporal, por eso un PLAYER_LEFT que llegó
// antes que su PLAYER_JOINED no deja al jugador "activo" por error.
func (s *State) ActivePlayers() []string {
	active := map[string]struct{}{}
	for _, x := range s.sorted() {
		switch x.ev.Type {
		case "PLAYER_JOINED":
			active[x.ev.PlayerID] = struct{}{}
		case "PLAYER_LEFT":
			delete(active, x.ev.PlayerID)
		}
	}
	out := make([]string, 0, len(active))
	for p := range active {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}
