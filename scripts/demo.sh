#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(CDPATH= cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
COMPOSE_FILE="$SCRIPT_DIR/../docker-compose.yml"
GAMESTATS_GO_URL="${GAMESTATS_GO_URL:-http://localhost:8080}"
GAMESTATS_SCALA_URL="${GAMESTATS_SCALA_URL:-http://localhost:8081}"
TIMEOUT_SECONDS=90
START_SECONDS=$SECONDS

has_timed_out() {
  if (( SECONDS - START_SECONDS >= TIMEOUT_SECONDS )); then
    return 0
  fi
  return 1
}

wait_for_url() {
  local url="$1"
  while ! has_timed_out; do
    if curl --fail --silent --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "Timeout de $TIMEOUT_SECONDS s esperando $url. Comprueba Docker Desktop y Compose." >&2
  return 1
}

wait_for_simulator() {
  local container_id simulator_state
  echo "Esperando a que termine el contenedor simulator..."
  while ! has_timed_out; do
    container_id="$(docker compose -f "$COMPOSE_FILE" ps --all --quiet simulator 2>/dev/null | head -n 1 || true)"
    if [[ -n "$container_id" ]]; then
      simulator_state="$(docker inspect --format '{{.State.Status}} {{.State.ExitCode}}' "$container_id" 2>/dev/null || true)"
      case "$simulator_state" in
        "exited 0") return 0 ;;
        exited\ *)
          echo "El contenedor simulator termino con estado: $simulator_state." >&2
          return 1
          ;;
      esac
    fi
    sleep 1
  done
  echo "Timeout de $TIMEOUT_SECONDS s esperando que termine simulator." >&2
  return 1
}

get_leaderboard_match_count() {
  local game_id="$1"
  local body
  body="$(curl --fail --silent --max-time 3 "$GAMESTATS_SCALA_URL/v1/games/$game_id/leaderboard" 2>/dev/null)" || return 1
  printf '%s\n' "$body" | awk '
    {
      remaining = $0
      while (match(remaining, /"matches"[[:space:]]*:[[:space:]]*[0-9]+/)) {
        value = substr(remaining, RSTART, RLENGTH)
        sub(/^[^:]*:[[:space:]]*/, "", value)
        total += value
        remaining = substr(remaining, RSTART + RLENGTH)
      }
    }
    END { print total + 0 }
  '
}

get_total_match_count() {
  local combat_count racing_count
  combat_count="$(get_leaderboard_match_count combat-arena)" || return 1
  racing_count="$(get_leaderboard_match_count racing-circuit)" || return 1
  printf '%s\n' "$((combat_count + racing_count))"
}

wait_for_stable_analysis() {
  local previous_count="" stable_since=0 current_count
  local analysis_ready=false
  echo "Esperando a que termine el análisis..."
  while ! has_timed_out; do
    current_count="$(get_total_match_count 2>/dev/null || true)"
    if [[ "$current_count" =~ ^[0-9]+$ ]] && (( current_count > 0 )); then
      if [[ "$current_count" == "$previous_count" ]]; then
        if (( SECONDS - stable_since >= 10 )) && ! has_timed_out; then
          analysis_ready=true
          break
        fi
      else
        previous_count="$current_count"
        stable_since=$SECONDS
      fi
    else
      previous_count=""
      stable_since=0
    fi
    sleep 1
  done
  if [[ "$analysis_ready" != true ]]; then
    echo "Timeout de $TIMEOUT_SECONDS s: el total de partidas no se mantuvo estable durante 10 s." >&2
    return 1
  fi
}

echo "Esperando Go en $GAMESTATS_GO_URL/health..."
wait_for_url "$GAMESTATS_GO_URL/health"
wait_for_simulator
wait_for_stable_analysis

run_demo() {
  local title="$1"
  local url="$2"
  printf '\n=== %s ===\n' "$title"
  curl --fail --silent --show-error "$url"
  printf '\n'
}

run_demo "Health (Go)" "$GAMESTATS_GO_URL/health"
run_demo "Leaderboard: combat-arena" "$GAMESTATS_SCALA_URL/v1/games/combat-arena/leaderboard"
run_demo "Leaderboard: racing-circuit" "$GAMESTATS_SCALA_URL/v1/games/racing-circuit/leaderboard"
run_demo "Patrones: combat-arena" "$GAMESTATS_SCALA_URL/v1/patterns?gameId=combat-arena"
run_demo "Patrones: racing-circuit" "$GAMESTATS_SCALA_URL/v1/patterns?gameId=racing-circuit"
run_demo "Estadisticas: p-01" "$GAMESTATS_SCALA_URL/v1/stats/players/p-01"
run_demo "Partida: m-pat-c-0001" "$GAMESTATS_SCALA_URL/v1/matches/m-pat-c-0001?gameId=combat-arena"