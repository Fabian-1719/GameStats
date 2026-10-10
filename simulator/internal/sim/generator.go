package sim

import (
	"fmt"
	"math/rand"
	"time"
)

type Generator struct {
	rnd       *rand.Rand
	eventSeq  int
	timeClock time.Time
}

func NewGenerator(seed int64) *Generator {
	if seed == 0 {
		seed = time.Now().UnixNano()
	}
	return &Generator{
		rnd:       rand.New(rand.NewSource(seed)),
		timeClock: time.Date(2026, 10, 10, 15, 0, 0, 0, time.UTC),
	}
}

func (g *Generator) nextEventID() string {
	g.eventSeq++
	return fmt.Sprintf("e-%06d", g.eventSeq)
}

func (g *Generator) formatTime(t time.Time) string {
	return t.Format(time.RFC3339Nano)
}

// GenerateCombatMatch genera los eventos para una partida de combate.
// Incluye opciones para inyectar rachas de bajas o desconexiones sospechosas.
func (g *Generator) GenerateCombatMatch(matchID string, hasStreak bool, hasRageQuit bool) []Event {
	events := make([]Event, 0, 40)
	t := g.timeClock.Add(time.Duration(g.rnd.Intn(600)) * time.Second)

	// MATCH_STARTED
	events = append(events, Event{
		EventID:     g.nextEventID(),
		Timestamp:   g.formatTime(t),
		GameID:      "combat-arena",
		GameVersion: "1.0",
		MatchID:     matchID,
		Type:        "MATCH_STARTED",
	})

	players := []string{"p-01", "p-02", "p-03", "p-04"}
	for _, p := range players {
		t = t.Add(time.Duration(100+g.rnd.Intn(200)) * time.Millisecond)
		events = append(events, Event{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			PlayerID:    p,
			Type:        "PLAYER_JOINED",
		})
	}

	weapons := []string{"RIFLE", "SHOTGUN", "SNIPER", "PISTOL"}

	// Ronda inicial de disparos
	for i := 0; i < 8; i++ {
		t = t.Add(time.Duration(500+g.rnd.Intn(1500)) * time.Millisecond)
		shooter := players[g.rnd.Intn(len(players))]
		target := players[g.rnd.Intn(len(players))]
		for target == shooter {
			target = players[g.rnd.Intn(len(players))]
		}
		wep := weapons[g.rnd.Intn(len(weapons))]

		// SHOT_FIRED
		events = append(events, Event{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			PlayerID:    shooter,
			Type:        "SHOT_FIRED",
			Action:      wep,
			Data:        map[string]any{"weapon": wep},
		})

		// 70% acierto
		if g.rnd.Float32() < 0.70 {
			t = t.Add(100 * time.Millisecond)
			dmg := 20 + g.rnd.Intn(35)
			headshot := g.rnd.Float32() < 0.25
			events = append(events, Event{
				EventID:     g.nextEventID(),
				Timestamp:   g.formatTime(t),
				GameID:      "combat-arena",
				GameVersion: "1.0",
				MatchID:     matchID,
				PlayerID:    shooter,
				Type:        "SHOT_HIT",
				Action:      wep,
				Data: map[string]any{
					"targetId": target,
					"damage":   dmg,
					"headshot": headshot,
				},
			})
		}
	}

	// Patrón 1: Racha de eliminaciones (< 10 segundos para 3 bajas sin morir)
	if hasStreak {
		streakShooter := "p-01"
		targets := []string{"p-02", "p-03", "p-04"}
		for idx, tgt := range targets {
			t = t.Add(2 * time.Second) // 2s entre cada baja = 6s total (< 10s)
			events = append(events, Event{
				EventID:     g.nextEventID(),
				Timestamp:   g.formatTime(t),
				GameID:      "combat-arena",
				GameVersion: "1.0",
				MatchID:     matchID,
				PlayerID:    streakShooter,
				Type:        "SHOT_FIRED",
				Action:      "SNIPER",
				Data:        map[string]any{"weapon": "SNIPER"},
			})
			t = t.Add(50 * time.Millisecond)
			events = append(events, Event{
				EventID:     g.nextEventID(),
				Timestamp:   g.formatTime(t),
				GameID:      "combat-arena",
				GameVersion: "1.0",
				MatchID:     matchID,
				PlayerID:    streakShooter,
				Type:        "SHOT_HIT",
				Action:      "SNIPER",
				Data: map[string]any{
					"targetId": tgt,
					"damage":   100,
					"headshot": idx == 0,
				},
			})
			t = t.Add(50 * time.Millisecond)
			events = append(events, Event{
				EventID:     g.nextEventID(),
				Timestamp:   g.formatTime(t),
				GameID:      "combat-arena",
				GameVersion: "1.0",
				MatchID:     matchID,
				PlayerID:    streakShooter,
				Type:        "PLAYER_ELIMINATED",
				Data: map[string]any{
					"targetId": tgt,
					"weapon":   "SNIPER",
					"distance": 45,
				},
			})
		}
	} else {
		// Eliminación normal
		t = t.Add(3 * time.Second)
		events = append(events, Event{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			PlayerID:    "p-02",
			Type:        "PLAYER_ELIMINATED",
			Data: map[string]any{
				"targetId": "p-03",
				"weapon":   "RIFLE",
				"distance": 22,
			},
		})
	}

	// Patrón 2: Abandono sospechoso (PLAYER_DISCONNECTED sin reconexión en 60s mientras iba perdiendo)
	if hasRageQuit {
		rageQuitter := "p-04"
		t = t.Add(2 * time.Second)
		events = append(events, Event{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			PlayerID:    rageQuitter,
			Type:        "PLAYER_DISCONNECTED",
		})
		// Pasan más de 60 segundos sin reconexión antes de terminar
		t = t.Add(75 * time.Second)
	} else {
		t = t.Add(15 * time.Second)
	}

	// MATCH_FINISHED
	events = append(events, Event{
		EventID:     g.nextEventID(),
		Timestamp:   g.formatTime(t),
		GameID:      "combat-arena",
		GameVersion: "1.0",
		MatchID:     matchID,
		Type:        "MATCH_FINISHED",
		Data: map[string]any{
			"winnerId": "p-01",
			"scores": map[string]any{
				"p-01": 1500,
				"p-02": 850,
				"p-03": 400,
				"p-04": 200,
			},
		},
	})

	return events
}

// GenerateRacingMatch genera eventos de una carrera en circuito.
// Soporta la inyección del patrón Remontada (comienza o va en baja posición a mitad y termina ganando).
func (g *Generator) GenerateRacingMatch(matchID string, hasComeback bool) []Event {
	events := make([]Event, 0, 30)
	t := g.timeClock.Add(time.Duration(g.rnd.Intn(600)) * time.Second)

	tracks := []string{"Monza", "Silverstone", "Spa", "Suzuka"}
	track := tracks[g.rnd.Intn(len(tracks))]
	totalLaps := 3

	// MATCH_STARTED
	events = append(events, Event{
		EventID:     g.nextEventID(),
		Timestamp:   g.formatTime(t),
		GameID:      "racing-circuit",
		GameVersion: "1.0",
		MatchID:     matchID,
		Type:        "MATCH_STARTED",
		Data: map[string]any{
			"track": track,
			"laps":  totalLaps,
		},
	})

	racers := []string{"p-10", "p-11", "p-12", "p-13"}
	for _, p := range racers {
		t = t.Add(time.Duration(100+g.rnd.Intn(200)) * time.Millisecond)
		events = append(events, Event{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "racing-circuit",
			GameVersion: "1.0",
			MatchID:     matchID,
			PlayerID:    p,
			Type:        "PLAYER_JOINED",
		})
	}

	winner := "p-10"

	for lap := 1; lap <= totalLaps; lap++ {
		t = t.Add(60 * time.Second)

		// Vuelta completada
		for _, r := range racers {
			lapTime := 75000 + g.rnd.Intn(5000)
			pos := 2
			if lap == 1 {
				if r == winner && hasComeback {
					pos = 4 // Empieza de último
				} else if r == "p-11" {
					pos = 1
				} else if r == "p-12" {
					pos = 2
				} else {
					pos = 3
				}
			} else if lap == 2 {
				// Mitad de carrera
				if r == winner && hasComeback {
					pos = 3 // A mitad sigue abajo
				} else if r == "p-11" {
					pos = 1
				} else if r == "p-12" {
					pos = 2
				} else {
					pos = 4
				}
			} else {
				// Última vuelta
				if r == winner {
					pos = 1 // Termina ganando
					lapTime = 72100 // Mejor vuelta para ganar
				} else if r == "p-11" {
					pos = 2
				} else if r == "p-12" {
					pos = 3
				} else {
					pos = 4
				}
			}

			events = append(events, Event{
				EventID:     g.nextEventID(),
				Timestamp:   g.formatTime(t.Add(time.Duration(pos*200) * time.Millisecond)),
				GameID:      "racing-circuit",
				GameVersion: "1.0",
				MatchID:     matchID,
				PlayerID:    r,
				Type:        "LAP_COMPLETED",
				Data: map[string]any{
					"lap":       lap,
					"lapTimeMs": lapTime,
					"position":  pos,
				},
			})
		}

		// Cambios de posición y posibles penalizaciones
		if lap == 2 {
			t = t.Add(5 * time.Second)
			if hasComeback {
				// Remontada en progreso: winner adelanta a p-13
				events = append(events, Event{
					EventID:     g.nextEventID(),
					Timestamp:   g.formatTime(t),
					GameID:      "racing-circuit",
					GameVersion: "1.0",
					MatchID:     matchID,
					PlayerID:    winner,
					Type:        "POSITION_CHANGED",
					Data: map[string]any{
						"from": 4,
						"to":   3,
					},
				})
			}
			// Penalización a un competidor
			t = t.Add(3 * time.Second)
			events = append(events, Event{
				EventID:     g.nextEventID(),
				Timestamp:   g.formatTime(t),
				GameID:      "racing-circuit",
				GameVersion: "1.0",
				MatchID:     matchID,
				PlayerID:    "p-13",
				Type:        "PENALTY",
				Data: map[string]any{
					"reason":  "CORTE_DE_CURVA",
					"seconds": 5,
				},
			})
		}
	}

	// MATCH_FINISHED
	t = t.Add(10 * time.Second)
	events = append(events, Event{
		EventID:     g.nextEventID(),
		Timestamp:   g.formatTime(t),
		GameID:      "racing-circuit",
		GameVersion: "1.0",
		MatchID:     matchID,
		Type:        "MATCH_FINISHED",
		Data: map[string]any{
			"classification": []string{winner, "p-11", "p-12", "p-13"},
		},
	})

	return events
}

// GenerateInvalidEvents genera un catálogo de eventos con diversos errores de validación.
func (g *Generator) GenerateInvalidEvents(matchID string) []Event {
	t := g.timeClock.Add(time.Minute)
	return []Event{
		// 1. BAD_JSON (línea no JSON)
		{
			RawOverride: `esto no es un objeto json valido {{{`,
		},
		// 2. MISSING_FIELD (falta matchId)
		{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			Type:        "MATCH_STARTED",
		},
		// 3. BAD_TIMESTAMP
		{
			EventID:     g.nextEventID(),
			Timestamp:   "2026-99-99 88:88:88",
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			Type:        "MATCH_STARTED",
		},
		// 4. UNKNOWN_GAME
		{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "super-mario-galaxy",
			GameVersion: "1.0",
			MatchID:     matchID,
			Type:        "MATCH_STARTED",
		},
		// 5. UNKNOWN_TYPE
		{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			Type:        "CAST_SPELL_TELEPORT",
		},
		// 6. MISSING_PLAYER (requerido para LAP_COMPLETED)
		{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "racing-circuit",
			GameVersion: "1.0",
			MatchID:     matchID,
			Type:        "LAP_COMPLETED",
			Data: map[string]any{
				"lap":       1,
				"lapTimeMs": 85000,
				"position":  1,
			},
		},
		// 7. MISSING_DATA_FIELD (SHOT_HIT requiere damage y targetId)
		{
			EventID:     g.nextEventID(),
			Timestamp:   g.formatTime(t),
			GameID:      "combat-arena",
			GameVersion: "1.0",
			MatchID:     matchID,
			PlayerID:    "p-01",
			Type:        "SHOT_HIT",
			Data: map[string]any{
				"targetId": "p-02",
				// damage omitido intencionalmente
			},
		},
	}
}

// PerturbEvents desordena una porción de los eventos y duplica un porcentaje de ellos.
func (g *Generator) PerturbEvents(events []Event, dupsPct int, shuffle bool) []Event {
	out := make([]Event, len(events))
	copy(out, events)

	// Agregar duplicados
	if dupsPct > 0 {
		numDups := (len(events) * dupsPct) / 100
		for i := 0; i < numDups; i++ {
			idx := g.rnd.Intn(len(events))
			// Duplicamos exactamente el evento con el mismo eventId
			dup := events[idx]
			out = append(out, dup)
		}
	}

	// Desordenar (Shuffle) si se solicita
	if shuffle && len(out) > 1 {
		// Fisher-Yates shuffle
		g.rnd.Shuffle(len(out), func(i, j int) {
			out[i], out[j] = out[j], out[i]
		})
	}

	return out
}
