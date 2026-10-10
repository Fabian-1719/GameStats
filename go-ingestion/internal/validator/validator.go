// Package validator comprueba que un evento cumple el formato común y las
// reglas del catálogo (config/catalog.json). Agregar un juego o un tipo de
// evento nuevo solo requiere editar el catálogo, no este código.
package validator

import (
	"encoding/json"
	"fmt"
	"os"

	"gamestats/go-ingestion/internal/model"
)

// CommonKey es la sección del catálogo con eventos válidos para todos los juegos.
const CommonKey = "_common"

// Códigos de rechazo, útiles para tests y para contar motivos.
const (
	CodeMissingField     = "MISSING_FIELD"
	CodeBadTimestamp     = "BAD_TIMESTAMP"
	CodeUnknownGame      = "UNKNOWN_GAME"
	CodeUnknownType      = "UNKNOWN_TYPE"
	CodeMissingPlayer    = "MISSING_PLAYER"
	CodeMissingDataField = "MISSING_DATA_FIELD"
)

// Rule describe qué exige un tipo de evento.
type Rule struct {
	RequiresPlayer bool     `json:"requiresPlayer"`
	Required       []string `json:"required"`
}

// Catalog: gameId -> tipo de evento -> regla.
type Catalog map[string]map[string]Rule

// Error es un rechazo con código y motivo legible.
type Error struct {
	Code   string
	Reason string
}

func (e *Error) Error() string { return e.Code + ": " + e.Reason }

func reject(code, format string, args ...any) *Error {
	return &Error{Code: code, Reason: fmt.Sprintf(format, args...)}
}

// LoadCatalog lee el catálogo desde un archivo JSON.
func LoadCatalog(path string) (Catalog, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("leyendo catálogo: %w", err)
	}
	var c Catalog
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("catálogo inválido: %w", err)
	}
	return c, nil
}

// Validator valida eventos contra un catálogo. Es seguro para uso concurrente
// porque el catálogo no se modifica después de crearlo.
type Validator struct {
	catalog Catalog
}

func New(c Catalog) *Validator { return &Validator{catalog: c} }

// rule busca primero la regla específica del juego y luego la común.
func (v *Validator) rule(gameID, eventType string) (Rule, bool) {
	if r, ok := v.catalog[gameID][eventType]; ok {
		return r, true
	}
	r, ok := v.catalog[CommonKey][eventType]
	return r, ok
}

// Validate devuelve nil si el evento es válido, o un *Error con el motivo.
func (v *Validator) Validate(e model.Event) error {
	// 1. Campos comunes obligatorios.
	for _, f := range []struct{ name, value string }{
		{"eventId", e.EventID},
		{"timestamp", e.Timestamp},
		{"gameId", e.GameID},
		{"gameVersion", e.GameVersion},
		{"matchId", e.MatchID},
		{"type", e.Type},
	} {
		if f.value == "" {
			return reject(CodeMissingField, "falta el campo obligatorio %q", f.name)
		}
	}

	// 2. Timestamp bien formado.
	if _, err := e.Time(); err != nil {
		return reject(CodeBadTimestamp, "timestamp %q no es RFC 3339", e.Timestamp)
	}

	// 3. Juego conocido.
	if e.GameID == CommonKey {
		return reject(CodeUnknownGame, "gameId %q no es válido", e.GameID)
	}
	if _, ok := v.catalog[e.GameID]; !ok {
		return reject(CodeUnknownGame, "gameId %q no está en el catálogo", e.GameID)
	}

	// 4. Tipo conocido para ese juego.
	rule, ok := v.rule(e.GameID, e.Type)
	if !ok {
		return reject(CodeUnknownType, "tipo %q no existe para el juego %q", e.Type, e.GameID)
	}

	// 5. Jugador, cuando el tipo lo exige.
	if rule.RequiresPlayer && e.PlayerID == "" {
		return reject(CodeMissingPlayer, "el tipo %q exige playerId", e.Type)
	}

	// 6. Campos requeridos dentro de data.
	for _, field := range rule.Required {
		if val, present := e.Data[field]; !present || val == nil {
			return reject(CodeMissingDataField, "el tipo %q exige data.%s", e.Type, field)
		}
	}
	return nil
}
