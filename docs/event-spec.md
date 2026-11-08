# GameStats — Especificación de eventos y contrato entre módulos

> Estado: **Congelado para traspaso (Día 3)**. Base estable para la implementación de Scala por Cristofer.

## 1. Juegos considerados

| gameId | Juego | Por qué se eligió |
|---|---|---|
| `combat-arena` | Combate por eliminación | Muchos eventos por segundo (luce la concurrencia); naturalmente produce estadísticas combinadas (precisión, K/D) y patrones de secuencia (rachas). |
| `racing-circuit` | Carreras por vueltas | Muy distinto al combate: eventos con tiempos y posiciones; permite consistencia, remontadas y relación entre penalizaciones y tiempos. |
| `connect-four` | Cuatro en Línea contra IA | Movimientos, resultado y coste de búsqueda de la IA Prolog. |
| `snake-arena` | Snake individual | Puntuación, longitud, comida recogida y causa de fin. |
| `memory-match` | Memoria individual | Pares, intentos, cartas volteadas y precisión. |

**Agregar un juego nuevo** (por ejemplo un tercero) requiere: (1) una sección nueva en
`go-ingestion/config/catalog.json`, (2) un `GameAnalyzer` nuevo en Scala y (3) que el
simulador sepa generarlo. No se modifica el validador ni el router de Go.

## 2. Evento común

```json
{
  "eventId": "e-000123",
  "timestamp": "2026-10-10T15:04:05.120Z",
  "gameId": "combat-arena",
  "gameVersion": "1.0",
  "matchId": "m-0042",
  "playerId": "p-07",
  "type": "SHOT_HIT",
  "action": "RIFLE",
  "data": { "targetId": "p-03", "damage": 27, "headshot": false }
}
```

| Campo | Tipo | Obligatorio | Descripción |
|---|---|---|---|
| `eventId` | string | sí | Identificador único del evento. Sirve para descartar duplicados. |
| `timestamp` | string | sí | Instante del hecho, RFC 3339 (`2026-10-10T15:04:05.120Z`). |
| `gameId` | string | sí | Juego que produjo el evento. Debe existir en el catálogo. |
| `gameVersion` | string | sí | Versión del juego. |
| `matchId` | string | sí | Partida a la que pertenece. Go agrupa por este campo. |
| `playerId` | string | según el tipo | Jugador asociado. Lo exigen los tipos con `requiresPlayer: true`. |
| `type` | string | sí | Tipo general del evento. |
| `action` | string | no | Acción específica (arma, maniobra…). |
| `data` | objeto | según el tipo | Información propia del juego; sus campos obligatorios los fija el catálogo. |

Los eventos pueden llegar **desordenados** y **duplicados**: Go ordena por `timestamp` y
descarta repetidos por `eventId`.

## 3. Catálogo de eventos

La fuente de verdad es `go-ingestion/config/catalog.json`. Una regla específica del juego
tiene prioridad sobre la sección `_common`.

**Comunes (todos los juegos):** `MATCH_STARTED`, `PLAYER_JOINED`, `PLAYER_LEFT`,
`PLAYER_DISCONNECTED`, `PLAYER_RECONNECTED`, `MATCH_FINISHED`.

**`combat-arena`**

| type | Jugador | Campos de `data` |
|---|---|---|
| `SHOT_FIRED` | sí | `weapon` |
| `SHOT_HIT` | sí | `targetId`, `damage` (opcional: `headshot`) |
| `PLAYER_ELIMINATED` | sí | `targetId`, `weapon` (opcional: `distance`) |
| `MATCH_FINISHED` | no | `winnerId`, `scores` |

**`racing-circuit`**

| type | Jugador | Campos de `data` |
|---|---|---|
| `MATCH_STARTED` | no | `track`, `laps` |
| `LAP_COMPLETED` | sí | `lap`, `lapTimeMs`, `position` |
| `POSITION_CHANGED` | sí | `from`, `to` |
| `PENALTY` | sí | `reason`, `seconds` |
| `MATCH_FINISHED` | no | `classification` |

**Juegos de GameConsole**

| gameId | type | Jugador | Campos de `data` |
|---|---|---|---|
| `connect-four` | `MOVE` | sí | `column`, `row`, `turn` |
| `connect-four` | `AI_DECISION` | sí | `column`, `level`, `nodes`, `timeMs`, `score` |
| `connect-four` | `PLAYER_WON`, `PLAYER_LOST` | sí | `playerId` |
| `connect-four` | `MATCH_FINISHED` | no | `result`, `durationMs` |
| `snake-arena` | `FOOD_EATEN` | sí | `score`, `length` |
| `snake-arena` | `PLAYER_DIED` | sí | `cause`, `score` |
| `snake-arena` | `MATCH_FINISHED` | no | `result`, `durationMs` |
| `memory-match` | `CARD_FLIPPED` | sí | `cardIndex` |
| `memory-match` | `PAIR_MATCHED` | sí | `pairId`, `attempts` |
| `memory-match` | `PAIR_MISSED` | sí | `attempts` |
| `memory-match` | `MATCH_FINISHED` | no | `result`, `durationMs` |

## 4. Estadísticas y patrones (los calcula Scala)

| Tipo | Combate | Carreras |
|---|---|---|
| Por jugador | K/D, daño promedio, % de headshots | Mejor vuelta, tiempo promedio, posiciones ganadas, penalizaciones |
| Por partida | Duración, MVP, ranking, daño total | Clasificación final, vuelta más rápida, adelantamientos |
| Combinada (varios tipos de evento) | Precisión = `SHOT_HIT` / `SHOT_FIRED` | Consistencia (desviación estándar de vueltas) ligada a penalizaciones |
| Patrón de secuencia | **Racha:** 3 o más eliminaciones en menos de 10 s sin morir. **Abandono sospechoso:** desconexión sin reconexión en 60 s mientras iba perdiendo | **Remontada:** posición baja a mitad de carrera y victoria final |
| GameConsole | Movimientos, nodos y tiempo promedio de IA | Puntuación, longitud y comidas | Pares, intentos y precisión |

## 5. Validación en Go (Día 1)

Un evento se rechaza, con un código y un motivo explícito, si ocurre alguna de estas cosas
(en este orden):

| Código | Causa |
|---|---|
| `BAD_JSON` | No es JSON, o un campo tiene un tipo incorrecto (por ejemplo `timestamp` numérico). |
| `MISSING_FIELD` | Falta `eventId`, `timestamp`, `gameId`, `gameVersion`, `matchId` o `type`. |
| `BAD_TIMESTAMP` | `timestamp` no cumple RFC 3339. |
| `UNKNOWN_GAME` | `gameId` no está en el catálogo. |
| `UNKNOWN_TYPE` | El `type` no existe para ese juego ni en `_common`. |
| `MISSING_PLAYER` | El tipo exige `playerId` y no viene. |
| `MISSING_DATA_FIELD` | Falta (o es `null`) un campo obligatorio de `data`. |

Los rechazados se guardan en `rejected.ndjson` con la fecha, el origen (`http:evento 3`,
`archivo.ndjson:línea 8`), el motivo y el contenido original.

## 6. Contrato entre módulos (HTTP/REST + JSON)

### 6.1 Entrada a Go — puerto 8080

| Método | Ruta | Cuerpo | Respuesta |
|---|---|---|---|
| `POST` | `/v1/events` | un evento (objeto) o varios (arreglo) | `202` todos aceptados · `207` mixto · `422` todos rechazados · `400` cuerpo inválido o vacío |
| `GET` | `/health` | — | `200` con estado y contadores |

Respuesta de `POST /v1/events`:

```json
{
  "received": 2, "accepted": 1, "rejected": 1,
  "errors": [{ "index": 1, "eventId": "e-2", "reason": "MISSING_DATA_FIELD: el tipo \"SHOT_HIT\" exige data.damage" }]
}
```

Alternativa por archivo: `ingestion -file datos.ndjson` (un evento por línea).

### 6.2 Go → Scala — puerto 8081

Go entrega **un lote por partida**, con los eventos ya sin duplicados y ordenados por `timestamp`:

```json
{ "matchId": "m-0042", "gameId": "combat-arena", "status": "COMPLETE", "events": [ { "...": "..." } ] }
```

`status` le dice a Scala si la partida está completa:

| status | Significado |
|---|---|
| `COMPLETE` | Se vio `MATCH_FINISHED`. |
| `TIMEOUT` | La partida dejó de recibir eventos (`IDLE_TIMEOUT`) sin llegar a `MATCH_FINISHED`. |
| `SHUTDOWN` | El servicio se apagó con la partida todavía en curso. |

Scala debe tratar `TIMEOUT` y `SHUTDOWN` como partidas incompletas (por ejemplo, no declarar ganador ni remontada).

**Cuándo se envía un lote:** `FINISH_GRACE` (2 s por defecto) después de `MATCH_FINISHED`, porque
eventos anteriores pueden llegar tarde por el desorden; o `IDLE_TIMEOUT` (30 s) sin eventos; o al apagar.
**Una partida se envía una sola vez.** Un evento que llega después va a `rejected.ndjson` con el
motivo `LATE_EVENT`. Dos juegos distintos pueden repetir un `matchId`: Go los trata como partidas
separadas (la clave es `gameId + matchId`).

| Método | Ruta | Función |
|---|---|---|
| `POST` | `/v1/analyze` | Recibe el lote de una partida |
| `GET` | `/v1/stats/players/{id}` | Estadísticas de un jugador |
| `GET` | `/v1/matches/{id}` | Análisis de una partida |
| `GET` | `/v1/patterns` | Patrones detectados |
| `GET` | `/v1/games/{gameId}/leaderboard` | Clasificación por juego |

> Los endpoints de Scala quedan **acordados** aquí; los implementa Cristofer en los días 4 y 5.

## 7. Configuración (variables de entorno de Go)

| Variable | Por defecto | Uso |
|---|---|---|
| `PORT` | `8080` | Puerto HTTP |
| `CATALOG_PATH` | `config/catalog.json` | Catálogo de eventos |
| `REJECTED_PATH` | `rejected.ndjson` | Archivo de rechazados (DLQ) |
| `IDLE_TIMEOUT` | `30s` | Sin eventos durante este tiempo, la partida se envía como `TIMEOUT` |
| `FINISH_GRACE` | `2s` | Espera tras `MATCH_FINISHED` por eventos que llegan tarde |
| `BATCH_DIR` | — | Si se define, guarda cada lote como `<gameId>_<matchId>.json` (depuración local) |
| `SCALA_URL` | — | Base URL de Scala (ejemplo: `http://localhost:8081`) |
| `SENDER_WORKERS` | `4` | Número de goroutines del pool de envío a Scala |
| `SENDER_TIMEOUT` | `5s` | Timeout por petición HTTP hacia Scala |
| `FAILED_BATCH_DIR` | `failed-batches` | Directorio para resguardar lotes que fallen tras reintentos |
