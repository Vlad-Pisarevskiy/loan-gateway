# Loan Gateway

Сервис-шлюз: принимает заявку на кредит наличными, рассылает её в банки-партнёры через адаптеры и собирает решения.
Спека — `docs/spec.md`, план — `docs/plan.md`, журнал — `docs/dev-log.md`.

## Команды

- `make test` — все тесты с `-race` (нужен Docker: testcontainers поднимает PostgreSQL)
- `make test-short` — только юнит-тесты, без Docker
- `make lint` — gofmt, go vet, golangci-lint
- `make up` / `make down` — поднять и остановить систему в docker compose
- `make demo` — отправить заявку и дождаться решений (нужен `jq`)

## Карта пакетов

- `cmd/gateway` — подкоманды `api`, `worker`, `migrate`; `banks.go` — реестр адаптеров
- `cmd/mockbank` — заглушка банка, диалект из `MOCKBANK_DIALECT`
- `internal/application` — домен: типы, валидация, хеш запроса, сервис Create/Get
- `internal/bank` — интерфейсы банков, классы ошибок, реестр, деньги, подпись вебхуков; адаптеры в подпакетах
- `internal/dispatch` — воркер: захват задач, вызов банков, ретраи, чистильщик
- `internal/store/postgres` — миграции и репозитории; `pgtest` — PostgreSQL в Docker для тестов
- `internal/httpapi` — HTTP API: заявки, вебхуки, health
- `internal/mockbank` — заглушки банков: правила решений и диалекты
- `internal/config` — конфигурация из env
- `internal/telemetry` — метрики Prometheus, трейсинг OpenTelemetry, `trace_id` в логах (раздел 14 спеки); `telemetrytest` — помощники тестов
- `test/e2e` — end-to-end тесты

## Соглашения

- Деньги — `int64` в копейках, ставки — `int` в базисных пунктах. Рубли и проценты строкой — только в адаптерах и заглушке через `internal/bank/money.go`, без `float`.
- ФИО, телефон, дату рождения и тела запросов к банкам не логируем и не кладём в тексты ошибок.
- Ошибки адаптеров — `bank.ErrTemporary` или `bank.ErrPermanent`; отказ банка — `Decision`, не ошибка.
- Каждый `UPDATE bank_requests` из воркера содержит `status = 'sending' AND attempts = $claimed`.
- Сначала тест, потом код. Табличные тесты. Интеграционные и e2e-тесты пропускаются при `-short`.
- Комментарии — на русском; строки ошибок и логов — на английском, с маленькой буквы.
- Новые зависимости — только при явной необходимости.

## Git (gitflow)

- Работаем только в `feature/<slug>` от `develop`; PR — в `develop`. В `main` попадают только релизы.
- Коммиты — Conventional Commits на английском: `feat:`, `fix:`, `test:`, `docs:`, `chore:`, `ci:`.
- Никаких force-push в `develop` и `main`.

## Готовность задачи

- `make lint` и `make test` зелёные.
- В `docs/dev-log.md` добавлена строка о задаче.
- Спеку молча не меняем: при неоднозначности — остановиться и спросить.
