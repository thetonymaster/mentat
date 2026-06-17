.PHONY: harness-up harness-down smoke lint

lint:
	golangci-lint run ./...

harness-up:
	docker compose -f deploy/docker-compose.yml up -d

harness-down:
	docker compose -f deploy/docker-compose.yml down -v

smoke:
	bash deploy/smoke.sh
