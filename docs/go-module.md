# Módulo de Ingestión y Concurrencia (Go)

Documento de diseño arquitectónico y justificación técnica del módulo Go en **GameStats**.

---

## 1. Responsabilidad y alcance del módulo

El módulo desarrollado en Go se sitúa en la frontera de entrada del sistema. Su objetivo no es realizar análisis ni cálculos estadísticos (responsabilidad delegada a Scala), sino garantizar que el flujo de eventos se ingiera, valide, limpie, ordene y agrupe de forma fiable y concurrente.

### Tareas asignadas:
1. **Ingestión multi-origen**: Recepción continua de eventos mediante HTTP (`POST /v1/events`) o ingesta masiva por lotes desde archivos NDJSON (`-file`).
2. **Validación guiada por catálogo**: Desacoplamiento del esquema. Un archivo JSON de configuración (`catalog.json`) define las reglas por juego sin requerir recompilar el código.
3. **Dead Letter Queue (DLQ)**: Registro auditable (`rejected.ndjson`) de cualquier evento inválido (JSON corrupto, campo faltante, timestamp no RFC 3339) o evento tardío (`LATE_EVENT`).
4. **Deduplicación y ordenamiento temporal**: Descarte de duplicados mediante `eventId` y ordenamiento cronológico por `timestamp` (RFC 3339 Nano).
5. **Agrupación y control de ciclo de vida por partida**: Reconstrucción del estado de cada partida activa (`WAITING`, `STARTED`, `FINISHED`) y emisión en un lote único (`model.Batch`).
6. **Entrega confiable a Scala**: Worker pool HTTP con reintentos, retroceso exponencial (*exponential backoff*) y aislamiento en disco de lotes fallidos (`failed-batches/`).

---

## 2. Arquitectura y diseño de concurrencia

El diseño sigue estrictamente la filosofía de Go:  
> *"Do not communicate by sharing memory; instead, share memory by communicating."*

```
[Cliente HTTP / NDJSON]
          │
          ▼
   [Processor / Validador] ──(inválido)──► [DLQ: rejected.ndjson]
          │ (válido)
          ▼ (chan Event)
      [Router] (1 goroutine, map[matchKey]chan Event sin mutex)
       ├──► [Worker Partida 1] (goroutine: deduplicación + orden + timers)
       ├──► [Worker Partida 2] (goroutine: deduplicación + orden + timers)
       └──► [Worker Partida N] (goroutine: deduplicación + orden + timers)
          │
          ▼ (chan Batch)
   [Sender Worker Pool] (N goroutines concurrentes)
          │
          ├──(POST /v1/analyze)──► [Módulo Scala: scala-analytics]
          └──(fallo permanente)─► [Disco: failed-batches/*.json]
```

### 2.1. Router sin contención de locks
En sistemas concurrentes de alta carga, un mapa global protegido por un `sync.RWMutex` se convierte en un cuello de botella debido a la contención de memoria caché entre núcleos del procesador.
- El **Router** se ejecuta en su propia goroutine y es el **único dueño** del mapa de partidas activas (`map[string]chan Event`).
- Cuando entra un evento, el router busca el canal del worker correspondiente; si no existe, crea el worker y su canal, y le entrega el evento.
- Al no haber múltiples hilos accediendo al mapa simultáneamente, se eliminan completamente los bloqueos por contención.

### 2.2. Aislamiento por partida (Worker Goroutines)
Cada partida que recibe eventos tiene su propia goroutine dedicada (`internal/match/worker.go`):
- **Canal sin buffer**: Garantiza sincronización estricta; ningún evento queda flotando en buffers intermedios sin haber sido reconocido por la partida.
- **Estado local inmutable desde el exterior**: Cada worker gestiona una estructura `State` privada donde acumula los eventos, verifica duplicados en un `map[string]struct{}` y ordena los eventos según su timestamp real.
- **Ciclo de vida y temporizadores (`select` multiplexado)**:
  - `FINISH_GRACE` (2 s por defecto): Tras recibir `MATCH_FINISHED`, el worker espera un breve lapso antes de emitir el lote. Esto resuelve el problema de eventos que llegan ligeramente desordenados por la red pero que ocurrieron antes del fin de la partida.
  - `IDLE_TIMEOUT` (30 s por defecto): Si una partida deja de recibir eventos sin un evento de finalización, se cierra automáticamente y se emite como lote de tipo `TIMEOUT`.
  - `Context Cancellation`: Ante un apagado del servicio, el worker vuelca inmediatamente su estado como `SHUTDOWN`.

### 2.3. Envío concurrente a Scala (Sender Worker Pool)
Los lotes terminados se canalizan hacia un pool de trabajadores (`internal/sender/sender.go`):
- Permite que múltiples partidas se transmitan en paralelo hacia Scala sin bloquear la recepción de nuevos eventos.
- Si Scala experimenta lentitud o errores transitorios (5xx / 429), el worker aplica reintentos automáticos con retroceso exponencial.
- Si se agotan los reintentos, el lote se serializa en `failed-batches/` con su motivo de fallo, garantizando tolerancia a desastres (*zero data loss*).

### 2.4. Apagado ordenado (Graceful Shutdown)
El sistema implementa un cierre coordinado en cascada:
1. `signal.NotifyContext` captura `SIGINT` o `SIGTERM`.
2. El servidor HTTP deja de aceptar peticiones (`srv.Shutdown`).
3. Se cancela el contexto del router, provocando que todos los workers activos emitan sus lotes pendientes.
4. El canal del sender se drena por completo y el pool finaliza todas las transmisiones antes de que el proceso termine.

---

## 3. Justificación académica: ¿Por qué Go para esta etapa?

El enunciado del proyecto pide evaluar por qué determinadas responsabilidades corresponden a un enfoque imperativo/concurrente y otras a un enfoque funcional.

### Ventajas de Go para la Ingestión:
1. **Modelo de concurrencia CSP nativo y peso pluma**:
   Las goroutines de Go consumen alrededor de 2 KB de stack inicial (frente a 1–2 MB de un hilo típico del sistema operativo o la JVM). Esto permite mantener miles de partidas concurrentes en memoria con una huella mínima de recursos.
2. **Baja latencia y control de memoria predecible**:
   El recolector de basura de Go está optimizado para latencias sub-milisegundo en aplicaciones de red. No sufre de pausas *stop-the-world* prolongadas, crucial para procesar flujos continuos de eventos en tiempo real.
3. **Excelente soporte nativo para redes y I/O**:
   La biblioteca estándar de Go (`net/http`) es de nivel de producción, con servidores HTTP altamente optimizados y soporte idiomático para `context.Context` en cancelaciones y timeouts.
4. **Tipado estático con mutabilidad controlada**:
   La preparación de datos (ordenar un slice, descartar duplicados mediante tablas hash locales, contar eventos) es intrínsecamente imperativa. Realizar esto en Go antes de llegar a Scala ahorra costos computacionales masivos al motor analítico funcional.
