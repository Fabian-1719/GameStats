# GameStats

Plataforma multilenguaje para procesamiento y analisis de eventos de videojuegos
(Curso de Lenguajes de Programacion). **Go** ingiere y organiza los eventos;
**Scala** los analiza de forma funcional.

```
Simulador -> [Go: ingestion] -> lote por partida -> [Scala: analisis] -> resultados JSON
```

| Carpeta | Contenido | Estado |
|---|---|---|
| `go-ingestion/` | Validacion, entrada de eventos, DLQ | Dia 1 listo |
| `scala-analytics/` | Estadisticas y patrones | Pendiente (Cristofer) |
| `simulator/` | Generador de eventos | Pendiente (Dia 3) |
| `docs/` | Especificacion y documentacion | `event-spec.md` listo |

Especificacion de eventos y contrato entre modulos: [docs/event-spec.md](docs/event-spec.md)

## Ejecutar el modulo Go

Requiere Go 1.22 o superior.

```bash
cd go-ingestion
go test -race ./...              # pruebas
go run ./cmd/ingestion           # servidor en :8080
go run ./cmd/ingestion -file ../docs/sample-data/smoke.ndjson   # carga un archivo
```

Probar el servidor:

```bash
curl -s localhost:8080/health
curl -s -X POST localhost:8080/v1/events -d @evento.json
```

## Convenciones

- Ramas: `feature/<nombre>-<tarea>`; `main` protegida; pull request revisado por el otro.
- Commits pequenos con prefijo: `feat:`, `fix:`, `test:`, `docs:`, `ci:`.
