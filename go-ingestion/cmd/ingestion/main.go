// Comando ingestion: recibe eventos por HTTP o carga un archivo NDJSON.
//
// Configuración por variables de entorno:
//
//	PORT           puerto HTTP (por defecto 8080)
//	CATALOG_PATH   catálogo de eventos (por defecto config/catalog.json)
//	REJECTED_PATH  archivo de rechazados (por defecto rejected.ndjson)
//
// Uso:
//
//	ingestion                   # levanta el servidor HTTP
//	ingestion -file datos.ndjson   # carga un archivo y termina
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"gamestats/go-ingestion/internal/dlq"
	"gamestats/go-ingestion/internal/ingest"
	"gamestats/go-ingestion/internal/model"
	"gamestats/go-ingestion/internal/validator"
)

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	file := flag.String("file", "", "archivo NDJSON a cargar (si se omite, levanta el servidor HTTP)")
	flag.Parse()

	catalog, err := validator.LoadCatalog(env("CATALOG_PATH", "config/catalog.json"))
	if err != nil {
		log.Fatal(err)
	}
	rejected, err := dlq.Open(env("REJECTED_PATH", "rejected.ndjson"))
	if err != nil {
		log.Fatalf("abriendo DLQ: %v", err)
	}
	defer rejected.Close()

	// Día 1: los eventos válidos solo se cuentan. En el Día 2 este sink pasa a
	// enviar cada evento al router, que lo reparte a la goroutine de su partida.
	sink := func(model.Event) {}

	proc := ingest.NewProcessor(validator.New(catalog), rejected, sink)

	if *file != "" {
		f, err := os.Open(*file)
		if err != nil {
			log.Fatal(err)
		}
		defer f.Close()
		st, err := proc.LoadNDJSON(f, *file)
		if err != nil {
			log.Fatalf("leyendo archivo: %v", err)
		}
		fmt.Printf("total=%d aceptados=%d rechazados=%d\n", st.Total, st.Accepted, st.Rejected)
		return
	}

	srv := &http.Server{
		Addr:              ":" + env("PORT", "8080"),
		Handler:           ingest.NewMux(proc),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		log.Printf("ingestión escuchando en %s", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal(err)
		}
	}()

	<-ctx.Done()
	log.Println("apagando...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("error al apagar: %v", err)
	}
	st := proc.Stats()
	log.Printf("final: total=%d aceptados=%d rechazados=%d", st.Total, st.Accepted, st.Rejected)
}
