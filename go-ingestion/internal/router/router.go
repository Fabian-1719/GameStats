// Package router reparte los eventos válidos entre las goroutines de partida.
package router

import (
	"context"
	"sync"
	"sync/atomic"

	"gamestats/go-ingestion/internal/match"
	"gamestats/go-ingestion/internal/model"
)

// LateFunc se invoca con un evento que no pudo entregarse a su partida porque
// esa partida ya fue enviada a análisis.
type LateFunc func(ev model.Event, reason string)

const lateReason = "partida ya enviada a análisis"

// Stats resume la actividad del router.
type Stats struct {
	Matches int64 // partidas (workers) creadas
	Late    int64 // eventos que llegaron después de enviar su partida
}

// Router recibe eventos por un channel y los despacha por partida. Solo la
// goroutine de Run toca el mapa de workers, por eso no necesita locks.
type Router struct {
	cfg    match.Config
	onLate LateFunc

	in    chan model.Event
	out   chan model.Batch
	quit  chan struct{} // se cierra al empezar el apagado: Submit deja de aceptar
	flush chan struct{} // se cierra cuando los workers deben enviar lo que tengan

	workers map[string]*match.Worker // solo la goroutine de Run lo toca
	wg      sync.WaitGroup

	matches atomic.Int64
	late    atomic.Int64
}

// New crea el router. onLate puede ser nil.
func New(cfg match.Config, onLate LateFunc) *Router {
	return &Router{
		cfg:     cfg,
		onLate:  onLate,
		in:      make(chan model.Event),
		out:     make(chan model.Batch, 64),
		quit:    make(chan struct{}),
		flush:   make(chan struct{}),
		workers: make(map[string]*match.Worker),
	}
}

// Submit entrega un evento al router. Devuelve false si el router ya se está
// apagando (el evento no fue aceptado). Es seguro llamarlo desde muchas goroutines.
func (r *Router) Submit(ev model.Event) bool {
	select {
	case <-r.quit:
		return false
	default:
	}
	select {
	case r.in <- ev:
		return true
	case <-r.quit:
		return false
	}
}

// Batches es el channel de salida con un lote por partida lista. Se cierra
// cuando Run termina; quien lo consuma debe leerlo hasta ese cierre.
func (r *Router) Batches() <-chan model.Batch { return r.out }

// Stats devuelve contadores acumulados.
func (r *Router) Stats() Stats {
	return Stats{Matches: r.matches.Load(), Late: r.late.Load()}
}

// Run despacha eventos hasta que ctx se cancela. Al cancelarse deja de aceptar
// eventos nuevos, hace que cada partida en curso envíe lo que tiene (con
// estado COMPLETE o SHUTDOWN), espera a que terminen todos los workers y cierra
// el channel de lotes. Todo evento aceptado por Submit llega a su partida antes
// de que se inicie el cierre, porque cada despacho se completa antes de
// volver a mirar ctx.
func (r *Router) Run(ctx context.Context) {
	defer close(r.out)
	for {
		select {
		case ev := <-r.in:
			r.dispatch(ev)
		case <-ctx.Done():
			close(r.quit)
			close(r.flush)
			r.wg.Wait()
			return
		}
	}
}

// key identifica una partida. Incluye el juego porque dos juegos distintos
// podrían reutilizar el mismo matchId.
func key(ev model.Event) string { return ev.GameID + "|" + ev.MatchID }

func (r *Router) dispatch(ev model.Event) {
	k := key(ev)
	w, ok := r.workers[k]
	if !ok {
		w = match.NewWorker(ev.GameID, ev.MatchID, r.cfg, r.out)
		r.workers[k] = w
		r.matches.Add(1)
		r.wg.Add(1)
		go func() {
			defer r.wg.Done()
			w.Run(r.flush)
		}()
	}

	// El worker terminado se queda en el mapa como "lápida": así un evento
	// tardío se detecta y se informa en lugar de crear una partida duplicada.
	select {
	case w.In() <- ev:
	case <-w.Finished():
		r.late.Add(1)
		if r.onLate != nil {
			r.onLate(ev, lateReason)
		}
	}
}
