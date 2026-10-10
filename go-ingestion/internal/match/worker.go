package match

import (
	"time"

	"gamestats/go-ingestion/internal/model"
)

// Config controla cuándo una partida se considera lista.
type Config struct {
	// IdleTimeout: sin eventos durante este tiempo, la partida se envía como TIMEOUT.
	IdleTimeout time.Duration
	// FinishGrace: tras ver MATCH_FINISHED se espera este tiempo antes de enviar,
	// porque eventos anteriores pueden llegar tarde por el desorden.
	FinishGrace time.Duration
}

func (c Config) withDefaults() Config {
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = 30 * time.Second
	}
	if c.FinishGrace <= 0 {
		c.FinishGrace = 2 * time.Second
	}
	return c
}

// Worker es la goroutine de UNA partida. Recibe sus eventos por un channel sin
// buffer, así que si el router logró entregarle un evento, el worker lo recibió.
type Worker struct {
	gameID, matchID string
	cfg             Config
	in              chan model.Event
	finished        chan struct{}
	out             chan<- model.Batch
	state           *State
}

func NewWorker(gameID, matchID string, cfg Config, out chan<- model.Batch) *Worker {
	return &Worker{
		gameID:   gameID,
		matchID:  matchID,
		cfg:      cfg.withDefaults(),
		in:       make(chan model.Event),
		finished: make(chan struct{}),
		out:      out,
		state:    NewState(),
	}
}

// In es el channel por donde el router entrega eventos.
func (w *Worker) In() chan<- model.Event { return w.in }

// Finished se cierra cuando el worker dejó de aceptar eventos.
func (w *Worker) Finished() <-chan struct{} { return w.finished }

// Run procesa eventos hasta que la partida está lista y entonces emite su lote.
// La partida está lista cuando: pasó FinishGrace desde MATCH_FINISHED, pasó
// IdleTimeout sin eventos, o se cerró flush (apagado del servicio).
//
// Run bloquea al enviar el lote: quien lea el channel de salida debe seguir
// leyendo hasta que se cierre.
func (w *Worker) Run(flush <-chan struct{}) {
	idle := time.NewTimer(w.cfg.IdleTimeout)
	defer idle.Stop()

	var graceTimer *time.Timer
	var graceC <-chan time.Time // nil hasta ver MATCH_FINISHED
	defer func() {
		if graceTimer != nil {
			graceTimer.Stop()
		}
	}()

	timedOut := false
loop:
	for {
		select {
		case ev := <-w.in:
			w.state.Add(ev)
			if graceC == nil && w.state.Phase() == PhaseFinished {
				graceTimer = time.NewTimer(w.cfg.FinishGrace)
				graceC = graceTimer.C
			}
			resetTimer(idle, w.cfg.IdleTimeout)
		case <-graceC:
			break loop
		case <-idle.C:
			timedOut = true
			break loop
		case <-flush:
			break loop
		}
	}

	// Primero avisamos que ya no aceptamos eventos, y solo después enviamos el
	// lote: así el router nunca se queda bloqueado esperando a un worker que
	// está ocupado entregando su resultado.
	close(w.finished)

	if w.state.EventCount() == 0 {
		return
	}
	status := model.StatusShutdown
	switch {
	case w.state.Phase() == PhaseFinished:
		status = model.StatusComplete
	case timedOut:
		status = model.StatusTimeout
	}
	w.out <- model.Batch{
		MatchID: w.matchID,
		GameID:  w.gameID,
		Status:  status,
		Events:  w.state.Events(),
	}
}

// resetTimer reinicia un timer de forma segura (vaciando su channel si ya disparó).
func resetTimer(t *time.Timer, d time.Duration) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
	t.Reset(d)
}
