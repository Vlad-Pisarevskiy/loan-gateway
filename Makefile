GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0

.PHONY: test test-short lint fmt up down demo

## test: все тесты с детектором гонок (нужен Docker для testcontainers)
test:
	go test -race ./...

## test-short: только юнит-тесты, без Docker
test-short:
	go test -short ./...

## lint: gofmt, go vet, golangci-lint
lint:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	$(GOLANGCI_LINT) run

fmt:
	gofmt -w .

up:
	docker compose up --build -d

down:
	docker compose down

demo:
	./scripts/demo.sh
