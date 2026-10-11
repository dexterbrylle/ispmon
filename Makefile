GOPATH ?= $(shell go env GOPATH)
export PATH := $(GOPATH)/bin:$(PATH)

STATICCHECK ?= $(shell command -v staticcheck 2>/dev/null || echo "$(GOPATH)/bin/staticcheck")
GITLEAKS ?= $(shell command -v gitleaks 2>/dev/null || echo "gitleaks")

.PHONY: all build test fmt vet lint secrets check install-hooks

all: build

build:
	CGO_ENABLED=0 go build -o bin/ispmon ./cmd/ispmon

test:
	go test -race ./...

fmt:
	@DIFF=$$(gofmt -l .); \
	if [ -n "$$DIFF" ]; then \
		echo "Unformatted Go source files detected. Please run 'gofmt -w .':"; \
		echo "$$DIFF"; \
		exit 1; \
	fi

vet:
	go vet ./...

lint:
	@if [ ! -x "$$(command -v $(STATICCHECK))" ] && [ ! -x "$(STATICCHECK)" ]; then \
		echo "staticcheck is not installed. Install it with:"; \
		echo "  go install honnef.co/go/tools/cmd/staticcheck@latest"; \
		exit 1; \
	fi
	$(STATICCHECK) ./...

secrets:
	@if [ ! -x "$$(command -v $(GITLEAKS))" ] && [ ! -x "$(GITLEAKS)" ]; then \
		echo "gitleaks is not installed. Install it with:"; \
		echo "  brew install gitleaks (macOS) or see https://github.com/gitleaks/gitleaks"; \
		exit 1; \
	fi
	$(GITLEAKS) detect --no-banner

check: fmt vet lint test secrets

install-hooks:
	@mkdir -p .git/hooks
	@printf '#!/bin/sh\nset -e\nmake check\n' > .git/hooks/pre-push
	@chmod +x .git/hooks/pre-push
	@echo "Installed pre-push hook at .git/hooks/pre-push"
