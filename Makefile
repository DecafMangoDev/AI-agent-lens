GO ?= go
STATICCHECK_VERSION := v0.8.1
STATICCHECK := $(CURDIR)/.tools/staticcheck-$(STATICCHECK_VERSION)

.PHONY: build test fmt fmt-check vet tools lint check reports

build:
	$(GO) build -o bin/agentlens .

test:
	$(GO) test -race ./...

fmt:
	gofmt -w .

fmt-check:
	@test -z "$$(gofmt -l .)" || { gofmt -l .; echo 'Run make fmt'; exit 1; }

vet:
	$(GO) vet ./...

# Requires network only for the first installation of the pinned linter.
$(STATICCHECK):
	@mkdir -p .tools
	GOBIN=$(CURDIR)/.tools $(GO) install honnef.co/go/tools/cmd/staticcheck@$(STATICCHECK_VERSION)
	@mv .tools/staticcheck $(STATICCHECK)

tools: $(STATICCHECK)

lint: fmt-check vet $(STATICCHECK)
	$(STATICCHECK) ./...

check: lint test

reports:
	@mkdir -p docs/reports
	$(GO) run . analyze --input testdata/real > docs/reports/report.txt
	$(GO) run . analyze --input testdata/real --json > docs/reports/report.json
	$(GO) run . analyze --input testdata/snapshots/session-metrics.jsonl > docs/reports/session-metrics.txt
	$(GO) run . analyze --input testdata/snapshots/session-metrics.jsonl --json > docs/reports/session-metrics.json
