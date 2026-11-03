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

# REVISION is the commit the build comes from, stamped into the binary as
# main.buildRevision. A tree with uncommitted changes gets a -dirty suffix so a
# binary never claims a commit it was not built from. Outside a git checkout it
# is empty and nothing is stamped.
REVISION := $(shell git -C $(ROOT) rev-parse HEAD 2>/dev/null)
ifneq ($(REVISION),)
ifneq ($(shell git -C $(ROOT) status --porcelain --untracked-files=no 2>/dev/null),)
REVISION := $(REVISION)-dirty
endif
LDFLAGS := -X main.buildRevision=$(REVISION)
endif

.PHONY: build test test-json test-scripts vet fmt check

build:
	go build -C $(ROOT) -trimpath -ldflags "$(LDFLAGS)" -o clagentic-directory ./cmd/clagentic-directory

test:
	go test -C $(ROOT) ./...

# test-json runs go test -json and writes the event stream to GOTEST_JSON. It is
# the one place the crew test tier's go invocation lives, so it gets the cache
# fallback above. scripts/go_test_junit.py calls it and converts the stream.
# PKGS is a space-separated package list (default: every package, for a hand
# run; the converter always passes it on the command line, which overrides the
# environment and this default); the caller
# has already restricted it to [A-Za-z0-9_./-], so the shell split is lossless.
# Exit code: 0 when every test passes; 2 when go test fails, whether from a
# failing test or a build failure (make reports a failed recipe as 2, so the two
# are told apart in the event stream, not here).
PKGS ?= ./...
test-json:
	go test -C $(ROOT) -json $(PKGS) > "$(or $(GOTEST_JSON),$(error GOTEST_JSON is required))"

test-scripts:
	python3 -m unittest discover -s $(ROOT)/scripts -p 'test_*.py'

vet:
	go vet -C $(ROOT) ./...

fmt:
	go fmt -C $(ROOT) ./...

check: vet test test-scripts
