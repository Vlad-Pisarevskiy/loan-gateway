GOLANGCI_LINT = go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.14.0
PROMETHEUS_IMAGE = prom/prometheus:v3.15.0

.PHONY: test test-short lint fmt up down demo obs-check

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

## obs-check: конфиг Prometheus, тесты алертов и JSON дашборда (нужен Docker)
obs-check:
	docker run --rm -v "$(CURDIR)/deploy/observability:/etc/prometheus:ro" --entrypoint promtool $(PROMETHEUS_IMAGE) check config /etc/prometheus/prometheus.yml
	docker run --rm -v "$(CURDIR)/deploy/observability:/w:ro" -w /w --entrypoint promtool $(PROMETHEUS_IMAGE) test rules alerts_test.yml
	python3 -m json.tool deploy/observability/grafana/dashboards/loan-gateway.json > /dev/null
