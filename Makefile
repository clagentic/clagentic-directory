# Makefile — build targets for clagentic-directory.
# Provides make build / make test / make vet / make fmt for CI and agent runs.
# go build / go test are executed from the module root with correct GOPATH.

GOPATH     ?= /root/go
GOMODCACHE ?= /root/go/pkg/mod
GOCACHE    ?= /root/.cache/go

export GOPATH
export GOMODCACHE
export GOCACHE

.PHONY: build test vet fmt check

build:
	go build -C /workspace/clagentic-directory ./...

test:
	go test -C /workspace/clagentic-directory ./...

vet:
	go vet -C /workspace/clagentic-directory ./...

fmt:
	go fmt -C /workspace/clagentic-directory ./...

check: vet test
