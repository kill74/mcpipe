# Cookbook

## Preview A Pipeline

```powershell
mcpipe validate -f examples/research-digest.pipeline.json
mcpipe dry-run -f examples/research-digest.pipeline.json --input "topic=quantum computing"
```

## Run Without Providers

```powershell
mcpipe run -f examples/research-digest.pipeline.json --input "topic=quantum computing" --mock --json
```

Mock mode is deterministic and does not require Ollama, Anthropic credentials, or live MCP servers.

## Start From A Template

```powershell
mcpipe new --list
mcpipe new code-review review.pipeline.json
mcpipe new extract extract.pipeline.json
```

Templates are deliberately small, valid pipelines. They are meant to be edited, locked, tested, and promoted like source code.

## Compare Pipeline Changes

```powershell
mcpipe diff old.pipeline.json new.pipeline.json
```

The diff focuses on semantic risk: changed prompts, models, tool permissions, dependencies, outputs, and MCP server definitions.

## Replay With Mocks

```powershell
mcpipe replay -f pipeline.json --input-file inputs.json --json
```

Replay uses deterministic mock adapters and disables audit writing. It is useful for checking pipeline structure and output propagation without touching real providers or tools.

## See Tool Permissions

```powershell
mcpipe tools -f examples/research-digest.pipeline.json --mock
mcpipe providers list
mcpipe mcp list -f examples/research-digest.pipeline.json
```

Use this before live execution to check which tools each step can actually see after `allow` and `deny` rules are resolved.

## Create A Bundle

```powershell
mcpipe bundle -f pipeline.json --input-file inputs.json --out pipeline.mcpipebundle
```

Bundles are useful when reviewing or promoting a pipeline. They include the pipeline, lockfile, Mermaid graph, dry-run preview, and manifest hashes.

## Use Policy Presets

```powershell
mcpipe run -f pipeline.json --input-file inputs.json --policy strict
mcpipe replay -f pipeline.json --input-file inputs.json --policy ci --json
```

`strict` tightens runtime limits, `ci` avoids audit writes, `local-dev` keeps local iteration comfortable, and `unsafe-lab` is intentionally permissive for isolated experiments.

## Inspect A Run

```powershell
mcpipe run -f pipeline.json --input-file inputs.json
mcpipe inspect run .mcpipe/runs/<run_id>.jsonl
```

The inspection view is designed for humans. Use `--json` on `mcpipe run` when another program needs structured run output.

## Generate Schema Notes

```powershell
mcpipe schema-docs --out docs/schema.md
```

This produces a Markdown reference from `schemas/pipeline/v1.json`.
