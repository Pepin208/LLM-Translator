# LLM Translator

[![CI](https://github.com/Pepin208/LLM-Translator/actions/workflows/ci.yml/badge.svg)](https://github.com/Pepin208/LLM-Translator/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://github.com/Pepin208/LLM-Translator/blob/main/LICENSE)

A local LLM-powered subtitle translator: a LAN web app and an interactive CLI
that translate SRT/ASS/VTT subtitles while preserving every inline tag and
style.

- Web UI + REST/SSE API (`cmd/server`) and an interactive CLI (`cmd/cli`).
- Providers: OpenRouter, OpenAI, DeepSeek, Anthropic, Google Gemini, Local
  (OpenAI-compatible), OpenCode Zen and OpenCode Go.
- Tag-preserving subtitle reader/writer (no `go-astisub` normalization).
- Per-series glossaries, retry/quality pipeline, prompt caching, cost
  projections and balance tracking.
- Single self-contained binary: the frontend is embedded, so the built server
  runs anywhere with no extra files.

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

The web server prints an access token on startup; open the LAN URL it shows and
paste the token into the login overlay.

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
| `OPENROUTER_API_KEY`, `OPENAI_API_KEY`, `ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, `GOOGLE_GEMINI_API_KEY`, `OPENCODE_API_KEY` | Provider API keys (override the config file; both OpenCode gateways use `OPENCODE_API_KEY`) |

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

## Security

This is a single-user LAN tool, not a hardened multi-tenant service.

- Access is gated by an access token exchanged for a signed session cookie
  (`HttpOnly`, `SameSite=Strict`). The server prints the token on startup and,
  on a failed login, reprints it on its own terminal only, never into the
  shared log or the SSE stream.
- TLS is self-signed by default (see above). Use `--http` only on a trusted
  network.
- An authenticated session can upload and download subtitle files and read or
  update non-secret configuration. The download endpoint only serves files from
  the upload/output directories and refuses `translator_config.json`,
  `cert.pem`, `key.pem`, the server log and temp files.

## Providers

Pick any provider in the web UI or the CLI: OpenRouter, OpenAI, Anthropic,
DeepSeek, Google Gemini, a local OpenAI-compatible server, and the OpenCode
gateways. Each provider reads its API key from an environment variable (see
above) or from `translator_config.json`.

## Layout

```
cmd/server          Web/LAN server (port 21346)
cmd/cli             Interactive CLI (cobra + huh)
internal/config     Constants, endpoints, pricing, session, config I/O
internal/utils      NFKD normalization, range parsing
internal/subtitle   Minimal SRT/ASS/VTT reader+writer (tag preserving)
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

## Contributing

This project was built with AI assistance, so there may be bugs and rough
edges, small or large. Contributions are welcome: testing with other providers
and models, corrections and improvements. Open an issue or a pull request.

## License

[MIT](LICENSE) © 2026 Pepin208
