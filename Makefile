.PHONY: fmt fmt-check test test-race cover vet vuln vet-pipeline doctor build smoke check ci clean

fmt:
	gofmt -w cmd internal

fmt-check:
	@test -z "$$(gofmt -l cmd internal)" || (gofmt -l cmd internal && exit 1)

test:
	go test ./...

test-race:
	go test -race ./...

cover:
	go test -covermode=atomic -coverprofile=coverage.out ./...

vet:
	go vet ./...

vuln:
	govulncheck ./...

vet-pipeline:
	go run ./cmd/mcpipe vet -f examples/research-digest.pipeline.json

doctor:
	go run ./cmd/mcpipe doctor -f examples/research-digest.pipeline.json --input "topic=local check" --mock

build:
	go build -o bin/mcpipe ./cmd/mcpipe

smoke:
	go run ./cmd/mcpipe validate -f examples/research-digest.pipeline.json
	go run ./cmd/mcpipe dry-run -f examples/research-digest.pipeline.json --input "topic=local smoke"
	go run ./cmd/mcpipe run -f examples/research-digest.pipeline.json --input "topic=local smoke" --mock --json --no-audit --output-dir "$${TMPDIR:-/tmp}/mcpipe-outputs" >/dev/null

check: fmt-check vet test build

ci: fmt-check vet test test-race cover vet-pipeline doctor smoke build

clean:
	rm -rf bin dist coverage.out coverage.html mcpipe mcpipe.exe
