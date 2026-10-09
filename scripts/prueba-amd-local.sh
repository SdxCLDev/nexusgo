#!/usr/bin/env bash
# Prueba local de la integración amd-to-sgp-descarga-minuta.
#
# Levanta Nexus en ambiente dev, obtiene un token, dispara la descarga de
# minutas desde AMD, hace polling del job y muestra un veredicto (cuántas
# minutas se descargaron, éxitos/fallos) o el motivo si falló.
#
# Uso:
#   NEXUS_AMD_USER='admin' NEXUS_AMD_PASSWORD='la-clave' ./scripts/prueba-amd-local.sh
#
# Variables opcionales:
#   NEXUS_AMD_BASE_URL  (default: https://amddev.sodexhochile.cl)
#   PORT                (default: 8080)
#   TIMEOUT_SECONDS     (default: 180) tiempo máximo de espera del job
set -uo pipefail

PORT="${PORT:-8080}"
TIMEOUT_SECONDS="${TIMEOUT_SECONDS:-180}"
BASE="http://127.0.0.1:${PORT}"
API_KEY="sgp-dev-local-key" # default de dev (ver internal/config/config.go)

if [[ -z "${NEXUS_AMD_USER:-}" || -z "${NEXUS_AMD_PASSWORD:-}" ]]; then
  echo "ERROR: definí NEXUS_AMD_USER y NEXUS_AMD_PASSWORD antes de ejecutar." >&2
  echo "Ej: NEXUS_AMD_USER='admin' NEXUS_AMD_PASSWORD='clave' $0" >&2
  exit 1
fi

cd "$(dirname "$0")/.."

DB="$(mktemp -u /tmp/nexus-prueba-XXXX.db)"
LOG="$(mktemp /tmp/nexus-prueba-XXXX.log)"

# jqlite: parsea un campo JSON simple de stdin sin depender de jq. Devuelve ""
# si la entrada está vacía o no es JSON (ej. si el servidor se cayó).
jget() { python3 -c "import sys,json
try:
    d=json.load(sys.stdin)
except Exception:
    print(''); sys.exit(0)
k='$1'
v=d
for part in k.split('.'):
    v=v.get(part) if isinstance(v,dict) else None
print('' if v is None else v)"; }

server_alive() { kill -0 "${SERVER_PID}" 2>/dev/null; }

dump_logs() {
  echo ""
  echo "----- últimas líneas del log de Nexus (${LOG}) -----"
  tail -n 25 "${LOG}" 2>/dev/null || true
  echo "----------------------------------------------------"
}

# Preflight: si algo ya responde en el puerto, abortar con un mensaje claro en
# vez de hablarle sin querer a otra instancia (ej. un Nexus viejo colgado).
if curl -sf "${BASE}/health" >/dev/null 2>&1; then
  echo "ERROR: ya hay un servicio escuchando en ${BASE}." >&2
  echo "       Cerralo (ej.: fuser -k ${PORT}/tcp) o usá otro puerto: PORT=8090 $0" >&2
  exit 1
fi

# Compilamos un binario y lo ejecutamos directo (NO `go run`): `go run` lanza
# el binario como proceso hijo, y al matar el padre el hijo queda huérfano
# reteniendo el puerto. Con un binario propio, SERVER_PID es el proceso real y
# el cleanup lo detiene de verdad.
BIN="$(mktemp -u /tmp/nexus-prueba-bin-XXXX)"
echo "==> Compilando Nexus ..."
if ! go build -o "${BIN}" ./cmd/nexus; then
  echo "ERROR: no compiló." >&2; exit 1
fi

echo "==> Levantando Nexus (dev) en ${BASE} ..."
NEXUS_ENV=dev \
NEXUS_HTTP_ADDR="127.0.0.1:${PORT}" \
NEXUS_DB_PATH="${DB}" \
NEXUS_LOG_LEVEL=info \
"${BIN}" >"${LOG}" 2>&1 &
SERVER_PID=$!
cleanup() { kill "${SERVER_PID}" 2>/dev/null || true; rm -f "${DB}"* "${LOG}" "${BIN}"; }
trap cleanup EXIT

echo "==> Esperando a que esté listo ..."
for _ in $(seq 1 60); do
  curl -sf "${BASE}/health" >/dev/null 2>&1 && break
  server_alive || { echo "ERROR: Nexus terminó durante el arranque."; dump_logs; exit 1; }
  sleep 0.5
done
if ! curl -sf "${BASE}/health" >/dev/null 2>&1; then
  echo "ERROR: Nexus no respondió en ${BASE}/health"; dump_logs; exit 1
fi

echo "==> Obteniendo token ..."
TOKEN=$(curl -s -X POST "${BASE}/api/v1/auth/token" \
  -H "Content-Type: application/json" \
  -d "{\"client_id\":\"sgp\",\"api_key\":\"${API_KEY}\"}" | jget access_token)
if [[ -z "${TOKEN}" ]]; then
  echo "ERROR: no se pudo obtener token."; dump_logs; exit 1
fi

echo "==> Disparando descarga de minutas (amd-to-sgp-descarga-minuta) ..."
JOB=$(curl -s -X POST "${BASE}/api/v1/integrations/amd-to-sgp-descarga-minuta/send" \
  -H "Authorization: Bearer ${TOKEN}" -H "Content-Type: application/json" \
  -d '{"source_system":"SGP","timestamp":"2026-10-09T12:00:00Z","payload":{}}' | jget job_id)
if [[ -z "${JOB}" ]]; then
  echo "ERROR: no se obtuvo job_id."; dump_logs; exit 1
fi
echo "    job_id = ${JOB}"

echo "==> Esperando a que el job termine (máx ${TIMEOUT_SECONDS}s) ..."
STATUS="" ; STATUS_JSON=""
for _ in $(seq 1 "${TIMEOUT_SECONDS}"); do
  if ! server_alive; then
    echo ""
    echo "❌ El servicio de Nexus se CAYÓ mientras procesaba el job (no debería)."
    dump_logs
    exit 1
  fi
  STATUS_JSON=$(curl -s "${BASE}/api/v1/jobs/${JOB}" -H "Authorization: Bearer ${TOKEN}")
  STATUS=$(echo "${STATUS_JSON}" | jget status)
  case "${STATUS}" in COMPLETED|PARTIAL|FAILED) break;; esac
  sleep 1
done

echo ""
echo "============================ VEREDICTO ============================"
case "${STATUS}" in
  "")
    echo "❓ No se pudo leer el estado del job (¿servicio caído?)."
    dump_logs
    ;;
  RUNNING|PENDING)
    echo "⏳ El job sigue en ${STATUS} tras ${TIMEOUT_SECONDS}s. Si hay muchas minutas,"
    echo "   subí el timeout:  TIMEOUT_SECONDS=600 ... $0"
    ;;
  FAILED)
    echo "❌ El job FALLÓ. Motivo (result_summary.error):"
    echo "   $(echo "${STATUS_JSON}" | jget result_summary.error)"
    ;;
  COMPLETED|PARTIAL)
    RESULT=$(curl -s "${BASE}/api/v1/jobs/${JOB}/result" -H "Authorization: Bearer ${TOKEN}")
    echo "${RESULT}" | python3 - "${STATUS}" <<'PY'
import sys, json
status = sys.argv[1]
try:
    r = json.load(sys.stdin)
except Exception:
    print("No se pudo leer el resultado."); sys.exit(0)
s = r.get("summary", {})
print(f"✅ Job {status}: {s.get('total',0)} minuta(s) — {s.get('success',0)} OK, {s.get('failed',0)} con error.")
items = r.get("items", [])
for it in items[:3]:
    if it.get("status") == "SUCCESS":
        det = (it.get("data") or {}).get("detalle", []) or []
        print(f"  - minuta {it.get('external_id')}: OK, {len(det)} fila(s) de detalle")
    else:
        print(f"  - minuta {it.get('external_id')}: FAILED ({it.get('error','')})")
if len(items) > 3:
    print(f"  ... y {len(items)-3} más")
PY
    ;;
esac
echo "==================================================================="
