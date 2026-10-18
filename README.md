# GameStats

## Qué es GameStats

GameStats recibe eventos de partidas de videojuegos.
Go valida los eventos y los agrupa por partida.
Scala calcula estadísticas y patrones; el simulador envía partidas de ejemplo.

```text
simulator --> go-ingestion --> scala-analytics
                  :8080             :8081
```

## Qué analiza

GameStats incluye dos juegos:

- **`combat-arena`:** calcula por jugador bajas, muertes, K/D, daño medio por impacto, porcentaje de disparos a la cabeza, disparos, impactos y precisión. Por partida calcula duración, daño total, ranking, MVP y ganador.
- **`racing-circuit`:** calcula por jugador mejor vuelta, promedio y desviación de tiempos, posiciones ganadas, penalizaciones y segundos de penalización. Por partida calcula clasificación final, vuelta más rápida y adelantamientos.

También detecta estos patrones:

- **`KILL_STREAK`:** al menos 3 bajas en una ventana de hasta 10 segundos, sin morir entre ellas.
- **`SUSPICIOUS_ABANDON`:** un jugador lleva al menos 60 segundos desconectado, no se reconecta durante esos 60 segundos y tenía menos bajas que el líder al desconectarse.
- **`COMEBACK`:** solo en partidas completas. El ganador estaba en la mitad trasera de los corredores en la vuelta `laps / 2`, con división entera; en una carrera de 3 vueltas, se evalúa la primera.

## Requisitos

Para ejecutar el proyecto necesitas Docker con Docker Compose v2, unos **3 GB de espacio libre** e **Internet para la primera build**. Comprueba Compose con:

```text
docker compose version
```

Debe aparecer una versión `v2.x`. Docker Desktop no es obligatorio: en Linux basta Docker Engine con el plugin Compose. [Instalar Docker](https://docs.docker.com/get-started/get-docker/).

## Cómo correrlo

1. Clona el repositorio y entra en su carpeta:

   ```text
   git clone https://github.com/Fabian-1719/GameStats.git
   cd GameStats
   ```

   Si no tienes Git, descarga el ZIP desde [GitHub](https://github.com/Fabian-1719/GameStats) y abre la carpeta `GameStats`.

2. Construye las imágenes y arranca los servicios:

   ```text
   docker compose up --build -d
   ```

3. Ejecuta la demo. En Windows PowerShell:

   ```powershell
   .\scripts\demo.ps1
   ```

   En Linux o Mac:

   ```sh
   ./scripts/demo.sh
   ```

   Si Linux o Mac indica que falta permiso, ejecuta `chmod +x scripts/demo.sh` y vuelve a correr el comando.

## Tiempo de espera

La primera build tarda entre **5 y 15 minutos** (unos **14 minutos en nuestra prueba en frío**). Depende de Internet y del procesador. Es normal: descarga las imágenes y compila Scala; no se ha colgado. Las siguientes builds tardan unos 30 segundos.

## Qué debes ver

La demo muestra siete consultas. Go indica **97 eventos aceptados y 0 rechazados**. Para `combat-arena` hay 2 partidas, 2 patrones `KILL_STREAK` y 1 `SUSPICIOUS_ABANDON`; para `racing-circuit` hay 2 patrones `COMEBACK`. El simulador usa la semilla fija `42`, así que el resultado se repite en cada ejecución limpia.

## Cómo apagarlo

Desde la carpeta del proyecto:

```text
docker compose down -v
```

Esto detiene y elimina los contenedores y los volúmenes de datos.

## Endpoints

| Servicio | Método y ruta | Propósito |
|---|---|---|
| Go, puerto 8080 | `GET /health` | Comprueba el estado de ingestión |
| Go, puerto 8080 | `POST /v1/events` | Recibe uno o varios eventos |
| Scala, puerto 8081 | `GET /health` | Comprueba el estado de analítica |
| Scala, puerto 8081 | `GET /v1/games/{gameId}/leaderboard` | Devuelve la clasificación de un juego |
| Scala, puerto 8081 | `GET /v1/patterns?gameId={gameId}` | Devuelve los patrones detectados |
| Scala, puerto 8081 | `GET /v1/stats/players/{id}` | Devuelve las estadísticas de un jugador |
| Scala, puerto 8081 | `GET /v1/matches/{id}?gameId={gameId}` | Devuelve el análisis de una partida |

Go envía cada lote a `POST /v1/analyze` en Scala. Esta ruta es interna a la red de Compose.

## Solución de problemas

- **Docker apagado:** inicia Docker y vuelve a ejecutar `docker compose up --build -d`.
- **Puerto 8080 o 8081 ocupado:** cierra la aplicación que usa el puerto y vuelve a iniciar Compose.
- **PowerShell bloquea el script:** ejecuta `powershell -ExecutionPolicy Bypass -File .\scripts\demo.ps1`.
- **Primera build lenta:** entre 5 y 15 minutos es normal; depende de Internet y del procesador. No se ha colgado.

## Decisiones de diseño

- Go y Scala se comunican por HTTP/REST y JSON.
- Go se eligió para la ingestión por sus goroutines y canales, que permiten procesar partidas concurrentemente y mantener su estado separado. El simulador también está escrito en Go: genera y envía los eventos de ejemplo.
- Scala se eligió para el análisis por su enfoque funcional: operaciones como `map`, `filter` y `groupBy` ayudan a resumir cada lote y detectar patrones sin modificar los eventos recibidos.
- Scala detecta rachas (`KILL_STREAK`), abandonos sospechosos (`SUSPICIOUS_ABANDON`) y remontadas (`COMEBACK`).
- El análisis de Scala vive en memoria y se pierde al recrear su contenedor.

## Pruebas

Para correrlas localmente necesitas Go 1.22 o superior, Java 21 y sbt. En Windows, `go test -race` requiere CGO y un compilador C. Esto no hace falta para ejecutar GameStats con Docker.

Desde la raíz del repositorio, ejecuta:

```sh
cd go-ingestion
go test -race ./...
cd ../simulator
go test -race ./...
cd ../scala-analytics
sbt test
```

## Estructura del repositorio

- `go-ingestion/`: recibe, valida y agrupa eventos.
- `scala-analytics/`: ofrece la API de análisis, estadísticas y patrones.
- `simulator/`: genera y envía eventos de ejemplo.
- `scripts/`: demos para PowerShell y Bash.
- `docs/`: especificación de eventos y documentación de los módulos.
- `deploy/`: carpeta reservada para recursos de despliegue.
- `.github/workflows/`: configuración de CI/CD.

## CI/CD

El workflow de GitHub Actions tiene dos jobs:

- `test-and-build` corre en cada pull request y en cada push a `main`. Prueba Go y el simulador con el detector de carreras, ejecuta `sbt test` y construye las imágenes de Compose.
- `publish-ghcr` corre solo después de que el primer job pase y haya un push a `main`. Publica las imágenes de los tres servicios en GHCR, para `linux/amd64`, con las etiquetas `latest` y el SHA del commit.

## Deployment

El entorno de despliegue definido por el equipo es Docker Compose. Desde la raíz, se reproduce con `docker compose up --build -d`; Compose construye los servicios desde `go-ingestion/`, `scala-analytics/` y `simulator/`, y publica las API en los puertos 8080 y 8081. Los scripts de demo están en `scripts/`.

Contrato de eventos: [docs/event-spec.md](docs/event-spec.md).
