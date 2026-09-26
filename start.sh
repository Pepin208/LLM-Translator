#!/usr/bin/env bash
# start.sh - build-if-changed launcher for LLM Subtitle Translator.
#
# The Go backend is rebuilt only when its sources change (fingerprint stored in
# .build-hash). The frontend under Port_Go/static is served from disk, so a
# change there needs no rebuild: the script only notices and tells you to
# refresh the browser.
#
# Usage:
#   ./start.sh              # build if needed, run the web server
#   ./start.sh --cli        # build if needed, run the interactive CLI
#   ./start.sh --watch      # rebuild + restart the server when Go files change
#   ./start.sh --clean      # force a rebuild before starting
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
GO_DIR="$ROOT/Port_Go"
BIN_DIR="$ROOT/bin"
HASH_FILE="$ROOT/.build-hash"
STATIC_HASH_FILE="$ROOT/.build-hash-static"

TARGET="server"
WATCH=0
CLEAN=0
for arg in "$@"; do
  case "$arg" in
    --cli) TARGET="cli" ;;
    --server) TARGET="server" ;;
    --watch) WATCH=1 ;;
    --clean) CLEAN=1 ;;
    -h|--help) sed -n '2,15p' "$0"; exit 0 ;;
    *) echo "start.sh: unknown argument '$arg' (try --help)" >&2; exit 2 ;;
  esac
done

hash_go_sources() {
  (
    cd "$GO_DIR"
    { find cmd internal -name '*.go' -print0; printf '%s\0' go.mod go.sum; } \
      | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}'
  )
}

hash_static() {
  (
    cd "$GO_DIR"
    find static -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}'
  )
}

build_if_changed() {
  local current previous=""
  current="$(hash_go_sources)"
  [[ -f "$HASH_FILE" ]] && previous="$(cat "$HASH_FILE")"

  if [[ "$CLEAN" -eq 0 && "$current" == "$previous" \
        && -x "$BIN_DIR/llmt-server" && -x "$BIN_DIR/llmt-cli" ]]; then
    echo "start.sh: backend unchanged - skipping build"
  else
    echo "start.sh: building binaries into $BIN_DIR"
    mkdir -p "$BIN_DIR"
    ( cd "$GO_DIR" \
      && go build -o "$BIN_DIR/llmt-server" ./cmd/server \
      && go build -o "$BIN_DIR/llmt-cli"    ./cmd/cli )
    printf '%s\n' "$current" > "$HASH_FILE"
  fi

  local static_now static_prev=""
  static_now="$(hash_static)"
  [[ -f "$STATIC_HASH_FILE" ]] && static_prev="$(cat "$STATIC_HASH_FILE")"
  if [[ "$static_now" != "$static_prev" ]]; then
    echo "start.sh: frontend changed - no rebuild needed, just refresh the browser"
    printf '%s\n' "$static_now" > "$STATIC_HASH_FILE"
  fi
}

run_target() {
  # Keep the CWD inside Port_Go so config.BASE_DIR (which walks up to go.mod)
  # resolves static/, web_storage/ and translator_config.json correctly.
  cd "$GO_DIR"
  exec "$BIN_DIR/llmt-$TARGET"
}

if [[ "$WATCH" -eq 1 && "$TARGET" == "server" ]]; then
  echo "start.sh: watch mode (Ctrl-C to stop)"
  build_if_changed
  while true; do
    ( cd "$GO_DIR" && "$BIN_DIR/llmt-server" ) &
    child=$!
    while kill -0 "$child" 2>/dev/null; do
      sleep 2
      if [[ "$(hash_go_sources)" != "$(cat "$HASH_FILE")" ]]; then
        echo "start.sh: Go sources changed - rebuilding and restarting"
        kill "$child" 2>/dev/null || true
        wait "$child" 2>/dev/null || true
        break
      fi
    done
    wait "$child" 2>/dev/null || true
    build_if_changed
  done
fi

build_if_changed
run_target
