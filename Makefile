# Makefile — build targets for clagentic-directory.
# Provides make build / make test / make vet / make fmt for CI and agent runs.
# Every target runs against the directory holding this Makefile, so the result
# is the same from the repo root or through `make -C <path>` from elsewhere.

ROOT := $(patsubst %/,%,$(dir $(abspath $(lastword $(MAKEFILE_LIST)))))

# Go needs a build cache, a GOPATH and a module cache, and derives each from
# the environment: GOCACHE from XDG_CACHE_HOME or HOME, GOPATH from HOME, and
# GOMODCACHE from GOPATH. In a stripped service environment those are absent
# and go refuses to build. Each location is decided on its own, and only when
# Go would have no way to derive it; a value the caller set is never replaced.
ifeq ($(GOCACHE)$(XDG_CACHE_HOME)$(HOME),)
GOCACHE := $(ROOT)/.gocache/build
export GOCACHE
endif
ifeq ($(GOPATH)$(HOME),)
GOPATH := $(ROOT)/.gocache/gopath
export GOPATH
endif
# GOMODCACHE needs no fallback of its own: Go derives it from GOPATH, which is
# either the caller's value or the one set above.

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
