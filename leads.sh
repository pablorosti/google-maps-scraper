#!/usr/bin/env bash
# Province lead scraper (Docker). Progress is saved in leads-output/, so any
# command can be run at any time without losing leads.
#
#   ./leads.sh iniciar <provincia> <total> [por_rubro]  start (or resume) a province; without
#                                                       por_rubro the total is spread across all rubros
#   ./leads.sh estado <provincia>                       progress and health
#   ./leads.sh logs <provincia>                         follow live logs
#   ./leads.sh parar <provincia>                        stop (progress is kept)
#   ./leads.sh reanudar <provincia>                     resume with the saved settings
#   ./leads.sh reiniciar <provincia>                    restart the container
#   ./leads.sh reparar <provincia>                      rebuild the image and recreate the container
#   ./leads.sh exportar <provincia>                     regenerate the rubro CSVs from the scraped data
#   ./leads.sh diagnostico <provincia>                  check Docker, container, disk and recent errors
#   ./leads.sh rubros                                   list available rubros
#
# Names with spaces go in quotes: ./leads.sh iniciar "buenos aires" 100000.
# CABA aliases (caba, "capital federal", ...) all share leads-output/caba.
# Optional env vars for iniciar: RUBROS (comma-separated slugs), CONCURRENCY
# (default 4). Proxies: put one proxy URL per line in leads-output/proxies.txt
# and they are used automatically.
set -euo pipefail

cd "$(dirname "$0")"

COMPOSE_FILE=docker-compose.leads.yml
OUT=leads-output

slug() {
  echo "$1" | tr '[:upper:]' '[:lower:]' \
    | sed 's/á/a/g; s/é/e/g; s/í/i/g; s/ó/o/g; s/ú/u/g; s/ñ/n/g' \
    | sed 's/[^a-z0-9]\{1,\}/-/g; s/^-//; s/-$//'
}

# canon maps a province name to its folder name, so every alias of the same
# province shares one folder and one container.
canon() {
  local s
  s="$(slug "$1")"
  case "$s" in
    caba | capital | capital-federal | ciudad-autonoma-de-buenos-aires | ciudad-de-buenos-aires | cdad-autonoma-de-buenos-aires)
      echo caba ;;
    tierra-del-fuego*)
      echo tierra-del-fuego ;;
    *)
      echo "$s" ;;
  esac
}

usage() {
  sed -n '2,22p' "$0" | sed 's/^# \{0,1\}//'
  exit 1
}

need_docker() {
  if ! docker info >/dev/null 2>&1; then
    if [[ "$(uname)" == "Darwin" ]]; then
      echo "Docker no está corriendo. Abriendo Docker Desktop..."
      open -a Docker
      for _ in $(seq 1 60); do docker info >/dev/null 2>&1 && return; sleep 3; done
    fi
    echo "Docker no responde. Abrí Docker Desktop y volvé a intentar." >&2
    exit 1
  fi
}

params_file() { echo "$OUT/$(canon "$1")/.params"; }

load_params() {
  local f
  f="$(params_file "$1")"
  if [[ ! -f "$f" ]]; then
    echo "No hay una corrida guardada para $1. Usá: ./leads.sh iniciar $1 <total>" >&2
    exit 1
  fi
  # shellcheck disable=SC1090
  source "$f"
}

compose() {
  local prov="$1"
  shift
  PROVINCIA="${PROVINCIA:-$prov}" \
    CARPETA="$(canon "$prov")" \
    PROXIES_FILE="$([[ -s "$OUT/proxies.txt" ]] && echo /data/proxies.txt || true)" \
    docker compose -f "$COMPOSE_FILE" -p "leads-$(canon "$prov")" "$@"
}

# Keep the Mac awake while the scraper runs (macOS only).
keep_awake() {
  [[ "$(uname)" == "Darwin" ]] || return 0
  local pidfile
  pidfile="$OUT/$(canon "$1")/.caffeinate.pid"
  if [[ -f "$pidfile" ]] && kill -0 "$(cat "$pidfile")" 2>/dev/null; then return 0; fi
  nohup caffeinate -is >/dev/null 2>&1 &
  echo $! >"$pidfile"
}

allow_sleep() {
  local pidfile
  pidfile="$OUT/$(canon "$1")/.caffeinate.pid"
  [[ -f "$pidfile" ]] && kill "$(cat "$pidfile")" 2>/dev/null || true
  rm -f "$pidfile"
}

launch() {
  local prov="$1"
  shift
  PROVINCIA="$prov" TOTAL="$TOTAL" POR_RUBRO="$POR_RUBRO" RUBROS="$RUBROS" CONCURRENCY="$CONCURRENCY" \
    compose "$prov" up -d "$@"
  keep_awake "$prov"
  if [[ "$POR_RUBRO" == "0" ]]; then
    echo "Scraper corriendo para $prov (objetivo: $TOTAL leads repartidos entre todos los rubros)."
  else
    echo "Scraper corriendo para $prov (objetivo: $TOTAL leads, máximo $POR_RUBRO por rubro, en orden)."
  fi
  echo "Leads:    $OUT/$(canon "$prov")/<rubro>.csv"
  echo "Progreso: ./leads.sh estado $prov    Logs: ./leads.sh logs $prov"
}

prov="${2:-}"

case "${1:-}" in
  "" | -h | --help)
    usage
    ;;
  rubros)
    need_docker
    PROVINCIA=x docker compose -f "$COMPOSE_FILE" run --rm --no-deps leadsgen -list-rubros
    PROVINCIA=x docker compose -f "$COMPOSE_FILE" down >/dev/null 2>&1 || true
    ;;
  iniciar)
    [[ -n "$prov" && -n "${3:-}" ]] || usage
    need_docker
    mkdir -p "$OUT/$(canon "$prov")"
    TOTAL="$3" POR_RUBRO="${4:-0}" RUBROS="${RUBROS:-}" CONCURRENCY="${CONCURRENCY:-4}"
    printf 'TOTAL=%q\nPOR_RUBRO=%q\nRUBROS=%q\nCONCURRENCY=%q\n' \
      "$TOTAL" "$POR_RUBRO" "$RUBROS" "$CONCURRENCY" >"$(params_file "$prov")"
    launch "$prov" --build
    ;;
  reanudar)
    [[ -n "$prov" ]] || usage
    need_docker
    load_params "$prov"
    launch "$prov"
    ;;
  reiniciar)
    [[ -n "$prov" ]] || usage
    need_docker
    compose "$prov" restart
    keep_awake "$prov"
    echo "Reiniciado. Retoma desde donde estaba."
    ;;
  reparar)
    [[ -n "$prov" ]] || usage
    need_docker
    load_params "$prov"
    compose "$prov" down --remove-orphans || true
    PROVINCIA="$prov" compose "$prov" build --no-cache
    launch "$prov" --force-recreate
    ;;
  parar)
    [[ -n "$prov" ]] || usage
    need_docker
    compose "$prov" down
    allow_sleep "$prov"
    echo "Detenido. Para seguir: ./leads.sh reanudar $prov"
    ;;
  estado)
    [[ -n "$prov" ]] || usage
    report="$OUT/$(canon "$prov")/PROGRESO.txt"
    if [[ -f "$report" ]]; then cat "$report"; else echo "Todavía no hay progreso para $prov"; fi
    echo
    need_docker
    compose "$prov" ps --format 'table {{.Name}}\t{{.Status}}'
    ;;
  logs)
    [[ -n "$prov" ]] || usage
    need_docker
    compose "$prov" logs -f --tail 200
    ;;
  exportar)
    [[ -n "$prov" ]] || usage
    need_docker
    compose "$prov" run --rm --no-deps leadsgen \
      "-provincia=$prov" "-carpeta=$(canon "$prov")" -out=/data -solo-exportar
    echo "Archivos regenerados en $OUT/$(canon "$prov")/"
    ;;
  diagnostico)
    [[ -n "$prov" ]] || usage
    need_docker
    echo "== Docker: OK"
    cid="$(compose "$prov" ps -aq leadsgen 2>/dev/null || true)"
    if [[ -z "$cid" ]]; then
      echo "== Contenedor: no existe. Solución: ./leads.sh reanudar $prov"
    else
      docker inspect -f '== Contenedor: {{.State.Status}} (reinicios automáticos: {{.RestartCount}}, OOM: {{.State.OOMKilled}})' "$cid"
    fi
    report="$OUT/$(canon "$prov")/PROGRESO.txt"
    if [[ -f "$report" ]]; then
      age=$(( $(date +%s) - $(stat -f %m "$report" 2>/dev/null || stat -c %Y "$report") ))
      echo "== Último avance guardado: hace $((age / 60)) min"
      if (( age > 3 * 3600 )); then
        echo "   Más de 3 h sin avance. Probá: ./leads.sh reiniciar $prov (y si sigue igual: ./leads.sh reparar $prov)"
      fi
    fi
    echo "== Disco libre: $(df -h "$OUT" | awk 'NR==2 {print $4}')"
    [[ -s "$OUT/proxies.txt" ]] && echo "== Proxies: $(grep -c . "$OUT/proxies.txt") en uso" || echo "== Proxies: ninguno (IP propia)"
    echo "== Últimos avisos:"
    compose "$prov" logs --tail 2000 2>/dev/null | grep -E "leadsgen: .*(failed|pausing|restarting|recycling)" | tail -5 || echo "   ninguno"
    ;;
  *)
    usage
    ;;
esac
