# LLM Translator

A local LLM-powered subtitle translator: a LAN web app and an interactive CLI
that translate SRT/ASS/SSA/VTT subtitles while preserving every inline tag and
style. Ported from an earlier Python implementation (kept under `legacy/` for
reference only).

- Web UI + REST/SSE API (`cmd/server`) and an interactive CLI (`cmd/cli`).
- Providers: OpenRouter, OpenAI, DeepSeek, Anthropic, Google Gemini, Local
  (OpenAI-compatible), OpenCode Zen and OpenCode Go.
- Tag-preserving subtitle reader/writer (no `go-astisub` normalization).
- Per-series glossaries, retry/quality pipeline, prompt caching, cost
  projections and balance tracking.
- Single self-contained binary: the frontend is embedded, so a release build
  runs anywhere with no extra files.

## Download

Grab a prebuilt binary for Linux, macOS or Windows from
[Releases](https://github.com/Pepin208/LLM-Translator/releases), then run it:

```bash
./llmt-server          # web server
./llmt-server --help
```

The server prints an access token on startup; open the LAN URL it shows and
paste the token into the login overlay.

## Build from source

Requires Go 1.26+.

```bash
./start.sh             # build if changed, run the web server
./start.sh --cli       # build if changed, run the interactive CLI
./start.sh --watch     # rebuild + restart on Go changes
./start.sh --clean     # force a rebuild
```

Binaries are written to `bin/`. `start.sh` serves the frontend from
`internal/webui/static` during development, so frontend edits need no rebuild —
just refresh the browser. A plain build embeds the frontend instead:

```bash
go build -o bin/llmt-server ./cmd/server
go build -o bin/llmt-cli    ./cmd/cli
```

## Configuration

The server reads `translator_config.json` (holds API keys and auth material),
resolved in this order:

1. `$TRANSLATOR_CONFIG`
2. `<base>/translator_config.json`
3. the parent directory of `<base>`

`<base>` is the directory holding configuration and data. It resolves to
`$TRANSLATOR_BASE`, else the nearest ancestor of the working directory with a
`go.mod` (development), else the directory of the binary (portable release).

The live config is gitignored. Unknown keys are preserved on save; API keys are
masked in `GET /api/config` and never overwritten by a masked submission.

### Environment variables

| Variable | Purpose |
|---|---|
| `TRANSLATOR_BASE` | Base directory for config and data |
| `TRANSLATOR_CONFIG` | Explicit path to `translator_config.json` |
| `TRANSLATOR_STATIC` | Serve the frontend from this directory instead of the embedded copy |
| `PORT` | Listen port (default `21346`) |
| `OPENROUTER_REFERER` | Referer sent to OpenRouter |
| `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, `GEMINI_API_KEY`, `OPENCODE_API_KEY` | Provider keys (override the config file) |

See `.env.example`.

## Server flags

| Flag | Effect |
|---|---|
| `--http` | Serve plain HTTP (no TLS) |
| `--version`, `-v` | Print version and exit |
| `--help`, `-h` | Show help |

TLS is on by default: on first run the server generates a self-signed
certificate (`cert.pem`/`key.pem`) with SANs for `localhost`, the hostname and
every local IP. Install `cert.pem` as a trusted CA on your devices to avoid
browser warnings; use `--http` to disable TLS entirely.

## Providers

OpenRouter, OpenAI, DeepSeek, Anthropic, Google Gemini, Local, and the OpenCode
gateways:

- `OpenCode Zen` → `https://opencode.ai/zen/v1`
- `OpenCode Go`  → `https://opencode.ai/zen/go/v1`

Both use `opencode_api_key` (or `OPENCODE_API_KEY`). Requests are routed per
model family: `gpt-*`/`grok-*`/`muse-*` → `/responses`, `claude-*`/`qwen*` →
`/messages`, `gemini-*` → `:generateContent`, everything else
→ `/chat/completions`.

## Layout

```
cmd/server          Web/LAN server (port 21346)
cmd/cli             Interactive CLI (cobra + huh)
internal/config     Constants, endpoints, pricing, session, config I/O
internal/utils      NFKD normalization, range parsing
internal/subtitle   Minimal SRT/ASS/SSA/VTT reader+writer (tag preserving)
internal/parser     Tag masking/restoration (LIFO) and proper-name extraction
internal/providers  One file per provider protocol
internal/engine     Token estimation, quality checks, batch pipeline, retries
internal/finance    Balance, currency conversion, projections
internal/web        HTTP server, token auth, SSE, upload/download, logging
internal/webui      Embedded browser frontend
```

## Development

```bash
go test ./...
go vet ./...
go test ./internal/engine -run TestCleanLLMResponse   # single test
```

## License

[MIT](LICENSE) © 2026 Pepin208
