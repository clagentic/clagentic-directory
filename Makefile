# Makefile — build targets for clagentic-directory.
# Provides make build / make test / make vet / make fmt for CI and agent runs.
# Every target runs against the directory holding this Makefile, so the result
# is the same from the repo root or through `make -C <path>` from elsewhere.

ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

# Go refuses to build when it cannot derive a cache location (no GOCACHE and
# neither XDG_CACHE_HOME nor HOME set), which is the case in a stripped service
# environment. Only in that case, fall back to a repo-local, gitignored dir.
# Any value the caller or the toolchain environment supplies wins.
ifeq ($(origin GOCACHE)$(origin GOPATH)$(origin GOMODCACHE),undefinedundefinedundefined)
ifeq ($(HOME)$(XDG_CACHE_HOME),)
GOCACHE    := $(ROOT)/.gocache/build
GOPATH     := $(ROOT)/.gocache/path
GOMODCACHE := $(ROOT)/.gocache/path/pkg/mod
export GOCACHE
export GOPATH
export GOMODCACHE
endif
endif

.PHONY: build test vet fmt check

build:
	go build -C $(ROOT) -o clagentic-directory ./cmd/clagentic-directory

test:
	go test -C $(ROOT) ./...

vet:
	go vet -C $(ROOT) ./...

fmt:
	go fmt -C $(ROOT) ./...

check: vet test
