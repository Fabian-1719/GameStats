# GameStats

GameStats recibe eventos de partidas y los valida con un catalogo por juego.
Go los agrupa por partida y los entrega a Scala para calcular estadisticas y patrones.
Un simulador genera partidas de combate y carreras para que la demo funcione sin datos manuales.

```text
[simulator] -- POST /v1/events --> [go-ingestion :8080]
                                         |
                                         +-- POST /v1/analyze --> [scala-analytics :8081]
                                                                      |
                         leaderboard / patterns / player stats / matches <--+
```

## Requisitos

Solo Docker Desktop iniciado; incluye Docker Compose. No hace falta instalar Go, Java ni Scala.

## Inicio rapido

Desde la raiz, levanta los tres servicios; Compose espera los healthchecks de Scala y Go y el simulador envia datos automaticamente:

```powershell
docker compose up --build -d
```

Ejecuta la demo en PowerShell:

```powershell
.\scripts\demo.ps1
```

En Linux o macOS, ejecuta `bash scripts/demo.sh`. El script espera la API y la partida de muestra antes de imprimir siete consultas. Go queda en `http://localhost:8080` y Scala en `http://localhost:8081`.

Para detener los servicios: `docker compose down`.

## API

Los endpoints de analitica estan en Scala; Go recibe eventos y ofrece health.

| Metodo y ruta | Ejemplo | Uso |
|---|---|---|
| `GET /health` (Go) | `curl http://localhost:8080/health` | Estado de ingestion |
| `GET /health` (Scala) | `curl http://localhost:8081/health` | Estado de analitica |
| `POST /v1/events` (Go) | `curl -X POST localhost:8080/v1/events -H 'Content-Type: application/json' -d '{"eventId":"e-1","timestamp":"2026-10-18T12:00:00Z","gameId":"combat-arena","gameVersion":"1.0","matchId":"m-1","type":"MATCH_STARTED"}'` | Recibe uno o varios eventos |
| `GET /v1/games/{gameId}/leaderboard` | `curl http://localhost:8081/v1/games/combat-arena/leaderboard` | Clasificacion por juego |
| `GET /v1/patterns?gameId={gameId}` | `curl 'http://localhost:8081/v1/patterns?gameId=racing-circuit'` | Patrones detectados |
| `GET /v1/stats/players/{id}` | `curl http://localhost:8081/v1/stats/players/p-01` | Estadisticas agregadas del jugador |
| `GET /v1/matches/{id}?gameId={gameId}` | `curl 'http://localhost:8081/v1/matches/m-pat-c-0001?gameId=combat-arena'` | Analisis de una partida |
| `POST /v1/analyze` (interno) | Go lo invoca en `http://scala-analytics:8081/v1/analyze` | Recibe un lote por partida |

Juegos de ejemplo: `combat-arena` y `racing-circuit`. La demo ejecuta health, ambos leaderboards, ambos filtros de patrones, stats de `p-01` y el analisis de `m-pat-c-0001`.

## Tests

Localmente, con Go 1.22+ y Java 21/sbt instalados:

```powershell
cd go-ingestion; go test ./...; cd ..\simulator; go test ./...; cd ..\scala-analytics; sbt test
```

Solo con Docker Desktop (PowerShell, desde la raiz):

```powershell
docker run --rm --mount "type=bind,source=$($PWD.Path)\go-ingestion,target=/src" -w /src golang:1.24-alpine go test ./...
docker run --rm --mount "type=bind,source=$($PWD.Path)\simulator,target=/src" -w /src golang:1.24-alpine go test ./...
docker run --rm --mount "type=bind,source=$($PWD.Path)\scala-analytics,target=/workspace" -w /workspace sbtscala/scala-sbt:eclipse-temurin-21.0.12_8_1.13.0_2.13.18 sbt test
```

## Estructura

- `go-ingestion/`: API de entrada, validacion, concurrencia y lotes por partida.
- `scala-analytics/`: API HTTP, estadisticas y detectores de patrones.
- `simulator/`: generador y emisor de eventos.
- `docs/`: contratos, especificaciones y guias por modulo.
- `deploy/`: recursos de despliegue.
- `.github/workflows/ci.yml`: pruebas, build y publicacion de imagenes.
- `scripts/`: demos para PowerShell y Bash.

Contrato de eventos: [docs/event-spec.md](docs/event-spec.md).

## Decisiones de diseno

- Go y Scala se comunican por HTTP/REST con JSON; Go envia a Scala un lote ordenado por partida.
- Go procesa partidas concurrentemente y conserva la separacion por `gameId` y `matchId`.
- Scala detecta rachas, abandonos sospechosos y remontadas a partir de eventos.
- El estado analitico vive en memoria: al recrear Scala se pierde; los volumenes de Go conservan datos de ingestion y lotes fallidos.

## CI/CD

El workflow corre en push a `main` y en pull requests. `test-and-build` ejecuta los tests Go con race detector, `sbt test` y `docker compose build`. Tras su exito, `publish-ghcr` publica las tres imagenes para `linux/amd64` en GHCR, con etiquetas `latest` y el SHA del commit, solo en push a `main`.

## Deployment
El entorno de despliegue es Docker Compose; no requiere Kubernetes ni servicios externos. Para reproducirlo desde una copia del repo: `docker compose up --build -d`; verifica con `docker compose ps` y `./scripts/demo.ps1` (o `bash scripts/demo.sh`). Compose conecta los servicios en una red privada y publica los puertos 8080 y 8081 en el host.



## Solucion de problemas
- **Docker apagado:** inicia Docker Desktop y vuelve a ejecutar `docker compose up --build -d`.
- **Puerto 8080 o 8081 ocupado:** detiene el proceso que lo usa o cambia el puerto del host en `docker-compose.yml`; configura `$env:GAMESTATS_GO_URL` o `$env:GAMESTATS_SCALA_URL` con la URL nueva antes de ejecutar el demo.
- **Primera build lenta:** es normal; descarga imagenes, dependencias y compila Scala. Las builds siguientes reutilizan la cache.


Mas detalle del contrato: [docs/event-spec.md](docs/event-spec.md).