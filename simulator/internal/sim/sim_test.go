package sim_test

import (
	"testing"

	"gamestats/simulator/internal/sim"
)

func TestGeneratorCombatAndRacing(t *testing.T) {
	gen := sim.NewGenerator(12345)

	combatEvents := gen.GenerateCombatMatch("m-test-combat", true, true)
	if len(combatEvents) == 0 {
		t.Fatal("no se generaron eventos de combate")
	}

	racingEvents := gen.GenerateRacingMatch("m-test-racing", true)
	if len(racingEvents) == 0 {
		t.Fatal("no se generaron eventos de carreras")
	}

	invalidEvents := gen.GenerateInvalidEvents("m-test-invalid")
	if len(invalidEvents) != 7 {
		t.Fatalf("se esperaban 7 eventos inválidos, se generaron %d", len(invalidEvents))
	}

	perturbed := gen.PerturbEvents(combatEvents, 20, true)
	if len(perturbed) <= len(combatEvents) {
		t.Fatalf("se esperaban duplicados agregados: original=%d perturbado=%d",
			len(combatEvents), len(perturbed))
	}
}
