FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/simulator ./cmd/simulator

FROM alpine:3.21 AS runtime
COPY --from=build /out/simulator /usr/local/bin/simulator
ENTRYPOINT ["/usr/local/bin/simulator"]
CMD ["--matches", "2", "--mode", "patterns", "--dups", "0", "--shuffle=false"]