.PHONY: all test lint cover ci clean harness-up harness-down smoke

all: test lint

test:
	go test ./... -race

cover:
	bash .claude/skills/coverage/coverage.sh ./...

ci: lint test cover

lint:
	golangci-lint run ./...

clean:
	go clean ./...
	rm -f cover.out coverage.out

harness-up:
	docker compose -f deploy/docker-compose.yml up -d

harness-down:
	docker compose -f deploy/docker-compose.yml down -v

smoke:
	bash deploy/smoke.sh
