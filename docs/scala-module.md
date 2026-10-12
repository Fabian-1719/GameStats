# Módulo de Análisis y Patrones (Scala)

Documento de arquitectura y comportamiento del módulo `scala-analytics` de **GameStats**.
Recibe lotes por partida desde Go, calcula estadísticas y detecta patrones de juego.

---

## 1. Responsabilidad y alcance

Scala es responsable del análisis agregado, no de recibir eventos individuales ni validarlos
contra el catálogo. Go envía un `Batch` por partida con eventos deduplicados y ordenados por
`timestamp`.

- Analizar `combat-arena` y `racing-circuit` mediante implementaciones de `GameAnalyzer`.
- Calcular estadísticas por jugador y por partida.
- Detectar patrones de secuencia y guardarlos junto con el resultado.
- Exponer análisis, estadísticas acumuladas, leaderboards y patrones por HTTP.

---

## 2. Diseño funcional y estado

Los analizadores reciben `Batch` y producen `MatchAnalysis` sin mutar el lote. Las
transformaciones usan colecciones funcionales (`filter`, `map`, `groupBy`, `fold` y `collect`).

El único estado mutable de la aplicación es un `TrieMap` en memoria, indexado por
`(gameId, matchId)`. Cada entrada guarda el análisis y sus patrones. Si llega de nuevo la misma
partida, reemplaza esa entrada; juegos distintos pueden compartir `matchId`.

Los modelos principales son `Event` (con `data` como `ujson.Value`), `Batch`, `MatchAnalysis`,
estadísticas de jugador y `DetectedPattern`. El registro `Map[String, GameAnalyzer]` selecciona
el analizador por `gameId`; un juego desconocido se rechaza con HTTP `422`.

---

## 3. Analizadores por juego

### 3.1. `combat-arena`

**Por jugador:**

- `kills`: eventos `PLAYER_ELIMINATED` asociados al jugador.
- `deaths`: eliminaciones donde el jugador aparece como `data.targetId`.
- `kd`: `kills / max(deaths, 1)`.
- `avgDamage`: daño total recibido en `SHOT_HIT` dividido por impactos.
- `headshotPct`: impactos a la cabeza como porcentaje de `SHOT_HIT`.
- `shotsFired`, `shotsHit` y `accuracy`: precisión es `shotsHit / max(shotsFired, 1)`.

**Por partida:** duración entre `MATCH_STARTED` y `MATCH_FINISHED` (o el último evento si
incompleta), daño total, ranking y MVP. El ranking ordena por kills descendentes y luego daño
descendente. `winnerId` se toma exclusivamente de `MATCH_FINISHED.data.winnerId` y solo se
publica si `status` es `COMPLETE`.

### 3.2. `racing-circuit`

**Por jugador:** mejor vuelta, promedio y desviación estándar poblacional de tiempos,
posiciones ganadas, número de penalizaciones y segundos de penalización. Las posiciones ganadas
suman `from - to` de cada `POSITION_CHANGED`.

**Por partida:** clasificación de `MATCH_FINISHED`, vuelta más rápida (jugador y milisegundos)
y cantidad de `POSITION_CHANGED` donde `to < from` (adelantamientos). La clasificación solo se
publica para partidas `COMPLETE`.

---

## 4. Patrones

Cada resultado tiene `{type, gameId, matchId, playerId, detail}` y queda almacenado con el
análisis de la partida.

| Patrón | Condición |
|---|---|
| `KILL_STREAK` | Al menos 3 `PLAYER_ELIMINATED` del mismo jugador en una ventana de 10 s, sin que aparezca como `targetId` de una eliminación intermedia. `detail` contiene cantidad, inicio y fin. |
| `SUSPICIOUS_ABANDON` | `PLAYER_DISCONNECTED` sin reconexión en los siguientes 60 s observados, y con menos kills que el líder en el momento de desconectarse. |
| `COMEBACK` | Solo `COMPLETE`: el ganador oficial de `MATCH_FINISHED.data.classification` estaba en la mitad de atrás en la vuelta `laps / 2`. |

---

## 5. API HTTP

El servicio escucha en `0.0.0.0:8081` para aceptar conexiones desde el host y desde otros
contenedores.

| Método y ruta | Resultado |
|---|---|
| `POST /v1/analyze` | Guarda o reemplaza el análisis y responde con `MatchAnalysis`; juego desconocido: `422`. |
| `GET /v1/matches/{id}?gameId=` | Devuelve el análisis; sin `gameId`, puede devolver un arreglo cuando hay partidas homónimas. |
| `GET /v1/patterns?gameId=&type=&matchId=` | Devuelve patrones que cumplen los filtros opcionales. |
| `GET /v1/stats/players/{id}` | Acumula estadísticas del jugador por `gameId`; responde `404` si no existe. |
| `GET /v1/games/{gameId}/leaderboard` | Combate: kills descendentes y K/D para desempatar. Carreras: victorias descendentes y mejor vuelta ascendente. |
| `GET /health` | Devuelve `{"status":"ok"}`. |

Las estadísticas de jugador acumulan partidas almacenadas en el proceso. Los porcentajes y
promedios agregados se recalculan desde totales (por ejemplo, impactos y disparos), no promedian
los porcentajes de cada partida.

---

## 6. Pruebas

La suite MUnit cubre estadísticas de ambos juegos, lotes incompletos, reemplazo por clave,
filtros de patrones, acumulación de estadísticas y orden de leaderboards. También prueba
positivos y negativos para los tres patrones, incluyendo una racha que excede 10 s, una
reconexión dentro de 60 s y una remontada en `TIMEOUT`.

Ejecutar desde `scala-analytics/`:

```powershell
sbt test
```

La suite verificada pasó con 14 pruebas.
