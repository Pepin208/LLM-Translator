# AGENTS.md

## Repo shape
- Go module `github.com/Pepin208/LLM-Translator` at the repo root (Go 1.26). Compiled binaries live in `bin/` (gitignored).
- `cmd/server` is the LAN web server, `cmd/cli` the interactive CLI.
- `legacy/` is the frozen Python original: reference only, gitignored, not built or documented.

## Build / run
- `./start.sh` builds only when Go sources changed (fingerprint `.build-hash`), then runs the server. Flags: `--cli`, `--watch`, `--clean`.
- Binaries go to `bin/llmt-server` and `bin/llmt-cli`; version is injected with `-ldflags "-X main.version=..."`.
- Manual build: `go build -o bin/llmt-server ./cmd/server` (and `./cmd/cli`).
- `start.sh` exports `TRANSLATOR_STATIC=$ROOT/internal/webui/static` in development, so the frontend is served from disk and needs no rebuild. A plain build uses the embedded frontend.
- `config.BASE_DIR` resolves `$TRANSLATOR_BASE` → nearest ancestor of the CWD with `go.mod` (dev) → directory of the executable (portable release). Config and `web_storage/` hang off it.

## Test / verify
- `go test ./... && go vet ./...`; `gofmt` is the formatter. CI runs gofmt, vet, test and build.
- Single test: `go test ./internal/engine -run TestCleanLLMResponse`.
- Tests are offline; engine tests use a mock provider. `real_file_test.go` skips when its sample upload is absent.
- `web_test.go` swaps `config.ConfigPath` via `withTempConfig`; reuse that helper for config-touching tests.

## Config, auth & TLS
- `translator_config.json` resolution: `$TRANSLATOR_CONFIG` → `<base>/translator_config.json` → parent of `<base>`. It is gitignored (API keys + auth material).
- Config is a generic map: unknown keys survive save; API keys are masked, and a masked value never overwrites the real one. `auth_secret`, `auth_token`, `auth_token_hash` are never exposed via the API.
- The access token is stored in plaintext (`auth_token`) so the server can reprint it: on every startup, and again in the terminal on a failed login (`login failed ... access_token=...`) so a new device can read it. Sessions last 7 days via an HMAC cookie.
- TLS is on by default; `web.EnsureTLSCert` generates a self-signed cert with SANs (localhost, hostname, all local IPs) when `cert.pem`/`key.pem` are missing. `--http` disables TLS. `Server.SetTLS` controls the `Secure` cookie flag, so keep it in sync with how you serve.
- Service URLs/constants live in `internal/config/endpoints.go`; `PORT` and `OPENROUTER_REFERER` override the defaults there.

## Architecture
- Flow: `internal/web` (HTTP/SSE, chi) + `cmd/cli` → `internal/engine` (token estimation, sanitization, quality checks, batch/retry) → `internal/providers` (one file per provider protocol) → `internal/subtitle`/`internal/parser`.
- `internal/webui` embeds the frontend (`internal/webui/static`). `internal/web` prefers `$TRANSLATOR_STATIC` on disk, else the embedded FS.
- `internal/subtitle` is a custom SRT/ASS/SSA/VTT reader/writer kept for byte-level tag fidelity. Do NOT replace it with `go-astisub`: it normalizes away the inline tags and style names the masking pipeline must preserve.
- Tag masking/restoration is LIFO; providers see `<tN/>` placeholders that must round-trip. Preserve every tag exactly.
- OpenCode Zen/Go route per model family: `gpt-*`/`grok-*`/`muse-*` → `/responses`, `claude-*`/`qwen*` → `/messages`, `gemini-*` → `:generateContent`, else `/chat/completions`.
- The web server logs via `log/slog` (debug level) to stdout + `translator_log.txt`, one line per request (method, path, status, duration, ip, ua).

## Frontend
- `internal/webui/static/` is embedded at build time; in development `TRANSLATOR_STATIC` points the server at the same directory on disk, so edits only need a browser refresh.
- PWA assets (`site.webmanifest`, `android-chrome-*.png`) and all favicons are referenced; do not delete.

## Gotcha
- When writing Go strings that must contain a bare tag like ` thinking`, the tool layer may strip it as markup. Build such literals with hex escapes (e.g. `"\x3cthink\x3e"`) and verify with `od -c` after editing parser/engine files.
