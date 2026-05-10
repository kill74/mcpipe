# mcpipe

`mcpipe` is a Go CLI for running secure, inspectable LLM pipelines from JSON.

It gives you a practical way to describe a workflow as data: validate inputs, render prompts, call LLMs, use MCP tools, pass outputs between steps, run dependency graphs in order, and keep the whole thing auditable.

The short version: **JSON pipelines in, reproducible LLM/MCP runs out.**

## Why It Exists

LLM automation gets risky fast when prompts, tools, secrets, file writes, retries, and model providers are scattered through scripts. `mcpipe` puts those concerns in one explicit pipeline file and gives you the operational tools around it:

- strict validation before runtime
- dry-run previews before spending tokens or calling tools
- deterministic mock mode for CI and local development
- real Ollama and Anthropic adapters
- real MCP stdio tool execution
- dependency-aware step orchestration
- secure file-write sandboxing
- redacted audit logs
- lockfiles and signatures for trusted automation
- graphs, explanations, doctor checks, and JSON output for humans and machines

## Status

`mcpipe` is an executable MVP with a strong security baseline. The schema already accepts some forward-compatible fields, but the v1 runtime intentionally does not include a scheduler daemon, SSE MCP execution, or SQLite run-history persistence yet.

## Install

From this repository:

```powershell
go build -o bin/mcpipe ./cmd/mcpipe
```

Or run directly while developing:

```powershell
go run ./cmd/mcpipe version
```

Requirements:

- Go 1.26+
- Optional: Ollama for local models
- Optional: `ANTHROPIC_API_KEY` for Anthropic
- Optional: Node/npm for npm-based MCP stdio servers such as `npx @modelcontextprotocol/...`

## Quick Start

Validate and inspect the bundled example:

```powershell
go run ./cmd/mcpipe validate -f examples/research-digest.pipeline.json
go run ./cmd/mcpipe vet -f examples/research-digest.pipeline.json
go run ./cmd/mcpipe explain -f examples/research-digest.pipeline.json
go run ./cmd/mcpipe graph -f examples/research-digest.pipeline.json
```

Preview and run it without live providers:

```powershell
go run ./cmd/mcpipe dry-run -f examples/research-digest.pipeline.json --input "topic=quantum computing"
go run ./cmd/mcpipe run -f examples/research-digest.pipeline.json --input "topic=quantum computing" --mock
```

Create a fresh pipeline:

```powershell
go run ./cmd/mcpipe init pipeline.json
go run ./cmd/mcpipe new code-review review.pipeline.json
```

## The Pipeline Model

A pipeline has inputs, optional plugins, optional agent profiles, MCP servers, ordered steps, policies, and selected outputs. Each step renders a prompt, optionally exposes tools, calls an LLM, and writes named outputs for downstream steps.

Minimal shape:

```json
{
  "$schema": "https://mcpipe.dev/schemas/pipeline/v1.json",
  "version": "1.0.0",
  "defaults": {
    "timeout_ms": 30000,
    "llm": {
      "backend": "ollama",
      "model": "qwen3:7b"
    }
  },
  "inputs": {
    "topic": {
      "type": "string",
      "required": true
    }
  },
  "agents": {
    "summarizer": {
      "description": "Reusable LLM profile for concise summaries.",
      "llm": {
        "backend": "ollama",
        "model": "qwen3:7b",
        "temperature": 0.2
      }
    }
  },
  "steps": [
    {
      "id": "summarize",
      "agent_ref": "summarizer",
      "prompt": {
        "user": "Summarize {{ inputs.topic }} in five bullets."
      },
      "outputs": {
        "summary": "{{ response.text }}"
      }
    }
  ],
  "output": {
    "fields": ["steps.summarize.outputs.summary"]
  }
}
```

Templates support the example-compatible expressions used throughout the repo:

- `{{ inputs.topic }}`
- `{{ steps.search.outputs.findings }}`
- `{{ response.text }}`
- `{{ response.tool_results[0].path }}`
- `{{ inputs.topic | slugify }}`
- `{{ now | date: "%Y%m%d" }}`

## Inputs

Pass inputs one at a time:

```powershell
go run ./cmd/mcpipe run -f pipeline.json --input "topic=quantum computing"
```

Or use a JSON file:

```json
{
  "topic": "quantum computing breakthroughs 2026",
  "output_lang": "en"
}
```

```powershell
go run ./cmd/mcpipe run -f pipeline.json --input-file inputs.local.json
```

Explicit `--input key=value` flags override values from `--input-file`.

## Command Tour

```powershell
# Validate syntax and semantic references
mcpipe validate -f pipeline.json

# Security lint: broad tools, risky MCP commands, unsafe policies
mcpipe vet -f pipeline.json

# Human-readable pipeline summary
mcpipe explain -f pipeline.json

# Preflight inputs, providers, MCP setup, and v1 boundaries
mcpipe doctor -f pipeline.json --input "topic=demo" --mock

# Render Mermaid or Graphviz DOT
mcpipe graph -f pipeline.json
mcpipe graph -f pipeline.json --format dot

# Inspect effective MCP tool permissions
mcpipe tools -f pipeline.json --mock

# Package a shareable, redacted-friendly run bundle
mcpipe bundle -f pipeline.json --input "topic=demo" --out demo.mcpipebundle

# Inspect installed provider/MCP surface
mcpipe providers list
mcpipe mcp list -f pipeline.json
mcpipe plugins list -f pipeline.json
mcpipe agents list -f pipeline.json

# Summarize a redacted audit log
mcpipe inspect run .mcpipe/runs/<run_id>.jsonl

# Generate Markdown docs from the JSON Schema
mcpipe schema-docs --out docs/schema.md

# Compare two pipelines semantically
mcpipe diff old.pipeline.json new.pipeline.json

# Preview rendered prompts and graph order
mcpipe dry-run -f pipeline.json --input "topic=demo"

# Replay a pipeline deterministically with mock adapters
mcpipe replay -f pipeline.json --input "topic=demo" --json

# Execute
mcpipe run -f pipeline.json --input "topic=demo"

# Execute with deterministic mocks
mcpipe run -f pipeline.json --input "topic=demo" --mock

# Automation-friendly output
mcpipe run -f pipeline.json --input "topic=demo" --mock --json
```

## Secure Runs

`mcpipe` treats pipeline execution as privileged automation. The defaults are designed to be safe to reason about:

- file-writing tools are sandboxed to `.mcpipe/outputs`
- `--output-dir` can override the sandbox root
- pipeline `policy` can narrow tool paths, byte limits, and call counts
- audit events are written as redacted JSONL under `.mcpipe/runs`
- final audit output stores hashes, not raw final output text
- prompt, response, tool-result, tool-call, run-duration, and concurrency limits are enforced
- MCP stdio servers run with an environment allowlist plus explicit server env vars
- MCP startup/tool calls have timeouts and bounded response frames
- secrets are redacted from dry-runs, doctor output, runtime errors, final output, and audit logs
- filesystem paths are checked against symlink escapes
- `mcpipe tools` shows the effective tool surface for every step
- `mcpipe inspect run` summarizes audit logs without exposing prompt or output bodies
- `mcpipe diff` highlights prompt, model, dependency, tool, output, and MCP changes before promotion
- run metadata includes attempts, tool calls, and token usage when providers return it

Useful secure-run commands:

```powershell
# Security lint
mcpipe vet -f pipeline.json

# Require explicit confirmation before execution
mcpipe run -f pipeline.json --input "topic=demo" --require-confirmation
mcpipe run -f pipeline.json --input "topic=demo" --require-confirmation --yes

# Constrain runtime behavior
mcpipe run -f pipeline.json `
  --input "topic=demo" `
  --policy strict `
  --max-run-duration 10m `
  --max-concurrent-steps 4 `
  --max-prompt-chars 200000 `
  --max-response-chars 1000000 `
  --max-tool-result-bytes 5000000

# Disable audit logs for ephemeral local smoke runs
mcpipe run -f pipeline.json --input "topic=demo" --mock --no-audit
```

Pipeline-level tool policy:

```json
{
  "policy": {
    "filesystem.write_file": {
      "allowed_paths": [".mcpipe/outputs"],
      "max_bytes": 1000000,
      "max_calls": 2
    },
    "brave_search.*": {
      "max_calls": 5
    }
  }
}
```

## Lockfiles And Signatures

Use lockfiles when CI or production should refuse pipeline drift:

```powershell
mcpipe lock -f pipeline.json
mcpipe lock -f pipeline.json --verify
mcpipe run -f pipeline.json --input "topic=demo" --locked
```

Use signatures when pipelines are shared or promoted between environments:

```powershell
mcpipe keygen --private mcpipe.key --public mcpipe.pub
mcpipe sign -f pipeline.json --key mcpipe.key --out pipeline.sig
mcpipe verify -f pipeline.json --key mcpipe.pub --sig pipeline.sig
```

Keep private keys out of the repository.

## MCP Tools

MCP servers are declared in the pipeline:

```json
{
  "mcp_servers": {
    "filesystem": {
      "transport": "stdio",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", ".mcpipe/outputs"],
      "env": {}
    }
  }
}
```

Steps opt into tools:

```json
{
  "tools": {
    "allow": ["filesystem.write_file"],
    "deny": []
  }
}
```

Notes:

- deny rules win over allow rules
- `server.*` works, but `mcpipe vet` warns on broad access
- `${env:NAME}` references are expanded when launching MCP servers
- stdio MCP is executable in v1
- SSE MCP config is parsed for forward compatibility but not executed yet

## Plugins And Agents

Plugins package reusable MCP servers, tool allow/deny rules, and tool policies:

```json
{
  "plugins": {
    "local_files": {
      "description": "Filesystem writing plugin constrained by policy.",
      "mcp_servers": {
        "filesystem": {
          "transport": "stdio",
          "command": "npx",
          "args": ["-y", "@modelcontextprotocol/server-filesystem", ".mcpipe/outputs"]
        }
      },
      "tools": {
        "allow": ["filesystem.write_file"]
      },
      "policy": {
        "filesystem.write_file": {
          "allowed_paths": [".mcpipe/outputs"],
          "max_calls": 1
        }
      }
    }
  }
}
```

Agents package reusable LLM and agent-loop defaults:

```json
{
  "agents": {
    "writer": {
      "description": "Bounded file-writing agent.",
      "llm": {
        "backend": "ollama",
        "model": "qwen3:1.7b",
        "temperature": 0
      },
      "agent": {
        "enabled": true,
        "max_iterations": 2,
        "stop_on": "no_tool_call"
      }
    }
  }
}
```

Steps opt in with `plugins` and `agent_ref`. Plugin and agent defaults are merged first; explicit step fields still win.

```json
{
  "id": "write_note",
  "plugins": ["local_files"],
  "agent_ref": "writer",
  "prompt": {
    "user": "Write a note about {{ inputs.topic }}."
  },
  "outputs": {
    "file_path": "{{ response.tool_results[0].path }}"
  }
}
```

See [examples/plugins-agents.pipeline.json](./examples/plugins-agents.pipeline.json).

## Templates

`mcpipe new` creates focused starting points:

```powershell
mcpipe new --list
mcpipe new research pipeline.json
mcpipe new code-review review.pipeline.json
mcpipe new docs-digest docs.pipeline.json
mcpipe new extract extract.pipeline.json
```

`init` is kept as a friendly alias for the research digest starter.

## Bundles

Bundles are zip archives with a `.mcpipebundle` extension. They include:

- `pipeline.json`
- `pipeline.lock`
- `graph.mmd`
- `dry-run.txt`
- `manifest.json` with SHA-256 hashes

Create one before sharing or promoting a pipeline:

```powershell
mcpipe bundle -f pipeline.json --input-file inputs.local.json --out review.mcpipebundle
```

Use policy presets when you want a clear operational posture:

```powershell
mcpipe run -f pipeline.json --input-file inputs.json --policy strict
mcpipe run -f pipeline.json --input-file inputs.json --policy ci --mock
mcpipe run -f pipeline.json --input-file inputs.json --policy local-dev
```

## Providers

Ollama:

```powershell
ollama serve
mcpipe run -f pipeline.json --input "topic=demo"
```

Set `OLLAMA_HOST` only if Ollama is not listening on `http://localhost:11434`.

Anthropic:

```powershell
$env:ANTHROPIC_API_KEY = "..."
mcpipe run -f pipeline.json --input "topic=demo"
```

For CI and local development without secrets, use `--mock`.

## Shell Completion

Completion scripts are generated without extra dependencies:

```powershell
mcpipe completion powershell
mcpipe completion bash
mcpipe completion zsh
```

## CI

The repository includes a senior-level GitHub Actions workflow covering:

- actionlint
- gofmt
- `go mod tidy` drift
- generated artifact guard
- `go vet`
- unit tests on Linux, Windows, and macOS
- race tests
- coverage artifact upload
- govulncheck
- Gitleaks secret scanning
- CodeQL
- pipeline security lint
- doctor/lock/signature smoke checks
- CLI smoke checks
- cross-platform builds
- tagged release binaries with SHA-256 checksums

Local parity:

```powershell
go test ./...
go vet ./...
go build ./cmd/mcpipe
```

On Unix-like shells with `make`:

```bash
make ci
```

## Project Layout

```text
cmd/mcpipe            CLI entrypoint
internal/cli          command implementations
internal/config       pipeline loading, defaults, validation
internal/runtime      DAG execution, dry-run, graphing, output formatting
internal/llm          Ollama, Anthropic, mock LLM adapters
internal/mcp          MCP stdio manager and mock manager
internal/security     redaction, sandboxing, audit logs, vet, locks, signatures
internal/template     small safe template renderer
schemas/pipeline      JSON schema
examples              runnable example pipelines
docs                  security notes and cookbook
```

## v1 Boundaries

Accepted but not executed in v1:

- scheduler daemon behavior from `schedule`
- SSE MCP runtime sessions
- SQLite run-history persistence

The config is forward-compatible so pipeline files can grow without forcing those runtime pieces into the MVP too early.

## Development

Useful commands:

```powershell
go test ./...
go test -race ./...
go vet ./...
go run github.com/rhysd/actionlint/cmd/actionlint@latest .github/workflows/ci.yml
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

Before opening a PR, run:

```powershell
gofmt -w cmd internal
go test ./...
go vet ./...
```

## License

MIT. See [LICENSE](./LICENSE).
