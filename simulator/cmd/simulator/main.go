// Simulador de eventos de videojuegos para GameStats.
//
// Permite generar eventos normales, eventos con patrones para Scala (racha,
// remontada, abandono sospechoso) y eventos inválidos para probar la DLQ de Go.
//
// Uso:
//   simulator --matches 20 --out docs/sample-data/normal.ndjson
//   simulator --matches 10 --target http://localhost:8080/v1/events --rate 100
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"gamestats/simulator/internal/sim"
)

func main() {
	matchesFlag := flag.Int("matches", 10, "número de partidas a generar")
	rateFlag := flag.Int("rate", 0, "tasa máxima de eventos/segundo al enviar por HTTP (0 = sin límite)")
	seedFlag := flag.Int64("seed", 42, "semilla aleatoria para reproducibilidad (0 = aleatorio por reloj)")
	targetFlag := flag.String("target", "", "URL destino de Go (ejemplo: http://localhost:8080/v1/events)")
	outFlag := flag.String("out", "", "ruta de archivo de salida NDJSON (si se omite y no hay target, sale por stdout)")
	modeFlag := flag.String("mode", "all", "modo de generación: 'all', 'normal', 'patterns', 'invalid'")
	dupsFlag := flag.Int("dups", 10, "porcentaje de eventos duplicados (por defecto 10%)")
	shuffleFlag := flag.Bool("shuffle", true, "desordenar los eventos dentro de cada partida")
	flag.Parse()

	gen := sim.NewGenerator(*seedFlag)

	var allEvents []sim.Event

	switch *modeFlag {
	case "invalid":
		// Solo eventos inválidos de prueba
		for i := 1; i <= *matchesFlag; i++ {
			matchID := fmt.Sprintf("m-err-%04d", i)
			allEvents = append(allEvents, gen.GenerateInvalidEvents(matchID)...)
		}

	case "patterns":
		// Partidas con patrones específicos (racha, abandono sospechoso, remontada)
		for i := 1; i <= *matchesFlag; i++ {
			cMatchID := fmt.Sprintf("m-pat-c-%04d", i)
			rMatchID := fmt.Sprintf("m-pat-r-%04d", i)

			// Partida con racha y con abandono sospechoso
			cEvents := gen.GenerateCombatMatch(cMatchID, true, i%2 == 0)
			cEvents = gen.PerturbEvents(cEvents, *dupsFlag, *shuffleFlag)
			allEvents = append(allEvents, cEvents...)

			// Partida de carreras con remontada
			rEvents := gen.GenerateRacingMatch(rMatchID, true)
			rEvents = gen.PerturbEvents(rEvents, *dupsFlag, *shuffleFlag)
			allEvents = append(allEvents, rEvents...)
		}

	case "normal":
		// Partidas normales sin fallos ni abandonos
		for i := 1; i <= *matchesFlag; i++ {
			cMatchID := fmt.Sprintf("m-norm-c-%04d", i)
			rMatchID := fmt.Sprintf("m-norm-r-%04d", i)

			cEvents := gen.GenerateCombatMatch(cMatchID, false, false)
			cEvents = gen.PerturbEvents(cEvents, 0, false)
			allEvents = append(allEvents, cEvents...)

			rEvents := gen.GenerateRacingMatch(rMatchID, false)
			rEvents = gen.PerturbEvents(rEvents, 0, false)
			allEvents = append(allEvents, rEvents...)
		}

	default: // "all"
		// Mezcla de normales, patrones, con duplicados y algunos inválidos
		for i := 1; i <= *matchesFlag; i++ {
			hasStreak := i%2 == 1
			hasRageQuit := i%3 == 0
			hasComeback := i%2 == 0

			cMatchID := fmt.Sprintf("m-c-%04d", i)
			rMatchID := fmt.Sprintf("m-r-%04d", i)

			cEvents := gen.GenerateCombatMatch(cMatchID, hasStreak, hasRageQuit)
			cEvents = gen.PerturbEvents(cEvents, *dupsFlag, *shuffleFlag)
			allEvents = append(allEvents, cEvents...)

			rEvents := gen.GenerateRacingMatch(rMatchID, hasComeback)
			rEvents = gen.PerturbEvents(rEvents, *dupsFlag, *shuffleFlag)
			allEvents = append(allEvents, rEvents...)

			// Inyectar un pequeño grupo de eventos inválidos en algunas partidas
			if i == 1 || i%5 == 0 {
				errMatchID := fmt.Sprintf("m-err-%04d", i)
				allEvents = append(allEvents, gen.GenerateInvalidEvents(errMatchID)...)
			}
		}
	}

	log.Printf("simulador: generados %d eventos en total (modo: %s, matches: %d)",
		len(allEvents), *modeFlag, *matchesFlag)

	// Destino 1: Archivo NDJSON
	if *outFlag != "" {
		if err := writeToFile(*outFlag, allEvents); err != nil {
			log.Fatalf("error escribiendo a archivo %s: %v", *outFlag, err)
		}
		log.Printf("simulador: datos guardados exitosamente en %s", *outFlag)
		return
	}

	// Destino 2: Envío por HTTP a Go
	if *targetFlag != "" {
		log.Printf("simulador: enviando eventos a %s...", *targetFlag)
		sendHTTP(*targetFlag, allEvents, *rateFlag)
		return
	}

	// Destino 3: Salida estándar
	for _, ev := range allEvents {
		line, _ := ev.ToNDJSON()
		fmt.Println(string(line))
	}
}

func writeToFile(path string, events []sim.Event) error {
	if dir := filepath.Dir(path); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()

	for _, ev := range events {
		line, err := ev.ToNDJSON()
		if err != nil {
			continue
		}
		if _, err := f.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func sendHTTP(target string, events []sim.Event, rate int) {
	client := &http.Client{Timeout: 10 * time.Second}
	var delay time.Duration
	if rate > 0 {
		delay = time.Second / time.Duration(rate)
	}

	sent := 0
	start := time.Now()

	for _, ev := range events {
		body, err := ev.ToNDJSON()
		if err != nil {
			continue
		}

		req, err := http.NewRequest(http.MethodPost, target, bytes.NewReader(body))
		if err != nil {
			log.Printf("error creando request: %v", err)
			continue
		}
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("error enviando evento: %v", err)
		} else {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			sent++
		}

		if delay > 0 {
			time.Sleep(delay)
		}
	}

	elapsed := time.Since(start)
	log.Printf("simulador: finalizado envío HTTP. Enviados: %d/%d eventos en %v",
		sent, len(events), elapsed)
}
