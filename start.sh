#!/usr/bin/env bash
# start.sh - build-if-changed launcher for LLM Translator.
#
# Rebuilds the Go binaries only when backend sources change (fingerprint in
# .build-hash) and writes them to bin/ at the repo root. In development it
# exports TRANSLATOR_STATIC so the server serves the frontend from disk, which
# means frontend edits need no rebuild: just refresh the browser.
#
# Usage:
#   ./start.sh              # build if needed, run the web server
#   ./start.sh --cli        # build if needed, run the interactive CLI
#   ./start.sh --watch      # rebuild + restart the server when Go files change
#   ./start.sh --clean      # force a rebuild before starting
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_DIR="$ROOT/bin"
STATIC_DIR="$ROOT/internal/webui/static"
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

VERSION="$(git -C "$ROOT" describe --tags --always --dirty 2>/dev/null || echo dev)"
LDFLAGS="-s -w -X main.version=$VERSION"

hash_go_sources() {
  (
    cd "$ROOT"
    { find cmd internal -name '*.go' -print0; printf '%s\0' go.mod go.sum; } \
      | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}'
  )
}

hash_static() {
  (
    cd "$ROOT"
    find internal/webui/static -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | awk '{print $1}'
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
    echo "start.sh: building binaries into $BIN_DIR (version $VERSION)"
    mkdir -p "$BIN_DIR"
    ( cd "$ROOT" \
      && go build -ldflags "$LDFLAGS" -o "$BIN_DIR/llmt-server" ./cmd/server \
      && go build -ldflags "$LDFLAGS" -o "$BIN_DIR/llmt-cli"    ./cmd/cli )
    printf '%s\n' "$current" > "$HASH_FILE"
  fi

  local static_now static_prev=""
  static_now="$(hash_static)"
  [[ -f "$STATIC_HASH_FILE" ]] && static_prev="$(cat "$STATIC_HASH_FILE")"
  if [[ "$static_now" != "$static_prev" ]]; then
    echo "start.sh: frontend changed - served from disk, just refresh the browser"
    printf '%s\n' "$static_now" > "$STATIC_HASH_FILE"
  fi
}

run_target() {
  # Serve the frontend from disk in development (no rebuild on frontend edits).
  export TRANSLATOR_STATIC="$STATIC_DIR"
  cd "$ROOT"
  exec "$BIN_DIR/llmt-$TARGET"
}

if [[ "$WATCH" -eq 1 && "$TARGET" == "server" ]]; then
  echo "start.sh: watch mode (Ctrl-C to stop)"
  build_if_changed
  while true; do
    ( cd "$ROOT" && TRANSLATOR_STATIC="$STATIC_DIR" "$BIN_DIR/llmt-server" ) &
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
