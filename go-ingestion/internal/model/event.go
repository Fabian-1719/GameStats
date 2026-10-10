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
