# Contenedores y CI/CD (Docker)

Documento de arquitectura, ejecución local y validación de las imágenes de **GameStats**.
La solución agrupa los servicios Go y Scala con Docker Compose, y usa el simulador para
producir eventos de prueba.

---

## 1. Responsabilidad y alcance

La contenerización empaqueta cada proceso con sus dependencias y configura su comunicación
sin cambiar la lógica de análisis ni de ingestión.

1. **`scala-analytics`**: compila Scala 3 con sbt en una etapa de build y ejecuta el JAR
   ensamblado sobre Java 21. Expone `8081` y comprueba `/health` mediante un health check.
2. **`go-ingestion`**: usa su Dockerfile multi-stage existente; ejecuta el binario en Alpine
   como usuario no privilegiado y escucha en `8080`.
3. **`simulator`**: compila un binario Go estático y lo ejecuta en Alpine. El comando de
   Compose genera dos partidas de patrones y las envía por HTTP a Go.
4. **Docker Compose**: conecta los tres servicios en una red privada, publica las API en el
   host y conserva los datos de Go en volúmenes con nombre.
5. **GitHub Actions**: ejecuta pruebas y construye las imágenes al recibir cambios.

---

## 2. Arquitectura

```text
[simulator]
     │ POST /v1/events
     ▼
[go-ingestion :8080]
     │ POST /v1/analyze (SCALA_URL=http://scala-analytics:8081)
     ▼
[scala-analytics :8081]
```

### 2.1. Imagen de Scala

`scala-analytics/Dockerfile` separa compilación y ejecución:

- La etapa `build` usa sbt y `sbt-assembly` para producir `scala-analytics.jar`.
- La etapa `runtime` usa Java 21 JRE; no incluye fuentes ni herramientas de compilación.
- Cask escucha en `0.0.0.0:8081`, necesario para recibir conexiones por el puerto publicado.
- El health check consulta `http://127.0.0.1:8081/health`.

### 2.2. Ingestión y simulador

- Go publica el puerto `8080`; Compose configura `SCALA_URL` con el nombre DNS del servicio
  Scala, no con `localhost`.
- El simulador recibe `--target http://go-ingestion:8080/v1/events`. En ejecución individual,
  sin `--target` ni `--out`, sus eventos van a stdout y quedan visibles en los logs.
- En Compose, el simulador es un proceso de una sola ejecución: al enviar las partidas,
  termina. Que quede `Exited` con código cero es normal.

### 2.3. Persistencia y orden de inicio

Compose publica `8080` y `8081` en el host y crea los volúmenes `ingestion-data` y
`failed-batches`. Go espera a que Scala esté saludable; el simulador espera a que Go esté
saludable. Esto evita que el simulador envíe eventos antes de que el receptor esté listo.

---

## 3. Ejecución local

Requiere Docker Desktop iniciado. Desde PowerShell en la raíz del proyecto:

```powershell
Set-Location C:\GameStats
docker compose up --build -d
```

El grupo aparece en Docker Desktop en **Containers** con el nombre `gamestats`. Desde esa
vista se pueden abrir los logs, revisar el estado, iniciar o detener servicios y examinar los
volúmenes. Scala y Go deben quedar `Healthy`; el simulador puede terminar después de enviar
los eventos.

Las API publicadas en el host son:

```text
http://localhost:8080  Go ingestion
http://localhost:8081  Scala analytics
```

El simulador configurado por Compose manda dos partidas de patrones. Para comprobar las
rutas después del arranque:

```powershell
curl.exe http://localhost:8080/health
curl.exe http://localhost:8081/health
curl.exe "http://localhost:8081/v1/patterns?gameId=combat-arena"
curl.exe "http://localhost:8081/v1/patterns?gameId=racing-circuit"
curl.exe http://localhost:8081/v1/games/combat-arena/leaderboard
curl.exe http://localhost:8081/v1/games/racing-circuit/leaderboard
```

El store de Scala vive en memoria y se vacía al recrear el contenedor. Para consultar otras
partidas o estadísticas, primero hay que enviar sus eventos a Go mientras Compose está
levantado.

---

## 4. Integración continua

`.github/workflows/ci.yml` define el workflow **CI** para `push` a `main` y para pull requests.
El job corre en `ubuntu-latest`, una máquina temporal llamada *runner* que GitHub prepara,
usa para ejecutar los pasos y descarta al terminar.

El workflow ejecuta:

1. `go test -race ./...` en `go-ingestion/`.
2. `go test -race ./...` en `simulator/`.
3. `sbt test` en `scala-analytics/` con Java 21.
4. `docker compose build` desde la raíz.

Para revisar una ejecución, subir el workflow a GitHub mediante un push o un pull request y
abrir **Actions → CI → test-and-build**. En Windows, `go test -race` requiere CGO y un
compilador C; la prueba local de este entorno no pudo activar CGO. En GitHub Actions el job
usa Ubuntu; localmente, `go test ./...`, `sbt test` y `docker compose build` sí pasaron.

---

## 5. Validación realizada

- Las imágenes `gamestats/scala-analytics:local`, `gamestats/go-ingestion:local` y
  `gamestats/simulator:local` se construyeron correctamente.
- `docker compose up --build -d` creó el grupo `gamestats`; Scala y Go quedaron saludables.
- El simulador envió sus eventos a Go; el sender confirmó el envío de los lotes de combate y
  carreras a Scala.
- `sbt test` pasó con 14 pruebas.
- `go test ./...` pasó para los módulos de ingestión y simulación en Windows.
- `docker compose build` pasó para las tres imágenes.

El workflow de CI está preparado localmente. GHCR y Azure todavía requieren sus pasos de
configuración y publicación; no se consideran desplegados en esta etapa.
