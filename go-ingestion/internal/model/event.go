// Package model define el evento comun de GameStats.
package model

import "time"

// Event es un hecho ocurrido dentro de una partida. Los campos comunes
// identifican juego, partida y jugador; Data lleva lo especifico de cada juego.
type Event struct {
	EventID     string         `json:"eventId"`
	Timestamp   string         `json:"timestamp"`
	GameID      string         `json:"gameId"`
	GameVersion string         `json:"gameVersion"`
	MatchID     string         `json:"matchId"`
	PlayerID    string         `json:"playerId,omitempty"`
	Type        string         `json:"type"`
	Action      string         `json:"action,omitempty"`
	Data        map[string]any `json:"data,omitempty"`
}

// Time interpreta Timestamp como RFC 3339 (con o sin fraccion de segundo).
func (e Event) Time() (time.Time, error) {
	return time.Parse(time.RFC3339Nano, e.Timestamp)
}

// Estados con los que Go entrega un lote a Scala.
const (
	// StatusComplete: se vio MATCH_FINISHED; la partida terminó con normalidad.
	StatusComplete = "COMPLETE"
	// StatusTimeout: la partida dejó de recibir eventos sin llegar a MATCH_FINISHED.
	StatusTimeout = "TIMEOUT"
	// StatusShutdown: el servicio se apagó con la partida todavía en curso.
	StatusShutdown = "SHUTDOWN"
)

// Batch es el lote de una partida que Go entrega a Scala: eventos sin
// duplicados y ordenados por timestamp.
type Batch struct {
	MatchID string  `json:"matchId"`
	GameID  string  `json:"gameId"`
	Status  string  `json:"status"`
	Events  []Event `json:"events"`
}
