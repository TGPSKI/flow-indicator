.DEFAULT_GOAL := help
.PHONY: build install test test-race check lint ci cover replay clean help

VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
COMMIT   ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "none")
LDFLAGS  := -X main.version=$(VERSION) -X main.commit=$(COMMIT) -extldflags '-static'
GO_ENV   := CGO_ENABLED=0
FIXTURES := healthy recent-thrash bounded-rescue large-data-dump released-obligation
REPLAY_DIR ?= $(CURDIR)/.replay

build: ## Compile the flow-indicator binary
	$(GO_ENV) go build -tags 'netgo osusergo' -ldflags "$(LDFLAGS)" -o ./flow-indicator ./cmd/flow-indicator

install: ## Install flow-indicator into GOBIN
	$(GO_ENV) go install -tags 'netgo osusergo' -ldflags "$(LDFLAGS)" ./cmd/flow-indicator

test: ## go test ./...
	go test ./...

test-race: ## go test -race ./...
	go test -race ./...

check: ## gofmt, go vet and go mod verify
	@out=$$(gofmt -l $$(find . -name '*.go')); \
		if [ -n "$$out" ]; then echo "gofmt: unformatted files:"; echo "$$out"; exit 1; fi
	go vet ./...
	go mod verify

lint: ## golangci-lint run
	golangci-lint run

ci: check test-race lint ## Full gate: check + test-race + lint

cover: ## Coverage report in the browser
	go test -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out

replay: build ## Replay all five fixtures into $(REPLAY_DIR)
	@rm -rf $(REPLAY_DIR)
	@for f in $(FIXTURES); do \
		./flow-indicator replay --adapter generic --data-dir $(REPLAY_DIR) fixtures/$$f/input.jsonl || exit 1; \
	done

clean: ## Remove build artifacts and replay output
	rm -f flow-indicator coverage.out
	rm -rf $(REPLAY_DIR)

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | sort | awk 'BEGIN {FS = ":.*?## "}; {printf "\033[36m%-20s\033[0m %s\n", $$1, $$2}'
