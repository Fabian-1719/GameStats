# Stage 1: Build
FROM golang:1.22-alpine AS builder

WORKDIR /src

# Instalar certificados y utilidades necesarias para build
RUN apk add --no-cache git ca-certificates

# Copiar dependencias y código fuente
COPY go.mod ./
# Si se agrega go.sum en el futuro, se copia automáticamente si existe
COPY . .

# Compilación de binario estático y optimizado
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/ingestion ./cmd/ingestion

# Stage 2: Runtime mínimo y seguro
FROM alpine:3.19

# Instalar ca-certificates para peticiones HTTPS salientes
RUN apk add --no-cache ca-certificates tzdata && \
    addgroup -S appgroup && adduser -S appuser -G appgroup

WORKDIR /app

# Copiar ejecutable y catálogo por defecto
COPY --from=builder /bin/ingestion /app/ingestion
COPY --from=builder /src/config /app/config

# Crear directorios de trabajo con permisos para el usuario sin privilegios
RUN mkdir -p /app/data /app/failed-batches && chown -R appuser:appgroup /app

USER appuser

EXPOSE 8080

ENV PORT=8080 \
    CATALOG_PATH=/app/config/catalog.json \
    REJECTED_PATH=/app/data/rejected.ndjson \
    FAILED_BATCH_DIR=/app/failed-batches

ENTRYPOINT ["/app/ingestion"]
