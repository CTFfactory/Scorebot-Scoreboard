#!/usr/bin/env bash

set -euo pipefail

MODULE_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK_DIR="$(mktemp -d "${TMPDIR:-/tmp}/scoreboard-binary-test.XXXXXX")"
BIN_PATH="${WORK_DIR}/scoreboard"
SCOREBOT_PID=""
SCOREBOARD_PID=""
PYTHON_BIN="$(command -v python3 || command -v python || true)"
MOCK_METRICS_URL=""

if [[ -z "${PYTHON_BIN}" ]]; then
  printf "python3 or python is required for test-binary.sh\n" >&2
  exit 1
fi

export GOTOOLCHAIN="${GOTOOLCHAIN:-local}"

stop_pid() {
  local pid="$1"
  if [[ -z "${pid}" ]] || ! kill -0 "${pid}" 2>/dev/null; then
    return
  fi
  kill -TERM "${pid}" || true
  for _ in $(seq 1 30); do
    if ! kill -0 "${pid}" 2>/dev/null; then
      break
    fi
    sleep 0.1
  done
  if kill -0 "${pid}" 2>/dev/null; then
    kill -KILL "${pid}" || true
  fi
  wait "${pid}" 2>/dev/null || true
}

cleanup() {
  stop_pid "${SCOREBOARD_PID}"
  stop_pid "${SCOREBOT_PID}"
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
  if ! grep -Fq "${needle}" "${file}"; then
    printf "Expected %s to contain: %s\n" "${file}" "${needle}" >&2
    printf "File contents:\n" >&2
    cat "${file}" >&2
    exit 1
  fi
}

assert_not_contains() {
  local file="$1"
  local needle="$2"
  if grep -Fq "${needle}" "${file}"; then
    printf "Expected %s to not contain: %s\n" "${file}" "${needle}" >&2
    printf "File contents:\n" >&2
    cat "${file}" >&2
    exit 1
  fi
}

assert_count() {
  local file="$1"
  local needle="$2"
  local expected="$3"
  local actual
  actual="$( (grep -oF "${needle}" "${file}" || true) | wc -l | tr -d '[:space:]' )"
  if [[ "${actual}" != "${expected}" ]]; then
    printf "Expected %s to contain %s instance(s) of '%s', got %s\n" "${file}" "${expected}" "${needle}" "${actual}" >&2
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
  shift
  for _ in $(seq 1 100); do
    if curl --silent --show-error --fail --max-time 1 "$@" "${url}" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

wait_for_contains() {
  local url="$1"
  local needle="$2"
  local out="$3"
  shift 3
  for _ in $(seq 1 120); do
    if curl --silent --show-error --fail --max-time 1 "$@" "${url}" >"${out}" 2>/dev/null; then
      if grep -Fq "${needle}" "${out}"; then
        return 0
      fi
    fi
    sleep 0.1
  done
  return 1
}

http_status() {
  local url="$1"
  shift
  curl --silent --show-error --output /dev/null --write-out "%{http_code}" --max-time 5 "$@" "${url}"
}

write_mock_scorebot() {
  cat >"${WORK_DIR}/mock_scorebot.py" <<'PY'
from http.server import BaseHTTPRequestHandler, HTTPServer
import json
import threading
import sys

COUNTS = {}
LOCK = threading.Lock()


def hit(path):
    with LOCK:
        COUNTS[path] = COUNTS.get(path, 0) + 1


class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        hit(self.path)
        if self.path == "/api/games":
            self._write_json(
                [
                    {"id": 1, "name": "Game One", "mode": 0, "status": 1},
                    {"id": 2, "name": "Game Two Hidden", "mode": 0, "status": 3},
                ]
            )
            return
        if self.path == "/api/games/1/scoreboard":
            self._write_json(
                {
                    "name": "Game One",
                    "mode": 0,
                    "credit": "Demo Credit",
                    "message": "Demo Message",
                    "teams": [
                        {
                            "id": 1,
                            "name": "Blue",
                            "logo": "/logo-b.png",
                            "color": "#3366ff",
                            "minimal": False,
                            "offense": False,
                            "beacons": [],
                            "hosts": [],
                            "score": {"total": 100, "health": 99},
                            "flags": {"open": 1, "lost": 0, "captured": 2},
                            "tickets": {"open": 3, "closed": 4},
                        }
                    ],
                    "events": [],
                }
            )
            return
        if self.path == "/metrics":
            with LOCK:
                snapshot = dict(COUNTS)
            self._write_json(snapshot)
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
}

wait_for_mock_count() {
  local endpoint="$1"
  local minimum="$2"
  for _ in $(seq 1 120); do
    local count
    count="$("${PYTHON_BIN}" - "${MOCK_METRICS_URL}" "${endpoint}" <<'PY' 2>/dev/null || true
import json
import sys
import urllib.request

url = sys.argv[1]
key = sys.argv[2]
with urllib.request.urlopen(url, timeout=1) as r:
    data = json.load(r)
print(int(data.get(key, 0)))
PY
)"
    if [[ -n "${count}" ]] && [[ "${count}" =~ ^[0-9]+$ ]] && (( count >= minimum )); then
      return 0
    fi
    sleep 0.1
  done
  return 1
}

start_mock_scorebot() {
  local port="$1"
  MOCK_METRICS_URL="http://127.0.0.1:${port}/metrics"
  "${PYTHON_BIN}" "${WORK_DIR}/mock_scorebot.py" "${port}" >"${WORK_DIR}/mock.out" 2>"${WORK_DIR}/mock.err" &
  SCOREBOT_PID=$!
  if ! wait_for_http "http://127.0.0.1:${port}/api/games"; then
    printf "Mock scorebot failed to start\n" >&2
    cat "${WORK_DIR}/mock.err" >&2 || true
    exit 1
  fi
}

start_scoreboard() {
  local out="$1"
  local err="$2"
  shift 2
  "${BIN_PATH}" "$@" >"${out}" 2>"${err}" &
  SCOREBOARD_PID=$!
}

stop_scoreboard() {
  stop_pid "${SCOREBOARD_PID}"
  SCOREBOARD_PID=""
}

prepare_ws_probe() {
  cat >"${WORK_DIR}/ws_probe.go" <<'GO'
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gorilla/websocket"
)

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: ws-probe <ws-url> <origin> <game-id> [expected-substring]")
		os.Exit(2)
	}
	wsURL := os.Args[1]
	origin := os.Args[2]
	gameID, err := strconv.ParseUint(os.Args[3], 10, 64)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid game id: %v\n", err)
		os.Exit(1)
	}
	expected := ""
	if len(os.Args) > 4 {
		expected = os.Args[4]
	}

	headers := make(http.Header)
	if len(origin) > 0 {
		headers.Set("Origin", origin)
	}
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, headers)
	if err != nil {
		if resp != nil {
			fmt.Fprintf(os.Stderr, "websocket dial failed with HTTP status %d\n", resp.StatusCode)
		}
		fmt.Fprintf(os.Stderr, "websocket dial failed: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	hello, _ := json.Marshal(map[string]uint64{"game": gameID})
	_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
	if err := conn.WriteMessage(websocket.TextMessage, hello); err != nil {
		fmt.Fprintf(os.Stderr, "write hello failed: %v\n", err)
		os.Exit(1)
	}
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, message, err := conn.ReadMessage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "read message failed: %v\n", err)
		os.Exit(1)
	}
	if !json.Valid(message) {
		fmt.Fprintln(os.Stderr, "received invalid JSON payload")
		os.Exit(1)
	}
	if len(expected) > 0 && !bytes.Contains(message, []byte(expected)) {
		fmt.Fprintf(os.Stderr, "expected websocket payload to contain %q\n", expected)
		fmt.Fprintln(os.Stderr, string(message))
		os.Exit(1)
	}
	_, _ = os.Stdout.Write(message)
}
GO
  cd "${MODULE_DIR}"
  go build -trimpath -buildvcs=false -o "${WORK_DIR}/ws-probe" "${WORK_DIR}/ws_probe.go"
}

prepare_certgen() {
  cat >"${WORK_DIR}/certgen.go" <<'GO'
package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"time"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: certgen <cert-path> <key-path>")
		os.Exit(2)
	}
	certPath, keyPath := os.Args[1], os.Args[2]
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	tpl := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: "127.0.0.1",
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		panic(err)
	}
	cf, err := os.Create(certPath)
	if err != nil {
		panic(err)
	}
	if err := pem.Encode(cf, &pem.Block{Type: "CERTIFICATE", Bytes: der}); err != nil {
		panic(err)
	}
	_ = cf.Close()

	kf, err := os.Create(keyPath)
	if err != nil {
		panic(err)
	}
	if err := pem.Encode(kf, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(priv)}); err != nil {
		panic(err)
	}
	_ = kf.Close()
}
GO
}

printf "Building scoreboard binary...\n"
cd "${MODULE_DIR}"
go build -trimpath -buildvcs=false -o "${BIN_PATH}" ./cmd/main.go

printf "Running CLI cases...\n"
run_case "version" 0 -V
assert_contains "${WORK_DIR}/version.out" "Scorebot Scoreboard:"

run_case "defaults" 0 -d
assert_contains "${WORK_DIR}/defaults.out" "\"scorebot\""

run_case "help-short" 2 -h
assert_contains "${WORK_DIR}/help-short.out" "Usage of scoreboard:"
assert_count "${WORK_DIR}/help-short.out" "Usage of scoreboard:" 1

run_case "help-long" 2 --help
assert_contains "${WORK_DIR}/help-long.out" "Usage of scoreboard:"
assert_count "${WORK_DIR}/help-long.out" "Usage of scoreboard:" 1

run_case "missing-required" 2
assert_contains "${WORK_DIR}/missing-required.out" "Usage of scoreboard:"
assert_count "${WORK_DIR}/missing-required.out" "Usage of scoreboard:" 1

run_case "invalid-flag" 2 -invalid-flag
assert_contains "${WORK_DIR}/invalid-flag.err" "flag provided but not defined"
assert_count "${WORK_DIR}/invalid-flag.out" "Usage of scoreboard:" 1

run_case "missing-config" 1 -c "${WORK_DIR}/missing.json"
assert_contains "${WORK_DIR}/missing-config.err" "Error during startup:"

printf '{"scorebot":' >"${WORK_DIR}/invalid.json"
run_case "invalid-config-json" 1 -c "${WORK_DIR}/invalid.json"
assert_contains "${WORK_DIR}/invalid-config-json.err" "Error during startup:"

printf "Preparing runtime helpers...\n"
write_mock_scorebot
prepare_ws_probe

SCOREBOT_PORT="$(free_port)"
printf "Starting mock scorebot on port %s...\n" "${SCOREBOT_PORT}"
start_mock_scorebot "${SCOREBOT_PORT}"

printf "Running runtime checks with direct CLI flags...\n"
SCOREBOARD_PORT="$(free_port)"

printf "Starting scoreboard binary on port %s...\n" "${SCOREBOARD_PORT}"
start_scoreboard "${WORK_DIR}/runtime.out" "${WORK_DIR}/runtime.err" \
  -sbe "http://127.0.0.1:${SCOREBOT_PORT}" \
  -bind "127.0.0.1:${SCOREBOARD_PORT}" \
  -tick 1 \
  -timeout 2 \
  -assets "https://assets.example"

if ! wait_for_http "http://127.0.0.1:${SCOREBOARD_PORT}/"; then
  printf "Scoreboard did not become ready\n" >&2
  cat "${WORK_DIR}/runtime.err" >&2 || true
  exit 1
fi

if ! wait_for_contains "http://127.0.0.1:${SCOREBOARD_PORT}/" "Game One" "${WORK_DIR}/home.html"; then
  printf "Home page did not render synced game data\n" >&2
  cat "${WORK_DIR}/runtime.err" >&2 || true
  exit 1
fi
assert_not_contains "${WORK_DIR}/home.html" "Game Two Hidden"
assert_contains "${WORK_DIR}/home.html" "/game/1/"

curl --silent --show-error --fail --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT}/game/1" >"${WORK_DIR}/game.html"
assert_contains "${WORK_DIR}/game.html" "const game = 1;"

curl --silent --show-error --fail --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT}/script/scoreboard.js" >"${WORK_DIR}/scoreboard.js"
assert_contains "${WORK_DIR}/scoreboard.js" "Initializing Skynet..."

logo_status="$(http_status "http://127.0.0.1:${SCOREBOARD_PORT}/image/logo.png")"
if [[ "${logo_status}" != "200" ]]; then
  printf "Expected logo asset to return 200, got %s\n" "${logo_status}" >&2
  exit 1
fi

post_status="$(http_status "http://127.0.0.1:${SCOREBOARD_PORT}/" -X POST)"
if [[ "${post_status}" != "405" ]]; then
  printf "Expected POST / to return 405, got %s\n" "${post_status}" >&2
  exit 1
fi

fallback_status="$(http_status "http://127.0.0.1:${SCOREBOARD_PORT}/game/not-a-number")"
if [[ "${fallback_status}" != "404" ]]; then
  printf "Expected non-game path fallback to return 404, got %s\n" "${fallback_status}" >&2
  exit 1
fi

ws_status="$(curl --silent --output /dev/null --write-out "%{http_code}" --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT}/w")"
if [[ "${ws_status}" != "400" ]] && [[ "${ws_status}" != "426" ]]; then
  printf "Expected websocket endpoint to reject plain HTTP, got status %s\n" "${ws_status}" >&2
  exit 1
fi

printf "Running websocket hello and update checks...\n"
"${WORK_DIR}/ws-probe" \
  "ws://127.0.0.1:${SCOREBOARD_PORT}/w" \
  "http://127.0.0.1:${SCOREBOARD_PORT}" \
  "1" \
  "https://assets.example/logo-b.png" \
  >"${WORK_DIR}/ws.json"
assert_contains "${WORK_DIR}/ws.json" "game-name"
assert_contains "${WORK_DIR}/ws.json" "https://assets.example/logo-b.png"

set +e
"${WORK_DIR}/ws-probe" \
  "ws://127.0.0.1:${SCOREBOARD_PORT}/w" \
  "http://invalid-origin.example" \
  "1" \
  >"${WORK_DIR}/ws-bad-origin.out" 2>"${WORK_DIR}/ws-bad-origin.err"
ws_bad_origin_code=$?
set -e
if [[ "${ws_bad_origin_code}" -eq 0 ]]; then
  printf "Expected websocket dial with invalid Origin to fail\n" >&2
  cat "${WORK_DIR}/ws-bad-origin.out" >&2 || true
  exit 1
fi

if ! wait_for_mock_count "/api/games" 1; then
  printf "Expected mock /api/games to be called at least once\n" >&2
  exit 1
fi
if ! wait_for_mock_count "/api/games/1/scoreboard" 1; then
  printf "Expected mock /api/games/1/scoreboard to be called at least once\n" >&2
  exit 1
fi

stop_scoreboard

printf "Running config file and directory override checks...\n"
OVERRIDE_DIR="${WORK_DIR}/override"
mkdir -p "${OVERRIDE_DIR}/public" "${OVERRIDE_DIR}/template"
printf "OVERRIDE-ASSET\n" >"${OVERRIDE_DIR}/public/override.txt"
cat >"${OVERRIDE_DIR}/template/home.html" <<'HTML'
OVERRIDE HOME {{range .}}{{.Name}} {{end}}
HTML
cat >"${OVERRIDE_DIR}/template/scoreboard.html" <<'HTML'
OVERRIDE SCORE {{.Game}}
HTML

SCOREBOARD_PORT_CFG="$(free_port)"
cat >"${WORK_DIR}/scoreboard-config.json" <<EOF
{
  "scorebot": "http://127.0.0.1:${SCOREBOT_PORT}",
  "listen": "127.0.0.1:${SCOREBOARD_PORT_CFG}",
  "tick": 1,
  "timeout": 2,
  "assets": "https://assets.example",
  "dir": "${OVERRIDE_DIR}"
}
EOF

start_scoreboard "${WORK_DIR}/runtime-config.out" "${WORK_DIR}/runtime-config.err" -c "${WORK_DIR}/scoreboard-config.json"
if ! wait_for_contains "http://127.0.0.1:${SCOREBOARD_PORT_CFG}/" "OVERRIDE HOME" "${WORK_DIR}/home-override.txt"; then
  printf "Config-driven scoreboard did not become ready with override templates\n" >&2
  cat "${WORK_DIR}/runtime-config.err" >&2 || true
  exit 1
fi
if ! wait_for_contains "http://127.0.0.1:${SCOREBOARD_PORT_CFG}/" "Game One" "${WORK_DIR}/home-override.txt"; then
  printf "Config-driven scoreboard home did not render synced game list\n" >&2
  cat "${WORK_DIR}/runtime-config.err" >&2 || true
  exit 1
fi

curl --silent --show-error --fail --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT_CFG}/game/1" >"${WORK_DIR}/game-override.txt"
assert_contains "${WORK_DIR}/game-override.txt" "OVERRIDE SCORE 1"

curl --silent --show-error --fail --max-time 5 "http://127.0.0.1:${SCOREBOARD_PORT_CFG}/override.txt" >"${WORK_DIR}/override-asset.txt"
assert_contains "${WORK_DIR}/override-asset.txt" "OVERRIDE-ASSET"

stop_scoreboard

printf "Running TLS startup checks...\n"
prepare_certgen
cd "${MODULE_DIR}"
go run "${WORK_DIR}/certgen.go" "${WORK_DIR}/cert.pem" "${WORK_DIR}/key.pem"

SCOREBOARD_PORT_TLS="$(free_port)"
start_scoreboard "${WORK_DIR}/runtime-tls.out" "${WORK_DIR}/runtime-tls.err" \
  -sbe "http://127.0.0.1:${SCOREBOT_PORT}" \
  -bind "127.0.0.1:${SCOREBOARD_PORT_TLS}" \
  -tick 1 \
  -timeout 2 \
  -cert "${WORK_DIR}/cert.pem" \
  -key "${WORK_DIR}/key.pem"

if ! wait_for_http "https://127.0.0.1:${SCOREBOARD_PORT_TLS}/" -k; then
  printf "TLS scoreboard did not become ready\n" >&2
  cat "${WORK_DIR}/runtime-tls.err" >&2 || true
  exit 1
fi
if ! wait_for_contains "https://127.0.0.1:${SCOREBOARD_PORT_TLS}/" "Game One" "${WORK_DIR}/home-tls.html" -k; then
  printf "TLS home page did not render expected content\n" >&2
  cat "${WORK_DIR}/runtime-tls.err" >&2 || true
  exit 1
fi

stop_scoreboard

printf "Binary test suite passed.\n"
