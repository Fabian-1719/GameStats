package sim

import (
	"encoding/json"
	"time"
)

// Event representa la estructura canónica de un evento de GameStats.
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

	// RawOverride permite inyectar cadenas inválidas para pruebas de BAD_JSON.
	RawOverride string `json:"-"`
}

// ToNDJSON convierte el evento a una línea NDJSON o devuelve RawOverride si existe.
func (e Event) ToNDJSON() ([]byte, error) {
	if e.RawOverride != "" {
		return []byte(e.RawOverride), nil
	}
	return json.Marshal(e)
}

// ParseTime parsea el timestamp a time.Time.
func (e Event) ParseTime() (time.Time, error) {
	return time.Parse(time.RFC3339Nano, e.Timestamp)
}
