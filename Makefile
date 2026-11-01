# The per-push gates for the managed-agent port.
#
# Every gate here is a make target, because the shared pipeline in latere-ai/ci
# probes this file and runs what it finds rather than carrying commands of its
# own. That is what makes each of them runnable on a workstation: a gate that
# only runs on a runner tells you too late.
#
# The gates themselves come from latere.ai/x/ci-gate, pinned in
# internal/gate/go.mod rather than in the root go.mod. The port promises that
# adopting it costs nothing in a consumer's module graph, and a tool
# requirement at the root would break that promise for a program no consumer
# runs. LATEREGATE reaches the tool through that module and points it back at
# this directory.
LATEREGATE := go -C internal/gate tool lateregate
GATE_ROOT := -C ../..
# A path handed to the tool is resolved from internal/gate, not from here.
GATE_PATH := ../..

GOLANGCI_VERSION ?= v2.13.1

# The adapters, each a module of its own so a consumer of the contract never
# sees a vendor's dependencies. `go test ./...` here stops at a module
# boundary, so every target that tests, races, covers or lints the contract
# runs over these as well; CI calls the same targets.
ADAPTERS := latere

.PHONY: all test test-race test-hermetic cover fmt fmt-check lint lint-config \
        lint-modernize validate deps generated gen-module generate modules

# A bare make runs every gate. Nothing here needs a network beyond the module
# cache, a container engine, or a credential.
all: fmt-check lint-modernize test test-hermetic cover validate lint

# go vet and then the suite, without the race detector, so the fast signal and
# the expensive one are not the same wait. The target never depends on lint: a failing lint that hides a failing test costs
# a second push to learn the second fact.
test:
	go vet ./...
	go test ./...
	go -C internal/gate test ./...
	@for m in $(ADAPTERS); do go -C $$m vet ./... && go -C $$m test ./... || exit 1; done

# Both modules under the detector. The runtime hands a session to callers that
# poll it from other goroutines, so a race here is a defect every adapter
# inherits.
test-race:
	go test -race ./...
	go -C internal/gen test -race ./...
	@for m in $(ADAPTERS); do go -C $$m test -race ./... || exit 1; done

# The suite with only the Go toolchain on PATH. A test that depends on what
# happens to be installed passes on a workstation and fails on a runner, which
# is the worst order to find out.
test-hermetic:
	@$(LATEREGATE) hermetic $(GATE_ROOT)

# Per package rather than as a repository average, against the floor and the
# one exemption in .lateregate.yaml. The figure CONTRIBUTING claims had no gate
# behind it before this, and a claim in a document with no gate goes stale
# silently.
cover:
	go test -coverpkg=./... -coverprofile=coverage.out ./...
	@$(LATEREGATE) cover $(GATE_ROOT) -profile=$(GATE_PATH)/coverage.out
	@for m in $(ADAPTERS); do \
		go -C $$m test -coverprofile=coverage.out ./... && \
		$(LATEREGATE) cover -C ../../$$m -profile=../../$$m/coverage.out || exit 1; \
	done

fmt:
	gofmt -w .

fmt-check:
	@$(LATEREGATE) fmt-check $(GATE_ROOT)

lint-modernize:
	@$(LATEREGATE) modernize $(GATE_ROOT)

# .golangci.yml is generated and gitignored: golangci-lint cannot inherit a
# shared config, so it is rendered from latere.ai/x/ci-gate on every run.
# Regenerating rather than committing is what makes divergence impossible
# instead of merely detectable.
lint-config:
	@$(LATEREGATE) golangci $(GATE_ROOT)

# The spec tree describes itself consistently: every spec numbered, indexed,
# and every wikilink resolving.
spec-lint:
	@$(LATEREGATE) spec-lint $(GATE_ROOT)

# Runs the linter the CI lint job runs, against the config lint-config renders.
# The version is pinned rather than `latest`, so a run cannot pick up a build
# published after this file was reviewed, and `go run` fetches it, so the gate
# needs no separately installed binary to be a gate.
lint: lint-config
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run ./...
	@for m in $(ADAPTERS); do \
		(cd $$m && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run ./...) || exit 1; \
	done

# What a gate over the root module does not reach: the gate module, the
# generator and each adapter are modules of their own. Each adapter is vetted, raced, held to the coverage
# floor and linted, as the root is.
modules: validate lint-config
	go -C internal/gate test ./...
	go -C internal/gen test -race ./...
	@for m in $(ADAPTERS); do \
		go -C $$m vet ./... && \
		go -C $$m test -race -coverprofile=coverage.out ./... && \
		$(LATEREGATE) cover -C ../../$$m -profile=../../$$m/coverage.out || exit 1; \
	done
	@for m in $(ADAPTERS); do \
		(cd $$m && go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_VERSION) run ./...) || exit 1; \
	done

# The three promises this repository makes that no shared Go gate knows about.
# Each keeps its own target, so a failure says which promise broke.
validate: deps generated gen-module

# The port's one promise to a consumer is that adopting it costs nothing in
# their module graph. A root go.sum existing at all would already mean the
# promise had been broken, and TestModuleDependsOnNothing proves the same thing
# from the other side, over the packages a consumer actually builds.
deps:
	@test ! -f go.sum || { \
		echo "go.sum exists: the root module took a dependency."; \
		echo "tools belong in internal/gate or internal/gen, which are modules for this reason."; \
		exit 1; \
	}
	go test -run TestModuleDependsOnNothing ./...

# The schema is the source of truth, so a schema edit that was never
# regenerated must fail here rather than leave Go and JSON disagreeing about
# the contract with each looking authoritative on its own.
generated:
	go generate ./...
	git diff --exit-code -- types.gen.go

# The generator is a module of its own, so `go vet ./...` at the root never
# reaches it and neither does the shared gate, which checks the root module.
gen-module:
	go -C internal/gen vet ./...
	go -C internal/gen test ./...

# The schema is the contract; this rewrites its Go form.
generate:
	go generate ./...
