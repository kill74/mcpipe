# Changelog

## 0.3.0

- Added `env.VAR` template resolution with OS env fallback, doctor checks, and secret redaction.
- Added webhook notifications on pipeline failure with retry and configurable headers.
- Added streaming output for Anthropic, OpenAI, and Ollama via SSE with progress display.

## 0.2.0

- Added OpenAI Chat Completions provider with tool calling support.
- Added template filters: `upper`, `lower`, `trim`, `truncate`, `json`, `base64`.
- Added input types: `number`, `boolean`, `array` with validation and type coercion.
- Added example pipelines: `code-review`, `data-extract`, `customer-support`, `parallel-research`.

## 0.1.0

- Added executable Go CLI with `init`, `validate`, `dry-run`, `run`, and `version`.
- Added strict pipeline parsing and semantic validation.
- Added dependency-ordered runtime execution with parallel-ready levels, retries, timeouts, fallback skips, and output rendering.
- Added example-compatible template rendering with `slugify` and `date` filters.
- Added real Ollama, Anthropic, and MCP stdio integration boundaries.
- Added deterministic mock mode for local development and CI.
- Added `explain`, `doctor`, `graph`, `run --json`, and shell completion scripts.
- Added `vet`, `lock`, `keygen`, `sign`, `verify`, sandboxed file-write enforcement, redacted audit logs, runtime size limits, and hardened MCP stdio execution.
