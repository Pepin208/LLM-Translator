# LLM Subtitle Translator — Go port

A Go reimplementation of the Python backend (web server + CLI). It shares the
same `translator_config.json`, `static/` frontend and file formats, so it is
drop-in compatible with the Python version.

## Layout

```
cmd/server        Web/LAN server (default port 21346)
cmd/cli           Interactive CLI (cobra + huh)
internal/config   Constants, pricing, TranslationSession, config/cache I/O
internal/utils    NFKD normalization, range parsing
internal/subtitle Minimal SRT/ASS/SSA/VTT reader+writer (tag preserving)
internal/parser   Tag masking/restoration (LIFO) and proper-name extraction
internal/providers OpenRouter, OpenAI, DeepSeek, Anthropic, Gemini, Local,
                    OpenCode Zen, OpenCode Go (multi-protocol routing)
internal/engine   Token estimation, quality checks, batch pipeline, retries
internal/finance  Balance, currency conversion, projections
internal/web      HTTP server, token auth (HMAC cookie), SSE, upload/download
```

## Build and run

```bash
cd Port_Go
go build -o bin/llmt-server ./cmd/server
go build -o bin/llmt-cli    ./cmd/cli

./bin/llmt-server     # web server
./bin/llmt-cli        # interactive CLI
```

The web server listens on `https://0.0.0.0:21346` (HTTP if no cert/key).
On first run it prints a one-time access token:

```
🔐 NEW ACCESS TOKEN (save it now, it will not be shown again):
      <token>
```

Paste it into the login overlay. Sessions last 7 days. Log out from the top
bar. The token hash is shared with the Python version (same config file), so
the same token works for both.

## Configuration

`translator_config.json` is resolved in this order:

1. `$TRANSLATOR_CONFIG`
2. the project root one level above `Port_Go/` (shared with Python)
3. `Port_Go/translator_config.json`

Unknown keys are preserved on save. API keys are masked in `GET /api/config`
and never overwritten by a masked submission.

## Providers

OpenRouter, OpenAI, DeepSeek, Anthropic, Google Gemini, Local, and the OpenCode
gateways:

- `OpenCode Zen` → `https://opencode.ai/zen/v1`
- `OpenCode Go`  → `https://opencode.ai/zen/go/v1`

Both use `opencode_api_key` from config (or `OPENCODE_API_KEY`). Requests are
routed per model family: `gpt-*`/`grok-*`/`muse-*` → `/responses`,
`claude-*`/`qwen*` → `/messages`, `gemini-*` → `/models/{id}:generateContent`,
everything else → `/chat/completions`.

## Notes

- `go-astisub` was evaluated but not used: it normalizes away the inline tags
  and style names that the masking pipeline must preserve. The custom reader in
  `internal/subtitle` guarantees byte-level tag fidelity.
- `sys.stdout` juggling from Python is gone: progress output goes through an
  `io.Writer` on the session, and SSE uses a channel broker.

## Test

```bash
go test ./...
go vet ./...
```
