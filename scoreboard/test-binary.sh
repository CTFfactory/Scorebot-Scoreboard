#!/usr/bin/env bash

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/scoreboard-binary-test.XXXXXX")"
BIN_PATH="${WORK_DIR}/scoreboard"
SCOREBOT_PID=""
SCOREBOARD_PID=""
PYTHON_BIN="$(command -v python3 || command -v python || true)"

if [[ -z "${PYTHON_BIN}" ]]; then
  printf "python3 or python is required for test-binary.sh\n" >&2
  exit 1
fi

export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

cleanup() {
  if [[ -n "${SCOREBOARD_PID}" ]] && kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
    kill -TERM "${SCOREBOARD_PID}" || true
    for _ in $(seq 1 30); do
      if ! kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
      kill -KILL "${SCOREBOARD_PID}" || true
    fi
    wait "${SCOREBOARD_PID}" 2>/dev/null || true
  fi
  if [[ -n "${SCOREBOT_PID}" ]] && kill -0 "${SCOREBOT_PID}" 2>/dev/null; then
    kill -TERM "${SCOREBOT_PID}" || true
    for _ in $(seq 1 30); do
      if ! kill -0 "${SCOREBOT_PID}" 2>/dev/null; then
        break
      fi
      sleep 0.1
    done
    if kill -0 "${SCOREBOT_PID}" 2>/dev/null; then
      kill -KILL "${SCOREBOT_PID}" || true
    fi
    wait "${SCOREBOT_PID}" 2>/dev/null || true
  fi
  rm -rf "${WORK_DIR}"
}
trap cleanup EXIT

free_port() {
  "${PYTHON_BIN}" - <<'PY'
import socket
s = socket.socket()
s.bind(("127.0.0.1", 0))
print(s.getsockname()[1])
s.close()
PY
}

assert_contains() {
  local file="$1"
  local needle="$2"
  if ! grep -q "${needle}" "${file}"; then
    printf "Expected %s to contain: %s\n" "${file}" "${needle}" >&2
    printf "File contents:\n" >&2
    cat "${file}" >&2
    exit 1
  fi
}

run_case() {
  local name="$1"
  local expected="$2"
  shift 2
  local out="${WORK_DIR}/${name}.out"
  local err="${WORK_DIR}/${name}.err"
  set +e
  "${BIN_PATH}" "$@" >"${out}" 2>"${err}"
  local code=$?
  set -e
  if [[ "${code}" -ne "${expected}" ]]; then
    printf "Case '%s' failed: expected exit %s, got %s\n" "${name}" "${expected}" "${code}" >&2
    printf "stdout:\n" >&2
    cat "${out}" >&2
    printf "stderr:\n" >&2
    cat "${err}" >&2
    exit 1
  fi
}

wait_for_http() {
  local url="$1"
  for _ in $(seq 1 100); do
    if curl --silent --show-error --fail --max-time 1 "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

printf "Building scoreboard binary...\n"
cd "${MODULE_DIR}"
go build -trimpath -buildvcs=false -o "${BIN_PATH}" ./cmd/main.go

printf "Running CLI cases...\n"
run_case "version" 0 -V
assert_contains "${WORK_DIR}/version.out" "Scorebot Scoreboard:"

run_case "defaults" 0 -d
assert_contains "${WORK_DIR}/defaults.out" "\"scorebot\""

run_case "missing-required" 2
assert_contains "${WORK_DIR}/missing-required.out" "Usage of scoreboard:"

run_case "invalid-flag" 2 -invalid-flag
assert_contains "${WORK_DIR}/invalid-flag.err" "flag provided but not defined"

run_case "missing-config" 1 -c "${WORK_DIR}/missing.json"
assert_contains "${WORK_DIR}/missing-config.err" "Error during startup:"

printf '{"scorebot":' >"${WORK_DIR}/invalid.json"
run_case "invalid-config-json" 1 -c "${WORK_DIR}/invalid.json"
assert_contains "${WORK_DIR}/invalid-config-json.err" "Error during startup:"

SCOREBOT_PORT="$(free_port)"
SCOREBOARD_PORT="$(free_port)"

cat >"${WORK_DIR}/mock_scorebot.py" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import sys


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/games":
            self._write_json(
                [{"id": 1, "name": "Game One", "mode": 0, "status": 1}]
            )
            return
        if self.path == "/api/games/1/scoreboard":
            self._write_json(
                {"name": "Game One", "mode": 0, "teams": [{"id": 1, "name": "Blue"}]}
            )
            return
        self.send_response(404)
        self.end_headers()

    def log_message(self, *_args):
        return

    def _write_json(self, payload):
        body = json.dumps(payload).encode("utf-8")
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)


if __name__ == "__main__":
    port = int(sys.argv[1])
    HTTPServer(("127.0.0.1", port), Handler).serve_forever()
PY

printf "Starting mock scorebot on port %s...\n" "${SCOREBOT_PORT}"
"${PYTHON_BIN}" "${WORK_DIR}/mock_scorebot.py" "${SCOREBOT_PORT}" >"${WORK_DIR}/mock.out" 2>"${WORK_DIR}/mock.err" &
SCOREBOT_PID=$!

sleep 0.2
if ! kill -0 "${SCOREBOT_PID}" 2>/dev/null; then
  printf "Mock scorebot failed to start\n" >&2
  cat "${WORK_DIR}/mock.err" >&2 || true
  exit 1
fi

printf "Starting scoreboard binary on port %s...\n" "${SCOREBOARD_PORT}"
"${BIN_PATH}" \
  -sbe "http://127.0.0.1:${SCOREBOT_PORT}" \
  -bind "127.0.0.1:${SCOREBOARD_PORT}" \
  -tick 1 \
  -timeout 2 \
  >"${WORK_DIR}/runtime.out" \
  2>"${WORK_DIR}/runtime.err" &
SCOREBOARD_PID=$!

if ! wait_for_http "http://127.0.0.1:${SCOREBOARD_PORT}/"; then
  printf "Scoreboard did not become ready\n" >&2
  cat "${WORK_DIR}/runtime.err" >&2 || true
  exit 1
fi

curl --silent --show-error --fail --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT}/" >"${WORK_DIR}/home.html"
curl --silent --show-error --fail --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT}/game/1" >"${WORK_DIR}/game.html"

if [[ ! -s "${WORK_DIR}/home.html" ]] || [[ ! -s "${WORK_DIR}/game.html" ]]; then
  printf "Expected non-empty HTTP responses from binary\n" >&2
  exit 1
fi

ws_status="$(curl --silent --output /dev/null --write-out "%{http_code}" --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT}/w")"
if [[ "${ws_status}" != "400" ]] && [[ "${ws_status}" != "426" ]]; then
  printf "Expected websocket endpoint to reject plain HTTP, got status %s\n" "${ws_status}" >&2
  exit 1
fi

kill -INT "${SCOREBOARD_PID}" || true
for _ in $(seq 1 50); do
  if ! kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
if kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
  kill -TERM "${SCOREBOARD_PID}" || true
fi
for _ in $(seq 1 30); do
  if ! kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
    break
  fi
  sleep 0.1
done
if kill -0 "${SCOREBOARD_PID}" 2>/dev/null; then
  kill -KILL "${SCOREBOARD_PID}" || true
fi
set +e
wait "${SCOREBOARD_PID}" 2>/dev/null
scoreboard_exit=$?
set -e
SCOREBOARD_PID=""
if [[ "${scoreboard_exit}" -ne 0 ]] && [[ "${scoreboard_exit}" -ne 130 ]] && [[ "${scoreboard_exit}" -ne 137 ]] && [[ "${scoreboard_exit}" -ne 143 ]]; then
  printf "Scoreboard runtime exited with unexpected code: %s\n" "${scoreboard_exit}" >&2
  cat "${WORK_DIR}/runtime.err" >&2 || true
  exit 1
fi

printf "Binary test suite passed.\n"
