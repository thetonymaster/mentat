.PHONY: all test lint cover ci clean harness-up harness-down smoke labs captures example

# Go sources that each lab SUT binary is built from. Evaluated at parse time so
# the file-target prerequisites below rebuild a binary only when its own source
# tree changes (not on every invocation).
RESEARCHBOT_SRC := $(shell find tracelab/researchbot -name '*.go')
ORDERFLOW_SRC := $(shell find tracelab/orderflow -name '*.go')

all: test lint

test:
	go test ./... -race

cover:
	bash .claude/skills/coverage/coverage.sh ./...

ci: lint test cover example

lint:
	golangci-lint run ./...

# example builds, tests, and import-lints the standalone extension module
# (examples/kafkaecho). It is a SEPARATE Go module, so the root `go test ./...`
# never reaches it — this target is what covers it in CI (spec 007 SC-001). The
# import lint FAILS if any example .go file imports a module-private
# (mentat/internal) package: a grep match is a non-zero `!`-negated exit, so CI
# fails and names the offending file:line.
example:
	cd examples/kafkaecho && test -z "$$(gofmt -l .)" || { echo "gofmt needs formatting:"; gofmt -l .; exit 1; }
	cd examples/kafkaecho && go vet ./... && go build ./... && go test ./...
	[ ! -f examples/kafkaecho/.golangci.yml ] || (cd examples/kafkaecho && golangci-lint run ./...)
	! grep -rn --include='*.go' "mentat/internal" examples/

clean:
	go clean ./...
	rm -f cover.out coverage.out

# labs builds the prebuilt lab SUT binaries and regenerates the committed trace
# fixtures. The bin/* file targets rebuild only when their Go source changes; the
# capture tools are deterministic, so re-running leaves testdata/traces/ clean.
labs: bin/researchbot bin/orderflow captures

bin/researchbot: $(RESEARCHBOT_SRC)
	go build -o $@ ./tracelab/researchbot/cmd/researchbot

bin/orderflow: $(ORDERFLOW_SRC)
	go build -o $@ ./tracelab/orderflow/cmd/orderflow

captures:
	go run ./tracelab/researchbot/cmd/capture
	go run ./tracelab/orderflow/cmd/capture

harness-up: labs
	docker compose -f deploy/docker-compose.yml up -d

harness-down:
	docker compose -f deploy/docker-compose.yml down -v

smoke:
	bash deploy/smoke.sh
