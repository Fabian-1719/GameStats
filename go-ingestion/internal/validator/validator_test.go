package validator

import (
	"errors"
	"testing"

	"gamestats/go-ingestion/internal/model"
)

func testCatalog() Catalog {
	return Catalog{
		CommonKey: {
			"MATCH_STARTED":       {RequiresPlayer: false},
			"PLAYER_DISCONNECTED": {RequiresPlayer: true},
		},
		"combat-arena": {
			"SHOT_HIT":          {RequiresPlayer: true, Required: []string{"targetId", "damage"}},
			"PLAYER_ELIMINATED": {RequiresPlayer: true, Required: []string{"targetId", "weapon"}},
		},
		"racing-circuit": {
			"LAP_COMPLETED": {RequiresPlayer: true, Required: []string{"lap", "lapTimeMs", "position"}},
		},
	}
}

// base devuelve un evento de combate válido; cada test lo modifica.
func base() model.Event {
	return model.Event{
		EventID:     "e-1",
		Timestamp:   "2026-10-10T15:04:05.120Z",
		GameID:      "combat-arena",
		GameVersion: "1.0",
		MatchID:     "m-1",
		PlayerID:    "p-1",
		Type:        "SHOT_HIT",
		Action:      "RIFLE",
		Data:        map[string]any{"targetId": "p-2", "damage": 27.0},
	}
}

func TestValidate(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(*model.Event)
		wantCode string // "" = debe ser válido
	}{
		{"evento de combate válido", func(e *model.Event) {}, ""},
		{"timestamp sin fracción de segundo", func(e *model.Event) { e.Timestamp = "2026-10-10T15:04:05Z" }, ""},
		{"evento común sin jugador (MATCH_STARTED)", func(e *model.Event) {
			e.Type, e.PlayerID, e.Data = "MATCH_STARTED", "", nil
		}, ""},
		{"evento de carreras válido", func(e *model.Event) {
			e.GameID, e.Type = "racing-circuit", "LAP_COMPLETED"
			e.Data = map[string]any{"lap": 2.0, "lapTimeMs": 81234.0, "position": 3.0}
		}, ""},

		{"falta eventId", func(e *model.Event) { e.EventID = "" }, CodeMissingField},
		{"falta matchId", func(e *model.Event) { e.MatchID = "" }, CodeMissingField},
		{"falta gameVersion", func(e *model.Event) { e.GameVersion = "" }, CodeMissingField},
		{"timestamp mal formado", func(e *model.Event) { e.Timestamp = "ayer por la tarde" }, CodeBadTimestamp},
		{"timestamp con fecha pero sin zona", func(e *model.Event) { e.Timestamp = "2026-10-10 15:04:05" }, CodeBadTimestamp},
		{"juego desconocido", func(e *model.Event) { e.GameID = "tetris" }, CodeUnknownGame},
		{"gameId reservado _common", func(e *model.Event) { e.GameID = "_common" }, CodeUnknownGame},
		{"tipo desconocido para el juego", func(e *model.Event) { e.Type = "LAP_COMPLETED" }, CodeUnknownType},
		{"tipo de otro juego usado en este", func(e *model.Event) { e.GameID = "racing-circuit" }, CodeUnknownType},
		{"falta playerId donde es obligatorio", func(e *model.Event) { e.PlayerID = "" }, CodeMissingPlayer},
		{"falta data.damage", func(e *model.Event) { e.Data = map[string]any{"targetId": "p-2"} }, CodeMissingDataField},
		{"data.targetId es null", func(e *model.Event) { e.Data = map[string]any{"targetId": nil, "damage": 5.0} }, CodeMissingDataField},
		{"data ausente por completo", func(e *model.Event) { e.Data = nil }, CodeMissingDataField},
	}

	v := New(testCatalog())
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := base()
			tc.mutate(&e)
			err := v.Validate(e)

			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("esperaba evento válido, obtuve: %v", err)
				}
				return
			}
			var verr *Error
			if !errors.As(err, &verr) {
				t.Fatalf("esperaba *Error con código %s, obtuve: %v", tc.wantCode, err)
			}
			if verr.Code != tc.wantCode {
				t.Fatalf("código = %s, esperaba %s (motivo: %s)", verr.Code, tc.wantCode, verr.Reason)
			}
			if verr.Reason == "" {
				t.Fatal("el rechazo debe traer un motivo explícito")
			}
		})
	}
}

func TestLoadCatalogReal(t *testing.T) {
	c, err := LoadCatalog("../../config/catalog.json")
	if err != nil {
		t.Fatalf("el catálogo real no carga: %v", err)
	}
	for _, game := range []string{CommonKey, "combat-arena", "racing-circuit", "connect-four", "snake-arena", "memory-match"} {
		if len(c[game]) == 0 {
			t.Errorf("el catálogo no tiene eventos para %q", game)
		}
	}
}
