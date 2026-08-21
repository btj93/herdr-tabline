GO ?= go
BIN := bin/herdr-tabline

.PHONY: build test race vet fmt-check check

build:
	$(GO) build -o $(BIN) ./cmd/herdr-tabline

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

vet:
	$(GO) vet ./...

fmt-check:
	@test -z "$$($(GO)fmt -l cmd internal)" || { echo "gofmt needed:"; $(GO)fmt -l cmd internal; exit 1; }

# check runs every non-live verification. It deliberately excludes anything that talks to a
# running Herdr server, so it is safe in CI and on a machine with no session.
check: fmt-check vet build test race
