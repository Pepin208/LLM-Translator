# AGENTS.md

## Repo shape
- Active code lives in `Port_Go/` (Go module `translate_llm`, Go 1.26). Compiled binaries live in `bin/` at the repo root. The repo root is the git root (branch `main`).
- `legacy/` is the frozen Python original: reference only, gitignored, not built or documented.

## Build / run
- `./start.sh` builds only when Go sources changed (fingerprint in `.build-hash`), then runs the server. Flags: `--cli`, `--watch`, `--clean`.
- Binaries are written to `bin/llmt-server` and `bin/llmt-cli`.
- Manual build: `cd Port_Go && go build -o ../bin/llmt-server ./cmd/server` (and `./cmd/cli`).
- `start.sh` deliberately runs the binary with CWD `Port_Go/`: `config.BASE_DIR` walks up from the CWD to the first `go.mod`, and `static/`, `web_storage/` and the config all hang off it. Running the binary from elsewhere resolves the wrong paths.
- Server binds `0.0.0.0:21346`; it uses HTTPS only if `cert.pem` + `key.pem` exist in `Port_Go/`.

## Test / verify
- `cd Port_Go && go test ./... && go vet ./...` (gofmt is the formatter; no Makefile/CI/lint config).
- Single test: `go test ./internal/engine -run TestCleanLLMResponse`.
- Tests are offline; engine tests use a mock provider. `real_file_test.go` skips when its sample upload is absent.
- `web_test.go` swaps `config.ConfigPath` via `withTempConfig`; reuse that helper for config-touching tests.

## Config & auth
- `translator_config.json` resolution: `$TRANSLATOR_CONFIG` → `Port_Go/translator_config.json` → parent dir. It is gitignored (API keys + auth material).
- Config is a generic map: unknown keys survive save; API keys are masked, and a masked value never overwrites the real one. `auth_secret`, `auth_token`, `auth_token_hash` are never exposed via the API.
- The access token is stored in plaintext (`auth_token`) so the server can reprint it: on every startup, and again in the terminal on a failed login (`login failed ... access_token=...`) so a new device can read it. Sessions last 7 days via an HMAC cookie.
- Put new service URLs/constants in `internal/config/endpoints.go`, not inline.

## Architecture
- Flow: `internal/web` (HTTP/SSE, chi) + `cmd/cli` → `internal/engine` (token estimation, sanitization, quality checks, batch/retry) → `internal/providers` (one file per provider protocol) → `internal/subtitle`/`internal/parser`.
- `internal/subtitle` is a custom SRT/ASS/SSA/VTT reader/writer kept for byte-level tag fidelity. Do NOT replace it with `go-astisub`: it normalizes away the inline tags and style names the masking pipeline must preserve.
- Tag masking/restoration is LIFO; providers see `<tN/>` placeholders that must round-trip. Preserve every tag exactly.
- OpenCode Zen/Go route per model family: `gpt-*`/`grok-*`/`muse-*` → `/responses`, `claude-*`/`qwen*` → `/messages`, `gemini-*` → `:generateContent`, else `/chat/completions`.
- The web server logs via `log/slog` (debug level) to stdout + `translator_log.txt`, one line per request (method, path, status, duration, ip, ua).

## Frontend
- `Port_Go/static/` is served from disk, not embedded: frontend changes need no rebuild, just a browser refresh. `start.sh` notes static changes but does not rebuild for them.
- PWA assets (`site.webmanifest`, `android-chrome-*.png`) and all favicons are referenced; do not delete.

## Gotcha
- When writing Go strings that must contain a bare tag like ` thinking`, the tool layer may strip it as markup. Build such literals with hex escapes (e.g. `"\x3cthink\x3e"`) and verify with `od -c` after editing parser/engine files.
