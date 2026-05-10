# Security Model

`mcpipe` treats pipeline execution as privileged automation. A pipeline can call models, start MCP processes, pass secrets through prompts, and write files through tools, so the runtime is designed to make those powers explicit and inspectable.

## Default Protections

- Pipeline files are loaded with strict unknown-field validation.
- Step IDs, dependency references, output references, and input values are validated before runtime.
- File-writing MCP tools are sandboxed to `.mcpipe/outputs` unless `--output-dir` or pipeline policy narrows the path.
- Symlink escapes are rejected when checking sandboxed paths.
- MCP stdio processes receive a restricted base environment plus explicit server env vars.
- MCP initialize and tool calls are bounded by timeouts and response-size limits.
- Prompt, response, tool-result, concurrency, and run-duration limits are enforced.
- Audit logs are JSONL, redacted, and store final output hashes instead of final output bodies.
- Secret-like inputs, MCP env values, and MCP headers are redacted from user-facing output and audit events.

## Recommended Production Flow

1. Run `mcpipe validate -f pipeline.json`.
2. Run `mcpipe vet -f pipeline.json` and fix blocking findings.
3. Run `mcpipe doctor -f pipeline.json --input-file inputs.json`.
4. Run `mcpipe dry-run -f pipeline.json --input-file inputs.json`.
5. Create or verify a lockfile with `mcpipe lock -f pipeline.json`.
6. Execute with `mcpipe run -f pipeline.json --input-file inputs.json --locked --require-confirmation --yes`.
7. Inspect the redacted audit log with `mcpipe inspect run .mcpipe/runs/<run_id>.jsonl`.
8. Package review artifacts with `mcpipe bundle -f pipeline.json --input-file inputs.json`.

## Policy Example

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

Prefer narrow policies. Broad `server.*` tool grants are convenient during exploration, but production pipelines should allow the specific tools they need.

## Policy Presets

- `default`: balanced runtime defaults.
- `strict`: lower prompt, response, tool-result, concurrency, write-size, and tool-call limits.
- `ci`: disables audit writes and keeps deterministic checks quiet.
- `local-dev`: modest concurrency for laptop-friendly iteration.
- `unsafe-lab`: permissive limits for isolated experiments only.

Use `strict` for trusted automation unless a pipeline has a specific reason to relax limits.

## Known V1 Boundaries

- SSE MCP servers are parsed and diagnosed but not executed.
- Cron schedules are parsed but no scheduler daemon is included.
- SQLite run history config is parsed but audit logs are currently JSONL.
- Provider network egress is controlled by the host environment, not by a built-in network sandbox.
