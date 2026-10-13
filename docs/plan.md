# Loan Gateway MVP — план реализации

> **Для агентов-исполнителей:** ОБЯЗАТЕЛЬНЫЙ НАВЫК: superpowers:subagent-driven-development (рекомендуется) или superpowers:executing-plans — задача за задачей. Шаги размечены чекбоксами (`- [ ]`).

**Цель:** сервис-шлюз, который принимает заявку на кредит наличными, рассылает её в три банка-партнёра через адаптеры и отдаёт сводку решений — релиз v0.1.0.

**Архитектура:** модульный монолит на Go: бинарник `gateway` (подкоманды `api`, `worker`, `migrate`) и заглушка банков `mockbank`. Таблица `bank_requests` в PostgreSQL одновременно служит очередью: воркер забирает задачи через `FOR UPDATE SKIP LOCKED`, вызывает адаптеры банков и ретраит с backoff; асинхронный банк присылает решение вебхуком с HMAC-подписью.

**Стек:** Go 1.26, PostgreSQL 18, pgx/v5, goose/v3, google/uuid, testcontainers-go, `net/http`, `log/slog`, Docker Compose, GitHub Actions, golangci-lint v2.

**Спека:** `docs/spec.md`. План опирается на неё; исполнители читают оба документа. При расхождении права спека — остановиться и спросить.

## Global Constraints

- Go 1.26 (`go 1.26` в `go.mod`), модуль `github.com/Vlad-Pisarevskiy/loan-gateway`.
- PostgreSQL 18 (образ `postgres:18-alpine`), Docker Compose. Без брокеров сообщений.
- Зависимости и версии: `github.com/jackc/pgx/v5` v5.11.0, `github.com/pressly/goose/v3` v3.29.0, `github.com/google/uuid` v1.6.0, `github.com/testcontainers/testcontainers-go` и `.../modules/postgres` v0.44.0; линтер — golangci-lint v2.14.0. Новые зависимости — только при явной необходимости.
- HTTP — стандартный `net/http` с шаблонами роутинга Go 1.22+; логи — `log/slog`, JSON в stdout.
- Деньги — `int64` в копейках, ставки — `int` в базисных пунктах (`1590` = 15,90 %). Рубли и проценты строкой — только в адаптерах банков и заглушке, через `internal/bank/money.go`, без `float` (исключение — расчёт аннуитета в заглушке).
- ФИО, телефон, дата рождения и тела запросов к банкам не попадают в логи и тексты ошибок.
- Каждый `UPDATE bank_requests` из воркера содержит защиту владения: `status = 'sending' AND attempts = $claimedAttempts`.
- Ошибки адаптеров оборачиваются в `bank.ErrTemporary` или `bank.ErrPermanent`; ошибка без класса считается временной; отказ банка — `Decision`, а не ошибка.
- Комментарии в коде — на русском; строки ошибок и сообщения логов — на английском, с маленькой буквы.
- Интеграционные и e2e-тесты пропускаются при `go test -short` (через `testing.Short()`).
- Gitflow: работа только в `feature/<slug>` от `develop`, PR — в `develop`; `main` — только релизы с тегом `vX.Y.Z`. Коммиты — Conventional Commits на английском.

## Review Focus

Входы и сбои, которые спека подразумевает, но легко упустить. Для каждого в указанной задаче есть закрепляющий тест.

1. **Граничный возраст.** Заявителю 18 исполняется завтра или сегодня исполнилось 76; родившиеся 29 февраля → полный возраст считается корректно, граница 18–75 включительно. Тесты: Task 2 (`TestValidateAgeBoundaries`, `TestAge`).
2. **ФИО кириллицей.** Ровно 200 символов (400 байт) принимается, 201 — `400`. Длина считается в рунах. Тест: Task 2 (`TestValidateCountsRunes`).
3. **Суммы с копейками.** Сумма 300 000,50 ₽ уходит в банки с рублями-строкой как `"300000.50"`, без потерь. Тесты: Task 4 (`TestFormatRubles`), Task 11 (`TestSubmitKeepsKopecks`).
4. **PostgreSQL недоступна у работающего воркера.** Воркер пишет ошибку в лог и продолжает цикл, а не падает. Тест: Task 7 (`TestRunKeepsWorkingAfterClaimError`).
5. **Вебхук пришёл раньше, чем воркер сохранил `external_id`.** Шлюз отвечает `404`, заглушка westbank повторяет вебхук, решение не теряется. Тест: Task 17 (`TestWestRetriesCallbackUntilAccepted`).

---

## Перед началом (делает человек)

- [ ] **Переименовать папку проекта.** Сейчас имя заканчивается пробелом (`superintelligence `), это ломает скрипты и агентов. Закрыть Claude Code и выполнить:
  ```bash
  cd ~ && mv "superintelligence " loan-gateway && cd loan-gateway && claude
  ```
- [ ] **Переименовать репозиторий на GitHub** в `loan-gateway` (Settings → General → Repository name) и обновить remote:
  ```bash
  git remote set-url origin git@github.com:Vlad-Pisarevskiy/loan-gateway.git
  ```
- [ ] **Установить `gh` и `jq`.** `gh` — по инструкции https://github.com/cli/cli/blob/trunk/docs/install_linux.md (пакет из apt в Ubuntu 22.04 устарел); `jq` — `sudo apt install jq`. Затем `gh auth login`.
- [ ] **Опубликовать ветки gitflow** и сделать `develop` веткой по умолчанию (Settings → Branches → Default branch):
  ```bash
  git push -u origin main develop
  ```

## Процесс выполнения задачи

Каждая задача — одна ветка `feature/<slug>` (slug указан в заголовке задачи) и один PR в `develop`.

1. **Ветка:** `git switch develop && git pull --ff-only && git switch -c feature/<slug>`.
2. **Реализация:** исполнитель (роль `developer`) проходит шаги задачи по порядку, коммитит после каждого зелёного цикла.
3. **Проверка:** `make lint && make test` — зелёные.
4. **PR:** `git push -u origin feature/<slug> && gh pr create --base develop --title "<заголовок>" --body "Task N из docs/plan.md"`.
5. **Ревью:** роль `reviewer` смотрит `git diff develop...HEAD`; замечания `blocker` и `major` исправляются коммитами в ту же ветку.
6. **Журнал:** строка в `docs/dev-log.md` (дата, задача, где, PR, замечания исправлено/всего, стоимость), коммит `docs: log task N`, push.
7. **Merge:** CI зелёный → `gh pr merge --merge --delete-branch && git switch develop && git pull --ff-only`.

Облачные задачи (этап 2) проходят шаги 1–6 в облачной сессии; merge делает человек после ревью.

## Этапы

| Этап | Задачи | Где |
|---|---|---|
| 1. Фундамент и northbank | Task 1–15 | локально, последовательно |
| 2. eastbank и westbank | Task 16, Task 17 | облако, две параллельные сессии |
| 3. Релиз | Task 18 | локально |

## Карта файлов

```
cmd/gateway/main.go               разбор подкоманд, логгер, сигналы
cmd/gateway/banks.go              реестр адаптеров, сборка банков из конфигурации
cmd/gateway/api.go                запуск HTTP API
cmd/gateway/worker.go             запуск воркера
cmd/mockbank/main.go              заглушка банка, выбор диалекта
internal/config/config.go         env → конфигурация gateway и mockbank
internal/application/types.go     доменные типы и статусы
internal/application/validate.go  валидация заявки, возраст
internal/application/hash.go      хеш запроса для идемпотентности
internal/application/service.go   сценарии Create/Get, интерфейс Repository
internal/bank/bank.go             интерфейсы Bank/CallbackHandler, классы ошибок
internal/bank/httpjson.go         PostJSON для адаптеров
internal/bank/registry.go         реестр банков
internal/bank/money.go            рубли/проценты строкой ↔ копейки/bps
internal/bank/webhook.go          подпись и проверка вебхуков (HMAC)
internal/bank/northbank/          адаптер northbank (синхронный)
internal/bank/eastbank/           адаптер eastbank (синхронный, копейки)
internal/bank/westbank/           адаптер westbank (асинхронный, вебхук)
internal/dispatch/types.go        Task, Outcome, интерфейс Queue
internal/dispatch/backoff.go      backoff с full jitter
internal/dispatch/worker.go       цикл воркера, чистильщик, остановка
internal/store/postgres/          миграции, Applications, Queue
internal/store/postgres/pgtest/   PostgreSQL в Docker для тестов
internal/httpapi/                 роутер, заявки, вебхуки, health, problem+json
internal/mockbank/                правила решений, общая механика, диалекты north/east/west
test/e2e/                         end-to-end тесты
scripts/demo.sh                   демо через curl + jq
Dockerfile, compose.yaml          образ и окружение
```

---

### Task 1: Каркас репозитория, CI и роли агентов

**Ветка:** `feature/bootstrap` · **Этап 1, локально**

**Files:**
- Modify: `go.mod`
- Create: `.gitignore`, `Makefile`, `.golangci.yml`, `.github/workflows/ci.yml`, `cmd/gateway/main.go`, `CLAUDE.md`, `.claude/agents/developer.md`, `.claude/agents/reviewer.md`, `docs/dev-log.md`, `README.md`

**Interfaces:**
- Consumes: —
- Produces: `make test`, `make test-short`, `make lint`, `make up`, `make down`, `make demo`; CI-джоб `check`; роли агентов `developer` и `reviewer`.

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git switch -c feature/bootstrap
```

- [ ] **Step 2: Переписать `go.mod`**

Файл: `go.mod`
```
module github.com/Vlad-Pisarevskiy/loan-gateway

go 1.26
```

- [ ] **Step 3: Создать `.gitignore`**

Файл: `.gitignore`
```
.idea/
.vscode/
/bin/
*.test
*.out
.env
```

- [ ] **Step 4: Создать заготовку бинарника** (полная версия — в Task 13)

Файл: `cmd/gateway/main.go`
```go
// Command gateway — шлюз заявок на кредит. Подкоманды: api, worker, migrate.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "usage: gateway api|worker|migrate")
	os.Exit(2)
}
```

- [ ] **Step 5: Создать `Makefile`** (отступы в рецептах — табуляция)

Файл: `Makefile`
```make
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
```

- [ ] **Step 6: Создать конфигурацию линтера**

Файл: `.golangci.yml`
```yaml
version: "2"

linters:
  default: standard
  enable:
    - bodyclose
    - errorlint
    - nilerr
    - unconvert
  exclusions:
    presets:
      - std-error-handling
    rules:
      - path: _test\.go
        linters:
          - bodyclose
```

- [ ] **Step 7: Создать CI**

Файл: `.github/workflows/ci.yml`
```yaml
name: ci

on:
  pull_request:
    branches: [develop, main]
  push:
    branches: [develop, main]

jobs:
  check:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with:
          go-version-file: go.mod
      - name: Lint
        run: make lint
      - name: Test
        run: make test
```

- [ ] **Step 8: Проверить сборку, линтер и тесты**

Run: `go build ./... && make lint && make test`
Expected: сборка без ошибок; `make lint` без замечаний (первый запуск скачивает golangci-lint, 1–2 минуты); `make test` печатает `? github.com/Vlad-Pisarevskiy/loan-gateway/cmd/gateway [no test files]` и завершается с кодом 0.

- [ ] **Step 9: Создать `CLAUDE.md`**

Файл: `CLAUDE.md`
```markdown
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
```

- [ ] **Step 10: Заполнить роли агентов**

Файл: `.claude/agents/developer.md`
```markdown
---
name: developer
description: Реализует одну задачу из docs/plan.md через TDD в текущей feature-ветке. Используй для каждой задачи плана.
model: sonnet
---

Ты — Go-разработчик проекта Loan Gateway. Тебе дают одну задачу из `docs/plan.md`.

Как работать:
1. Прочитай задачу целиком, раздел «Global Constraints» плана и разделы `docs/spec.md`, на которые опирается задача.
2. Иди по шагам задачи строго по порядку. Тест пишется и запускается до кода; убедись, что он падает по ожидаемой причине.
3. Код из плана — основа. Если он не компилируется или противоречит спеке, исправь минимально и опиши расхождение в отчёте.
4. Не расширяй объём: никаких «заодно», новых зависимостей и рефакторинга вне задачи.
5. Перед завершением `make lint` и `make test` должны быть зелёными. Коммиты — Conventional Commits на английском, только в текущей feature-ветке.
6. Если спека неоднозначна или шаг невыполним — остановись и опиши вопрос, не угадывай.

Отчёт: что сделано, какие файлы изменены, последние строки вывода `make test`, отклонения от плана и почему.
```

Файл: `.claude/agents/reviewer.md`
```markdown
---
name: reviewer
description: Ревьюит изменения одной задачи плана перед merge в develop — сверяет со спекой и чек-листом, код не правит. Используй после каждой завершённой задачи.
tools: Read, Grep, Glob, Bash
model: opus
---

Ты — ревьюер Go-кода проекта Loan Gateway. Код не правишь — только находишь проблемы.

Вход: номер задачи из `docs/plan.md`. Смотри дифф `git diff develop...HEAD`, задачу в плане и связанные разделы `docs/spec.md`.

Проверь:
1. Соответствие задаче и спеке: всё из задачи сделано, лишнего нет.
2. Идемпотентность: повтор POST, повторная отправка в банк, повтор вебхука.
3. Конкурентность: гонки, защита владения в `UPDATE bank_requests` (`status = 'sending' AND attempts = $n`), утечки горутин.
4. Ошибки банков классифицированы по разделу 6.2 спеки; отказ банка — не ошибка.
5. Деньги — только копейки и базисные пункты, без `float` вне аннуитета заглушки.
6. ПДн (ФИО, телефон, дата рождения) и тела запросов к банкам не попадают в логи и тексты ошибок.
7. Тесты проверяют поведение, а не реализацию; граничные случаи покрыты; нет хрупких зависимостей от `sleep` без нужды.
8. Запусти `make lint` и `make test`.

Ответ — список замечаний в формате `[blocker|major|minor] файл:строка — проблема — почему важно — как исправить`.
В конце — «Готово к merge: да/нет». Если замечаний нет, так и напиши.
```

- [ ] **Step 11: Создать журнал и README**

Файл: `docs/dev-log.md`
```markdown
# Журнал разработки

Строка на каждую завершённую задачу плана. Стоимость — для облачных сессий; для локальных — «—».

| Дата | Задача | Где | PR | Замечания ревью (исправлено / всего) | Стоимость |
|---|---|---|---|---|---|
```

Файл: `README.md`
```markdown
# Loan Gateway

Сервис-шлюз маркетплейса финансовых продуктов: принимает заявку на кредит наличными, рассылает её в банки-партнёры и собирает их решения.

- Спецификация: [docs/spec.md](docs/spec.md)
- План реализации: [docs/plan.md](docs/plan.md)
- Журнал разработки: [docs/dev-log.md](docs/dev-log.md)

Статус: в разработке (MVP v0.1.0).
```

- [ ] **Step 12: Commit**

```bash
git add .gitignore go.mod Makefile .golangci.yml .github cmd CLAUDE.md .claude docs/dev-log.md README.md
git commit -m "chore: bootstrap repository, CI and agent roles"
```

- [ ] **Step 13 (человек, после merge PR): защитить ветки**

В GitHub: Settings → Branches → Add branch protection rule — для `main` и для `develop`: «Require a pull request before merging» и «Require status checks to pass» с проверкой `check`. Роли агентов подхватываются при старте сессии — перезапустить Claude Code.

---

### Task 2: Домен заявки

**Ветка:** `feature/domain` · **Этап 1, локально** · **Спека:** разделы 4, 5.1

**Files:**
- Create: `internal/application/types.go`, `internal/application/validate.go`, `internal/application/hash.go`, `internal/application/service.go`
- Test: `internal/application/types_test.go`, `internal/application/validate_test.go`, `internal/application/hash_test.go`, `internal/application/service_test.go`

**Interfaces:**
- Consumes: —
- Produces (пакет `application`):
  - `type EmploymentType string`; `EmploymentEmployed`, `EmploymentSelfEmployed`, `EmploymentEntrepreneur`, `EmploymentPensioner`
  - `type Input struct{ FullName, BirthDate, Phone string; MonthlyIncome int64; EmploymentType EmploymentType; Amount int64; TermMonths int }` (JSON-теги из 4.2)
  - `type Application struct{ ID uuid.UUID; FullName string; BirthDate time.Time; Phone string; MonthlyIncome int64; EmploymentType EmploymentType; Amount int64; TermMonths int }`
  - `type Decision struct{ Approved bool; Amount int64; RateBPS, TermMonths int; MonthlyPayment int64; RejectReason string }`
  - `type RequestStatus string` с `RequestQueued`, `RequestSending`, `RequestWaitingCallback`, `RequestApproved`, `RequestRejected`, `RequestFailed`; `func (RequestStatus) Final() bool`
  - `type Status string` с `StatusProcessing`, `StatusCompleted`
  - `type BankResult struct{ BankCode string; Status RequestStatus; Attempts int; Decision *Decision; LastError string }`
  - `type View struct{ ID uuid.UUID; Amount int64; TermMonths int; CreatedAt time.Time; Banks []BankResult }`; `func (View) Status() Status`
  - `type CallbackResult int` с `CallbackApplied`, `CallbackDuplicate`, `CallbackUnknown`
  - `type FieldError struct{ Field, Message string }`; `type ValidationError struct{ Fields []FieldError }`
  - `func Validate(in Input, now time.Time) (Application, error)`; `func Age(birth, now time.Time) int`; `func RequestHash(in Input) []byte`
  - `var ErrNotFound, ErrIdempotencyConflict error`
  - `type Repository interface{ Create(ctx, key string, hash []byte, app Application, bankCodes []string) (uuid.UUID, []byte, error); Get(ctx, id uuid.UUID) (View, error) }`
  - `func NewService(repo Repository, bankCodes []string, now func() time.Time) *Service`; `(*Service) Create(ctx, key string, in Input) (View, error)`; `(*Service) Get(ctx, id uuid.UUID) (View, error)`

- [ ] **Step 1: Создать ветку и добавить зависимость**

```bash
git switch develop && git pull --ff-only && git switch -c feature/domain
go get github.com/google/uuid@v1.6.0
```

- [ ] **Step 2: Написать тест статусов**

Файл: `internal/application/types_test.go`
```go
package application

import "testing"

func TestRequestStatusFinal(t *testing.T) {
	want := map[RequestStatus]bool{
		RequestQueued:          false,
		RequestSending:         false,
		RequestWaitingCallback: false,
		RequestApproved:        true,
		RequestRejected:        true,
		RequestFailed:          true,
	}
	for status, final := range want {
		if got := status.Final(); got != final {
			t.Errorf("%s.Final() = %v, want %v", status, got, final)
		}
	}
}

func TestViewStatus(t *testing.T) {
	v := View{Banks: []BankResult{{Status: RequestApproved}, {Status: RequestWaitingCallback}}}
	if got := v.Status(); got != StatusProcessing {
		t.Fatalf("status = %s, want %s", got, StatusProcessing)
	}
	v.Banks[1].Status = RequestFailed
	if got := v.Status(); got != StatusCompleted {
		t.Fatalf("status = %s, want %s", got, StatusCompleted)
	}
}
```

- [ ] **Step 3: Запустить — тест должен упасть**

Run: `go test ./internal/application/`
Expected: FAIL — `undefined: RequestStatus` и подобные.

- [ ] **Step 4: Реализовать типы**

Файл: `internal/application/types.go`
```go
// Package application — домен заявки на кредит наличными: типы, валидация, сценарии.
package application

import (
	"time"

	"github.com/google/uuid"
)

// EmploymentType — тип занятости заявителя.
type EmploymentType string

const (
	EmploymentEmployed     EmploymentType = "employed"
	EmploymentSelfEmployed EmploymentType = "self_employed"
	EmploymentEntrepreneur EmploymentType = "entrepreneur"
	EmploymentPensioner    EmploymentType = "pensioner"
)

// Input — тело POST /v1/applications. Денежные поля — в копейках.
type Input struct {
	FullName       string         `json:"full_name"`
	BirthDate      string         `json:"birth_date"`
	Phone          string         `json:"phone"`
	MonthlyIncome  int64          `json:"monthly_income"`
	EmploymentType EmploymentType `json:"employment_type"`
	Amount         int64          `json:"amount"`
	TermMonths     int            `json:"term_months"`
}

// Application — провалидированная заявка.
type Application struct {
	ID             uuid.UUID
	FullName       string
	BirthDate      time.Time // полночь UTC
	Phone          string
	MonthlyIncome  int64
	EmploymentType EmploymentType
	Amount         int64
	TermMonths     int
}

// Decision — решение банка. При одобрении заполнены сумма, ставка, срок и платёж,
// при отказе — RejectReason.
type Decision struct {
	Approved       bool
	Amount         int64
	RateBPS        int
	TermMonths     int
	MonthlyPayment int64
	RejectReason   string
}

// RequestStatus — статус запроса в один банк (bank_requests.status).
type RequestStatus string

const (
	RequestQueued          RequestStatus = "queued"
	RequestSending         RequestStatus = "sending"
	RequestWaitingCallback RequestStatus = "waiting_callback"
	RequestApproved        RequestStatus = "approved"
	RequestRejected        RequestStatus = "rejected"
	RequestFailed          RequestStatus = "failed"
)

// Final сообщает, что статус финальный и больше не изменится.
func (s RequestStatus) Final() bool {
	return s == RequestApproved || s == RequestRejected || s == RequestFailed
}

// Status — сводный статус заявки. Не хранится, вычисляется по запросам в банки.
type Status string

const (
	StatusProcessing Status = "processing"
	StatusCompleted  Status = "completed"
)

// BankResult — состояние запроса в один банк.
type BankResult struct {
	BankCode  string
	Status    RequestStatus
	Attempts  int
	Decision  *Decision // для approved и rejected
	LastError string    // для failed
}

// View — заявка с результатами по банкам, отсортированными по BankCode.
type View struct {
	ID         uuid.UUID
	Amount     int64
	TermMonths int
	CreatedAt  time.Time
	Banks      []BankResult
}

// Status возвращает completed, если все запросы в банки финальные, иначе processing.
func (v View) Status() Status {
	for _, b := range v.Banks {
		if !b.Status.Final() {
			return StatusProcessing
		}
	}
	return StatusCompleted
}

// CallbackResult — итог применения вебхука банка.
type CallbackResult int

const (
	CallbackApplied   CallbackResult = iota + 1 // решение записано
	CallbackDuplicate                           // запрос уже не ждёт вебхук: повтор или опоздание
	CallbackUnknown                             // запроса с таким external_id нет
)
```

- [ ] **Step 5: Запустить — тест проходит**

Run: `go test ./internal/application/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/application/types.go internal/application/types_test.go
git commit -m "feat(application): add domain types and statuses"
```

- [ ] **Step 7: Написать тесты валидации**

Файл: `internal/application/validate_test.go`
```go
package application

import (
	"errors"
	"strings"
	"testing"
	"time"
)

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func validInput() Input {
	return Input{
		FullName:       "Иванов Иван Иванович",
		BirthDate:      "1990-05-17",
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

func TestValidateAcceptsValidInput(t *testing.T) {
	in := validInput()
	in.FullName = "  Иванов Иван Иванович  "
	app, err := Validate(in, testNow)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !app.BirthDate.Equal(time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC)) {
		t.Fatalf("birth date = %v", app.BirthDate)
	}
	app.BirthDate = time.Time{}
	want := Application{
		FullName:       "Иванов Иван Иванович",
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
	if app != want {
		t.Fatalf("got %+v, want %+v", app, want)
	}
}

func TestValidateRejectsInvalidFields(t *testing.T) {
	tests := []struct {
		name  string
		edit  func(*Input)
		field string
	}{
		{"blank name", func(in *Input) { in.FullName = "   " }, "full_name"},
		{"bad birth date format", func(in *Input) { in.BirthDate = "17.05.1990" }, "birth_date"},
		{"turns 18 tomorrow", func(in *Input) { in.BirthDate = "2008-10-14" }, "birth_date"},
		{"turned 76 today", func(in *Input) { in.BirthDate = "1950-10-13" }, "birth_date"},
		{"phone without +7", func(in *Input) { in.Phone = "89991234567" }, "phone"},
		{"phone too short", func(in *Input) { in.Phone = "+7999123456" }, "phone"},
		{"zero income", func(in *Input) { in.MonthlyIncome = 0 }, "monthly_income"},
		{"unknown employment", func(in *Input) { in.EmploymentType = "student" }, "employment_type"},
		{"amount in rubles instead of kopecks", func(in *Input) { in.Amount = 300_000 }, "amount"},
		{"amount above limit", func(in *Input) { in.Amount = 500_000_001 }, "amount"},
		{"term too short", func(in *Input) { in.TermMonths = 5 }, "term_months"},
		{"term too long", func(in *Input) { in.TermMonths = 61 }, "term_months"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := validInput()
			tt.edit(&in)
			_, err := Validate(in, testNow)
			var verr *ValidationError
			if !errors.As(err, &verr) {
				t.Fatalf("want *ValidationError, got %v", err)
			}
			if len(verr.Fields) != 1 || verr.Fields[0].Field != tt.field {
				t.Fatalf("want one error for %q, got %+v", tt.field, verr.Fields)
			}
		})
	}
}

func TestValidateReportsAllInvalidFields(t *testing.T) {
	_, err := Validate(Input{}, testNow)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	want := []string{"full_name", "birth_date", "phone", "monthly_income", "employment_type", "amount", "term_months"}
	if len(verr.Fields) != len(want) {
		t.Fatalf("got %d errors %+v, want %d", len(verr.Fields), verr.Fields, len(want))
	}
	for i, f := range verr.Fields {
		if f.Field != want[i] {
			t.Errorf("error %d is for %q, want %q", i, f.Field, want[i])
		}
	}
}

func TestValidateAgeBoundaries(t *testing.T) {
	for _, birth := range []string{"2008-10-13", "1950-10-14"} { // ровно 18 и ровно 75
		in := validInput()
		in.BirthDate = birth
		if _, err := Validate(in, testNow); err != nil {
			t.Errorf("birth date %s: unexpected error %v", birth, err)
		}
	}
}

func TestValidateCountsRunes(t *testing.T) {
	in := validInput()
	in.FullName = strings.Repeat("я", 200)
	if _, err := Validate(in, testNow); err != nil {
		t.Fatalf("200 cyrillic letters must be accepted: %v", err)
	}
	in.FullName = strings.Repeat("я", 201)
	if _, err := Validate(in, testNow); err == nil {
		t.Fatal("201 cyrillic letters must be rejected")
	}
}

func TestAge(t *testing.T) {
	tests := []struct {
		birth, now string
		want       int
	}{
		{"1990-05-17", "2026-05-16", 35},
		{"1990-05-17", "2026-05-17", 36},
		{"2000-02-29", "2026-02-28", 25},
		{"2000-02-29", "2026-03-01", 26},
	}
	for _, tt := range tests {
		birth, _ := time.Parse(time.DateOnly, tt.birth)
		now, _ := time.Parse(time.DateOnly, tt.now)
		if got := Age(birth, now); got != tt.want {
			t.Errorf("Age(%s, %s) = %d, want %d", tt.birth, tt.now, got, tt.want)
		}
	}
}
```

- [ ] **Step 8: Запустить — тесты должны упасть**

Run: `go test ./internal/application/`
Expected: FAIL — `undefined: Validate`, `undefined: ValidationError`, `undefined: Age`.

- [ ] **Step 9: Реализовать валидацию**

Файл: `internal/application/validate.go`
```go
package application

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"
)

// Правила раздела 4.2 спеки.
const (
	minAmount    = 3_000_000   // 30 000 ₽
	maxAmount    = 500_000_000 // 5 000 000 ₽
	minTerm      = 6
	maxTerm      = 60
	minAge       = 18
	maxAge       = 75
	maxNameRunes = 200
)

var phoneRe = regexp.MustCompile(`^\+7\d{10}$`)

// FieldError — ошибка в одном поле входа.
type FieldError struct {
	Field   string `json:"field"`
	Message string `json:"message"`
}

// ValidationError — все ошибки валидации входа.
type ValidationError struct {
	Fields []FieldError
}

func (e *ValidationError) Error() string {
	return fmt.Sprintf("validation failed: %d invalid field(s)", len(e.Fields))
}

// Validate проверяет вход и возвращает заявку без ID.
// now — момент приёма заявки; возраст считается на эту дату в UTC.
func Validate(in Input, now time.Time) (Application, error) {
	var errs []FieldError
	add := func(field, msg string) {
		errs = append(errs, FieldError{Field: field, Message: msg})
	}

	name := strings.TrimSpace(in.FullName)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameRunes {
		add("full_name", fmt.Sprintf("must be 1-%d characters", maxNameRunes))
	}
	birth, err := time.Parse(time.DateOnly, in.BirthDate)
	if err != nil {
		add("birth_date", "must be a date in YYYY-MM-DD format")
	} else if age := Age(birth, now); age < minAge || age > maxAge {
		add("birth_date", fmt.Sprintf("age must be between %d and %d", minAge, maxAge))
	}
	if !phoneRe.MatchString(in.Phone) {
		add("phone", "must match +7XXXXXXXXXX")
	}
	if in.MonthlyIncome <= 0 {
		add("monthly_income", "must be positive (kopecks)")
	}
	switch in.EmploymentType {
	case EmploymentEmployed, EmploymentSelfEmployed, EmploymentEntrepreneur, EmploymentPensioner:
	default:
		add("employment_type", "must be one of employed, self_employed, entrepreneur, pensioner")
	}
	if in.Amount < minAmount || in.Amount > maxAmount {
		add("amount", fmt.Sprintf("must be between %d and %d (kopecks)", minAmount, maxAmount))
	}
	if in.TermMonths < minTerm || in.TermMonths > maxTerm {
		add("term_months", fmt.Sprintf("must be between %d and %d", minTerm, maxTerm))
	}
	if len(errs) > 0 {
		return Application{}, &ValidationError{Fields: errs}
	}
	return Application{
		FullName:       name,
		BirthDate:      birth,
		Phone:          in.Phone,
		MonthlyIncome:  in.MonthlyIncome,
		EmploymentType: in.EmploymentType,
		Amount:         in.Amount,
		TermMonths:     in.TermMonths,
	}, nil
}

// Age возвращает число полных лет на дату now; обе даты берутся в UTC.
func Age(birth, now time.Time) int {
	birth, now = birth.UTC(), now.UTC()
	age := now.Year() - birth.Year()
	if now.Month() < birth.Month() || (now.Month() == birth.Month() && now.Day() < birth.Day()) {
		age--
	}
	return age
}
```

- [ ] **Step 10: Запустить — тесты проходят**

Run: `go test ./internal/application/`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/application/validate.go internal/application/validate_test.go
git commit -m "feat(application): validate loan application input"
```

- [ ] **Step 12: Написать тест хеша запроса**

Файл: `internal/application/hash_test.go`
```go
package application

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestRequestHashIgnoresFieldOrderAndWhitespace(t *testing.T) {
	compact := `{"full_name":"Иван","birth_date":"1990-05-17","phone":"+79991234567","monthly_income":1,"employment_type":"employed","amount":2,"term_months":6}`
	reordered := `{
		"term_months": 6, "amount": 2, "employment_type": "employed",
		"monthly_income": 1, "phone": "+79991234567", "birth_date": "1990-05-17", "full_name": "Иван"
	}`
	var a, b Input
	if err := json.Unmarshal([]byte(compact), &a); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(reordered), &b); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(RequestHash(a), RequestHash(b)) {
		t.Fatal("hashes differ for the same request")
	}
	if len(RequestHash(a)) != 32 {
		t.Fatalf("hash length = %d, want 32", len(RequestHash(a)))
	}
}

func TestRequestHashDependsOnValues(t *testing.T) {
	in := validInput()
	other := in
	other.Amount++
	if bytes.Equal(RequestHash(in), RequestHash(other)) {
		t.Fatal("different requests have the same hash")
	}
}
```

- [ ] **Step 13: Запустить — тест должен упасть**

Run: `go test ./internal/application/`
Expected: FAIL — `undefined: RequestHash`.

- [ ] **Step 14: Реализовать хеш**

Файл: `internal/application/hash.go`
```go
package application

import (
	"crypto/sha256"
	"encoding/json"
)

// RequestHash — SHA-256 канонического JSON входа. Вход уже декодирован в структуру,
// поэтому порядок полей и пробелы исходного тела на хеш не влияют.
func RequestHash(in Input) []byte {
	// Input состоит из строк и чисел — Marshal не возвращает ошибку.
	b, _ := json.Marshal(in)
	sum := sha256.Sum256(b)
	return sum[:]
}
```

- [ ] **Step 15: Запустить — тесты проходят**

Run: `go test ./internal/application/`
Expected: PASS

- [ ] **Step 16: Commit**

```bash
git add internal/application/hash.go internal/application/hash_test.go
git commit -m "feat(application): add canonical request hash"
```

- [ ] **Step 17: Написать тесты сервиса**

Файл: `internal/application/service_test.go`
```go
package application

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

type storedApp struct {
	id   uuid.UUID
	hash []byte
}

// fakeRepo — хранилище заявок в памяти.
type fakeRepo struct {
	byKey map[string]storedApp
	views map[uuid.UUID]View
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{byKey: map[string]storedApp{}, views: map[uuid.UUID]View{}}
}

func (f *fakeRepo) Create(_ context.Context, key string, hash []byte, app Application, bankCodes []string) (uuid.UUID, []byte, error) {
	if s, ok := f.byKey[key]; ok {
		return s.id, s.hash, nil
	}
	f.byKey[key] = storedApp{id: app.ID, hash: hash}
	banks := make([]BankResult, len(bankCodes))
	for i, code := range bankCodes {
		banks[i] = BankResult{BankCode: code, Status: RequestQueued}
	}
	f.views[app.ID] = View{ID: app.ID, Amount: app.Amount, TermMonths: app.TermMonths, Banks: banks}
	return app.ID, hash, nil
}

func (f *fakeRepo) Get(_ context.Context, id uuid.UUID) (View, error) {
	v, ok := f.views[id]
	if !ok {
		return View{}, ErrNotFound
	}
	return v, nil
}

func newTestService(repo Repository) *Service {
	return NewService(repo, []string{"eastbank", "northbank"}, func() time.Time { return testNow })
}

func TestServiceCreate(t *testing.T) {
	v, err := newTestService(newFakeRepo()).Create(t.Context(), "key-1", validInput())
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if v.ID == uuid.Nil {
		t.Fatal("id is not set")
	}
	if v.Status() != StatusProcessing || len(v.Banks) != 2 {
		t.Fatalf("got status %s with %d banks, want processing with 2", v.Status(), len(v.Banks))
	}
}

func TestServiceCreateIsIdempotent(t *testing.T) {
	svc := newTestService(newFakeRepo())
	first, err := svc.Create(t.Context(), "key-1", validInput())
	if err != nil {
		t.Fatal(err)
	}
	second, err := svc.Create(t.Context(), "key-1", validInput())
	if err != nil {
		t.Fatal(err)
	}
	if first.ID != second.ID {
		t.Fatalf("repeat returned %s, want %s", second.ID, first.ID)
	}
}

func TestServiceCreateConflict(t *testing.T) {
	svc := newTestService(newFakeRepo())
	if _, err := svc.Create(t.Context(), "key-1", validInput()); err != nil {
		t.Fatal(err)
	}
	other := validInput()
	other.Amount += 100
	_, err := svc.Create(t.Context(), "key-1", other)
	if !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestServiceCreateValidatesBeforeSaving(t *testing.T) {
	repo := newFakeRepo()
	in := validInput()
	in.TermMonths = 0
	_, err := newTestService(repo).Create(t.Context(), "key-1", in)
	var verr *ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want *ValidationError, got %v", err)
	}
	if len(repo.byKey) != 0 {
		t.Fatal("invalid application was saved")
	}
}

func TestServiceGetNotFound(t *testing.T) {
	_, err := newTestService(newFakeRepo()).Get(t.Context(), uuid.New())
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 18: Запустить — тесты должны упасть**

Run: `go test ./internal/application/`
Expected: FAIL — `undefined: Repository`, `undefined: NewService`, `undefined: ErrNotFound`.

- [ ] **Step 19: Реализовать сервис**

Файл: `internal/application/service.go`
```go
package application

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrNotFound            = errors.New("application not found")
	ErrIdempotencyConflict = errors.New("idempotency key already used with a different request")
)

// Repository — хранилище заявок; реализация — store/postgres.Applications.
type Repository interface {
	// Create в одной транзакции сохраняет заявку и по запросу в статусе queued на каждый банк.
	// Если ключ идемпотентности уже занят, ничего не пишет и возвращает id и хеш сохранённой заявки.
	Create(ctx context.Context, key string, hash []byte, app Application, bankCodes []string) (id uuid.UUID, storedHash []byte, err error)
	// Get возвращает заявку с результатами по банкам или ErrNotFound.
	Get(ctx context.Context, id uuid.UUID) (View, error)
}

// Service — сценарии приёма и чтения заявок.
type Service struct {
	repo      Repository
	bankCodes []string
	now       func() time.Time
}

// NewService создаёт сервис; bankCodes — банки, в которые уходит каждая новая заявка.
func NewService(repo Repository, bankCodes []string, now func() time.Time) *Service {
	return &Service{repo: repo, bankCodes: bankCodes, now: now}
}

// Create валидирует вход и идемпотентно создаёт заявку. Повтор с тем же ключом и телом
// возвращает исходную заявку, с тем же ключом и другим телом — ErrIdempotencyConflict.
func (s *Service) Create(ctx context.Context, key string, in Input) (View, error) {
	app, err := Validate(in, s.now())
	if err != nil {
		return View{}, err
	}
	app.ID, err = uuid.NewV7()
	if err != nil {
		return View{}, fmt.Errorf("generate id: %w", err)
	}
	hash := RequestHash(in)
	id, stored, err := s.repo.Create(ctx, key, hash, app, s.bankCodes)
	if err != nil {
		return View{}, fmt.Errorf("create application: %w", err)
	}
	if !bytes.Equal(stored, hash) {
		return View{}, ErrIdempotencyConflict
	}
	return s.repo.Get(ctx, id)
}

// Get возвращает заявку или ErrNotFound.
func (s *Service) Get(ctx context.Context, id uuid.UUID) (View, error) {
	return s.repo.Get(ctx, id)
}
```

- [ ] **Step 20: Запустить — тесты проходят**

Run: `go test ./internal/application/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 21: Commit**

```bash
git add internal/application/service.go internal/application/service_test.go
git commit -m "feat(application): add idempotent create and get service"
```

---

### Task 3: Интерфейсы банков, классы ошибок, реестр

**Ветка:** `feature/bank-core` · **Этап 1, локально** · **Спека:** разделы 6.1, 6.2

**Files:**
- Create: `internal/bank/bank.go`, `internal/bank/httpjson.go`, `internal/bank/registry.go`
- Test: `internal/bank/bank_test.go`, `internal/bank/registry_test.go`

**Interfaces:**
- Consumes: `application.Application`, `application.Decision` (Task 2)
- Produces (пакет `bank`):
  - `type SubmitRequest struct{ RequestID uuid.UUID; Application application.Application }`
  - `type SubmitResult struct{ Decision *application.Decision; ExternalID string }`
  - `type Bank interface{ Code() string; Submit(ctx, SubmitRequest) (SubmitResult, error) }`
  - `type CallbackHandler interface{ ParseCallback(body []byte) (externalID string, d application.Decision, err error) }`
  - `var ErrTemporary, ErrPermanent error`; `func IsPermanent(err error) bool`; `func StatusError(code int) error`
  - `func PostJSON(ctx, c *http.Client, url string, headers map[string]string, body any) (int, []byte, error)`
  - `type Registry`; `func NewRegistry(banks ...Bank) (*Registry, error)`; `(*Registry) Get(code string) (Bank, bool)`; `(*Registry) Codes() []string`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/bank-core
```

- [ ] **Step 2: Написать тесты классов ошибок и PostJSON**

Файл: `internal/bank/bank_test.go`
```go
package bank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestStatusErrorClasses(t *testing.T) {
	for _, code := range []int{429, 500, 502, 503} {
		err := StatusError(code)
		if !errors.Is(err, ErrTemporary) || IsPermanent(err) {
			t.Errorf("status %d: want temporary error, got %v", code, err)
		}
	}
	for _, code := range []int{400, 404, 409, 422} {
		if err := StatusError(code); !IsPermanent(err) {
			t.Errorf("status %d: want permanent error, got %v", code, err)
		}
	}
}

func TestIsPermanentTreatsUnclassifiedAsTemporary(t *testing.T) {
	if IsPermanent(errors.New("boom")) {
		t.Fatal("unclassified error must be temporary")
	}
	if !IsPermanent(fmt.Errorf("wrapped: %w", ErrPermanent)) {
		t.Fatal("wrapped permanent error must stay permanent")
	}
}

func TestPostJSONSendsBodyAndHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("X-Key") != "k-1" {
			t.Errorf("unexpected request: %s %v", r.Method, r.Header)
		}
		var got map[string]int
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil || got["a"] != 1 {
			t.Errorf("unexpected body %v (%v)", got, err)
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	status, body, err := PostJSON(t.Context(), srv.Client(), srv.URL, map[string]string{"X-Key": "k-1"}, map[string]int{"a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusCreated || string(body) != `{"ok":true}` {
		t.Fatalf("got %d %q", status, body)
	}
}

func TestPostJSONNetworkErrorIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	_, _, err := PostJSON(t.Context(), http.DefaultClient, url, nil, struct{}{})
	if !errors.Is(err, ErrTemporary) {
		t.Fatalf("want temporary error, got %v", err)
	}
}

func TestPostJSONTimeoutIsTemporary(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(time.Second):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	_, _, err := PostJSON(ctx, srv.Client(), srv.URL, nil, struct{}{})
	if !errors.Is(err, ErrTemporary) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("want temporary deadline error, got %v", err)
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/`
Expected: FAIL — `undefined: StatusError`, `undefined: PostJSON`.

- [ ] **Step 4: Реализовать интерфейсы, классы ошибок и PostJSON**

Файл: `internal/bank/bank.go`
```go
// Package bank — интеграция с банками-партнёрами: интерфейсы адаптеров, классы ошибок,
// реестр, конвертация денег и подпись вебхуков. Адаптеры живут в подпакетах.
package bank

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// SubmitRequest — заявка для отправки в банк.
type SubmitRequest struct {
	RequestID   uuid.UUID // bank_requests.id — ключ идемпотентности для банка
	Application application.Application
}

// SubmitResult — ответ банка: либо синхронное решение, либо id заявки в банке
// для асинхронного решения через вебхук.
type SubmitResult struct {
	Decision   *application.Decision
	ExternalID string
}

// Bank — адаптер одного банка.
type Bank interface {
	Code() string
	Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
}

// CallbackHandler реализуют асинхронные банки: разбор тела вебхука с решением.
type CallbackHandler interface {
	ParseCallback(body []byte) (externalID string, d application.Decision, err error)
}

var (
	// ErrTemporary — сбой, после которого запрос стоит повторить.
	ErrTemporary = errors.New("temporary bank error")
	// ErrPermanent — повтор не поможет, запрос сразу failed.
	ErrPermanent = errors.New("permanent bank error")
)

// IsPermanent сообщает, что ошибка постоянная. Ошибка без класса считается временной.
func IsPermanent(err error) bool {
	return errors.Is(err, ErrPermanent)
}

// StatusError возвращает ошибку нужного класса для неуспешного HTTP-статуса банка:
// 429 и 5xx — временные, остальные — постоянные.
func StatusError(code int) error {
	if code == http.StatusTooManyRequests || code >= 500 {
		return fmt.Errorf("%w: unexpected status %d", ErrTemporary, code)
	}
	return fmt.Errorf("%w: unexpected status %d", ErrPermanent, code)
}
```

Файл: `internal/bank/httpjson.go`
```go
package bank

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxResponseBytes = 1 << 20

// PostJSON отправляет body как JSON и возвращает статус и тело ответа.
// Сетевые ошибки и таймауты возвращаются как ErrTemporary.
func PostJSON(ctx context.Context, c *http.Client, url string, headers map[string]string, body any) (int, []byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: encode request: %w", ErrPermanent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: build request: %w", ErrPermanent, err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("%w: %w", ErrTemporary, err)
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return 0, nil, fmt.Errorf("%w: read response: %w", ErrTemporary, err)
	}
	return resp.StatusCode, data, nil
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/bank/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bank/bank.go internal/bank/httpjson.go internal/bank/bank_test.go
git commit -m "feat(bank): add adapter interfaces, error classes and PostJSON"
```

- [ ] **Step 7: Написать тесты реестра**

Файл: `internal/bank/registry_test.go`
```go
package bank

import (
	"context"
	"slices"
	"testing"
)

type stubBank string

func (b stubBank) Code() string { return string(b) }

func (stubBank) Submit(context.Context, SubmitRequest) (SubmitResult, error) {
	return SubmitResult{}, nil
}

func TestRegistry(t *testing.T) {
	reg, err := NewRegistry(stubBank("westbank"), stubBank("northbank"))
	if err != nil {
		t.Fatal(err)
	}
	if got := reg.Codes(); !slices.Equal(got, []string{"northbank", "westbank"}) {
		t.Fatalf("codes = %v, want sorted", got)
	}
	if b, ok := reg.Get("northbank"); !ok || b.Code() != "northbank" {
		t.Fatalf("Get(northbank) = %v, %v", b, ok)
	}
	if _, ok := reg.Get("eastbank"); ok {
		t.Fatal("Get(eastbank) must report a missing bank")
	}
}

func TestRegistryRejectsDuplicatesAndEmpty(t *testing.T) {
	if _, err := NewRegistry(stubBank("northbank"), stubBank("northbank")); err == nil {
		t.Fatal("duplicate codes must be rejected")
	}
	if _, err := NewRegistry(); err == nil {
		t.Fatal("empty registry must be rejected")
	}
}
```

- [ ] **Step 8: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/`
Expected: FAIL — `undefined: NewRegistry`.

- [ ] **Step 9: Реализовать реестр**

Файл: `internal/bank/registry.go`
```go
package bank

import (
	"errors"
	"fmt"
	"maps"
	"slices"
)

// Registry — включённые банки по коду.
type Registry struct {
	banks map[string]Bank
}

// NewRegistry проверяет, что банков хотя бы один и коды не повторяются.
func NewRegistry(banks ...Bank) (*Registry, error) {
	if len(banks) == 0 {
		return nil, errors.New("no banks configured: set BANK_<CODE>_URL")
	}
	r := &Registry{banks: make(map[string]Bank, len(banks))}
	for _, b := range banks {
		if _, dup := r.banks[b.Code()]; dup {
			return nil, fmt.Errorf("duplicate bank code %q", b.Code())
		}
		r.banks[b.Code()] = b
	}
	return r, nil
}

// Get возвращает адаптер банка по коду.
func (r *Registry) Get(code string) (Bank, bool) {
	b, ok := r.banks[code]
	return b, ok
}

// Codes возвращает коды банков по алфавиту.
func (r *Registry) Codes() []string {
	return slices.Sorted(maps.Keys(r.banks))
}
```

- [ ] **Step 10: Запустить — тесты проходят**

Run: `go test ./internal/bank/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 11: Commit**

```bash
git add internal/bank/registry.go internal/bank/registry_test.go
git commit -m "feat(bank): add bank registry"
```

---

### Task 4: Деньги строкой и подпись вебхуков

**Ветка:** `feature/bank-money-webhook` · **Этап 1, локально** · **Спека:** разделы 4.1, 5.3

**Files:**
- Create: `internal/bank/money.go`, `internal/bank/webhook.go`
- Test: `internal/bank/money_test.go`, `internal/bank/webhook_test.go`

**Interfaces:**
- Consumes: —
- Produces (пакет `bank`):
  - `func ParseRubles(s string) (int64, error)`; `func FormatRubles(kopecks int64) string`
  - `func ParsePercent(s string) (int, error)`; `func FormatPercent(bps int) string`
  - `const HeaderWebhookTimestamp = "X-Webhook-Timestamp"`, `HeaderWebhookSignature = "X-Webhook-Signature"`
  - `var ErrBadSignature error`
  - `func SignWebhook(secret []byte, timestamp string, body []byte) string` — значение заголовка `sha256=<hex>`
  - `func VerifyWebhook(secret []byte, timestamp, signature string, body []byte, now time.Time, maxSkew time.Duration) error`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/bank-money-webhook
```

- [ ] **Step 2: Написать тесты конвертации денег**

Файл: `internal/bank/money_test.go`
```go
package bank

import "testing"

func TestParseRubles(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"300000.00", 30_000_000},
		{"300000", 30_000_000},
		{"300000.5", 30_000_050},
		{"0.05", 5},
		{"14674.60", 1_467_460},
	}
	for _, tt := range tests {
		got, err := ParseRubles(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParseRubles(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "-1.00", "1.234", "1.", ".5", "1,00", " 1.00", "1e3", "abc", "1234567890123456"} {
		if _, err := ParseRubles(bad); err == nil {
			t.Errorf("ParseRubles(%q) must fail", bad)
		}
	}
}

func TestFormatRubles(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{30_000_000, "300000.00"},
		{30_000_050, "300000.50"},
		{5, "0.05"},
		{0, "0.00"},
		{1_467_460, "14674.60"},
	}
	for _, tt := range tests {
		if got := FormatRubles(tt.in); got != tt.want {
			t.Errorf("FormatRubles(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestParsePercent(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"15.90", 1590},
		{"15.9", 1590},
		{"15", 1500},
		{"0.5", 50},
	}
	for _, tt := range tests {
		got, err := ParsePercent(tt.in)
		if err != nil || got != tt.want {
			t.Errorf("ParsePercent(%q) = %d, %v; want %d", tt.in, got, err, tt.want)
		}
	}
	for _, bad := range []string{"", "15.901", "-1", "15%"} {
		if _, err := ParsePercent(bad); err == nil {
			t.Errorf("ParsePercent(%q) must fail", bad)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	for bps, want := range map[int]string{1590: "15.90", 1490: "14.90", 50: "0.50"} {
		if got := FormatPercent(bps); got != want {
			t.Errorf("FormatPercent(%d) = %q, want %q", bps, got, want)
		}
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/`
Expected: FAIL — `undefined: ParseRubles` и т. п.

- [ ] **Step 4: Реализовать конвертацию**

Файл: `internal/bank/money.go`
```go
package bank

import (
	"fmt"
	"strconv"
	"strings"
)

// ParseRubles переводит рубли строкой ("300000.00", "300000.5", "300000") в копейки без float.
func ParseRubles(s string) (int64, error) {
	return parseFixed2(s)
}

// FormatRubles переводит копейки (≥ 0) в рубли строкой с двумя знаками: 30000050 → "300000.50".
func FormatRubles(kopecks int64) string {
	return fmt.Sprintf("%d.%02d", kopecks/100, kopecks%100)
}

// ParsePercent переводит проценты строкой ("15.90", "15.9", "15") в базисные пункты.
func ParsePercent(s string) (int, error) {
	n, err := parseFixed2(s)
	return int(n), err
}

// FormatPercent переводит базисные пункты в проценты строкой с двумя знаками: 1590 → "15.90".
func FormatPercent(bps int) string {
	return fmt.Sprintf("%d.%02d", bps/100, bps%100)
}

// parseFixed2 разбирает неотрицательное десятичное число с 0–2 знаками после точки
// и возвращает его, умноженное на 100.
func parseFixed2(s string) (int64, error) {
	intPart, frac, hasDot := strings.Cut(s, ".")
	if intPart == "" || len(intPart) > 15 || !allDigits(intPart) {
		return 0, fmt.Errorf("invalid decimal %q", s)
	}
	if hasDot && (frac == "" || len(frac) > 2 || !allDigits(frac)) {
		return 0, fmt.Errorf("invalid decimal %q", s)
	}
	for len(frac) < 2 {
		frac += "0"
	}
	n, err := strconv.ParseInt(intPart+frac, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid decimal %q: %w", s, err)
	}
	return n, nil
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/bank/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/bank/money.go internal/bank/money_test.go
git commit -m "feat(bank): convert rubles and percent strings without float"
```

- [ ] **Step 7: Написать тесты подписи вебхуков**

Файл: `internal/bank/webhook_test.go`
```go
package bank

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

var testSecret = []byte("test-secret")

func TestSignWebhookFormat(t *testing.T) {
	mac := hmac.New(sha256.New, testSecret)
	mac.Write([]byte("1760000000.{}"))
	want := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if got := SignWebhook(testSecret, "1760000000", []byte("{}")); got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
}

func TestVerifyWebhook(t *testing.T) {
	now := time.Unix(1_760_000_000, 0)
	ts := "1760000000"
	body := []byte(`{"application_id": "w-1",  "result":"approved"}`) // пробелы сохраняются: подписывается сырое тело
	sig := SignWebhook(testSecret, ts, body)

	tests := []struct {
		name    string
		secret  []byte
		ts, sig string
		body    []byte
		now     time.Time
		wantErr bool
	}{
		{"valid", testSecret, ts, sig, body, now, false},
		{"valid within skew", testSecret, ts, sig, body, now.Add(4 * time.Minute), false},
		{"wrong secret", []byte("other"), ts, sig, body, now, true},
		{"re-encoded body", testSecret, ts, sig, []byte(`{"application_id":"w-1","result":"approved"}`), now, true},
		{"stale timestamp", testSecret, ts, sig, body, now.Add(6 * time.Minute), true},
		{"timestamp from future", testSecret, ts, sig, body, now.Add(-6 * time.Minute), true},
		{"missing signature", testSecret, ts, "", body, now, true},
		{"missing timestamp", testSecret, "", sig, body, now, true},
		{"bad timestamp", testSecret, "abc", sig, body, now, true},
		{"timestamp is part of signature", testSecret, "1760000001", sig, body, now, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := VerifyWebhook(tt.secret, tt.ts, tt.sig, tt.body, tt.now, 5*time.Minute)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if err != nil && !errors.Is(err, ErrBadSignature) {
				t.Fatalf("err = %v, want ErrBadSignature", err)
			}
		})
	}
}
```

- [ ] **Step 8: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/`
Expected: FAIL — `undefined: SignWebhook`, `undefined: VerifyWebhook`.

- [ ] **Step 9: Реализовать подпись**

Файл: `internal/bank/webhook.go`
```go
package bank

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// Заголовки вебхука банка (раздел 5.3 спеки).
const (
	HeaderWebhookTimestamp = "X-Webhook-Timestamp"
	HeaderWebhookSignature = "X-Webhook-Signature"
)

// ErrBadSignature — подпись вебхука отсутствует, неверна или устарела.
var ErrBadSignature = errors.New("bad webhook signature")

// SignWebhook возвращает значение заголовка X-Webhook-Signature:
// "sha256=" + hex(HMAC-SHA256(secret, timestamp + "." + body)).
func SignWebhook(secret []byte, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(timestamp))
	mac.Write([]byte("."))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// VerifyWebhook проверяет подпись сырого тела и то, что timestamp (unix-секунды)
// отличается от now не больше чем на maxSkew. Подписи сравниваются за постоянное время.
func VerifyWebhook(secret []byte, timestamp, signature string, body []byte, now time.Time, maxSkew time.Duration) error {
	if timestamp == "" || signature == "" {
		return fmt.Errorf("%w: missing headers", ErrBadSignature)
	}
	sec, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return fmt.Errorf("%w: bad timestamp", ErrBadSignature)
	}
	skew := now.Sub(time.Unix(sec, 0))
	if skew < 0 {
		skew = -skew
	}
	if skew > maxSkew {
		return fmt.Errorf("%w: timestamp outside allowed window", ErrBadSignature)
	}
	want := SignWebhook(secret, timestamp, body)
	if !hmac.Equal([]byte(want), []byte(signature)) {
		return fmt.Errorf("%w: signature mismatch", ErrBadSignature)
	}
	return nil
}
```

- [ ] **Step 10: Запустить — тесты проходят**

Run: `go test ./internal/bank/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 11: Commit**

```bash
git add internal/bank/webhook.go internal/bank/webhook_test.go
git commit -m "feat(bank): sign and verify bank webhooks with HMAC"
```

---

### Task 5: Хранилище заявок в PostgreSQL

**Ветка:** `feature/store-applications` · **Этап 1, локально** · **Спека:** разделы 5.1, 7

**Files:**
- Create: `internal/store/postgres/migrations/00001_init.sql`, `internal/store/postgres/postgres.go`, `internal/store/postgres/applications.go`, `internal/store/postgres/pgtest/pgtest.go`
- Test: `internal/store/postgres/helpers_test.go`, `internal/store/postgres/applications_test.go`

**Interfaces:**
- Consumes: `application.Application`, `application.View`, `application.BankResult`, `application.Decision`, `application.ErrNotFound`, интерфейс `application.Repository` (Task 2)
- Produces:
  - пакет `postgres`: `func Connect(ctx, databaseURL string) (*pgxpool.Pool, error)`; `func Migrate(ctx, databaseURL string) error`; `type Applications`; `func NewApplications(pool *pgxpool.Pool) *Applications` — реализует `application.Repository`
  - пакет `pgtest`: `func NewPool(t *testing.T) *pgxpool.Pool` — отдельная БД с миграциями в общем на процесс контейнере PostgreSQL; тест пропускается при `-short`
  - тестовые помощники пакета `postgres_test`: `newApp() application.Application`, `count(t, pool, query, args...) int`, `exec(t, pool, query, args...)`

- [ ] **Step 1: Создать ветку и добавить зависимости**

```bash
git switch develop && git pull --ff-only && git switch -c feature/store-applications
go get github.com/jackc/pgx/v5@v5.11.0 github.com/pressly/goose/v3@v3.29.0 \
  github.com/testcontainers/testcontainers-go@v0.44.0 \
  github.com/testcontainers/testcontainers-go/modules/postgres@v0.44.0
```

- [ ] **Step 2: Создать миграцию** (схема — раздел 7 спеки)

Файл: `internal/store/postgres/migrations/00001_init.sql`
```sql
-- +goose Up
CREATE TABLE applications (
    id              uuid        PRIMARY KEY,
    idempotency_key text        NOT NULL UNIQUE,
    request_hash    bytea       NOT NULL,
    full_name       text        NOT NULL,
    birth_date      date        NOT NULL,
    phone           text        NOT NULL,
    monthly_income  bigint      NOT NULL,
    employment_type text        NOT NULL,
    amount          bigint      NOT NULL,
    term_months     int         NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE bank_requests (
    id                uuid        PRIMARY KEY,
    application_id    uuid        NOT NULL REFERENCES applications (id),
    bank_code         text        NOT NULL,
    status            text        NOT NULL CHECK (status IN
                          ('queued', 'sending', 'waiting_callback', 'approved', 'rejected', 'failed')),
    attempts          int         NOT NULL DEFAULT 0,
    next_attempt_at   timestamptz NOT NULL DEFAULT now(),
    locked_until      timestamptz,
    external_id       text,
    callback_deadline timestamptz,
    approved_amount   bigint,
    rate_bps          int,
    approved_term     int,
    monthly_payment   bigint,
    reject_reason     text,
    last_error        text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (application_id, bank_code),
    UNIQUE (bank_code, external_id)
);

CREATE INDEX bank_requests_queued_idx  ON bank_requests (next_attempt_at)   WHERE status = 'queued';
CREATE INDEX bank_requests_sending_idx ON bank_requests (locked_until)      WHERE status = 'sending';
CREATE INDEX bank_requests_waiting_idx ON bank_requests (callback_deadline) WHERE status = 'waiting_callback';

-- +goose Down
DROP TABLE bank_requests;
DROP TABLE applications;
```

- [ ] **Step 3: Подключение и миграции**

Файл: `internal/store/postgres/postgres.go`
```go
// Package postgres — хранилище шлюза в PostgreSQL: миграции, заявки, очередь запросов в банки.
package postgres

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // драйвер database/sql "pgx" для goose
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Connect открывает пул соединений и проверяет доступность БД.
func Connect(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping database: %w", err)
	}
	return pool, nil
}

// Migrate применяет все миграции goose, встроенные в бинарник.
func Migrate(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer db.Close()
	fsys, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, fsys)
	if err != nil {
		return fmt.Errorf("goose provider: %w", err)
	}
	if _, err := provider.Up(ctx); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: PostgreSQL в Docker для тестов**

Файл: `internal/store/postgres/pgtest/pgtest.go`
```go
// Package pgtest поднимает PostgreSQL в Docker для интеграционных тестов.
package pgtest

import (
	"context"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

var (
	once     sync.Once
	adminURL string
	startErr error
	seq      atomic.Int64
)

// NewPool создаёт отдельную БД с применёнными миграциями в общем для процесса тестов
// контейнере PostgreSQL и возвращает пул к ней. В режиме -short тест пропускается.
func NewPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test: needs Docker")
	}
	once.Do(startContainer)
	if startErr != nil {
		t.Fatalf("start postgres container: %v", startErr)
	}
	ctx := context.Background()
	dbURL, err := createDatabase(ctx, fmt.Sprintf("test_%d", seq.Add(1)))
	if err != nil {
		t.Fatalf("create database: %v", err)
	}
	if err := postgres.Migrate(ctx, dbURL); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// startContainer запускает контейнер один раз на процесс; его удалит Ryuk из testcontainers.
func startContainer() {
	ctx := context.Background()
	ctr, err := tcpostgres.Run(ctx, "postgres:18-alpine",
		tcpostgres.WithDatabase("postgres"),
		tcpostgres.WithUsername("gateway"),
		tcpostgres.WithPassword("gateway"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		startErr = err
		return
	}
	adminURL, startErr = ctr.ConnectionString(ctx, "sslmode=disable")
}

func createDatabase(ctx context.Context, name string) (string, error) {
	conn, err := pgx.Connect(ctx, adminURL)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)
	if _, err := conn.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		return "", err
	}
	u, err := url.Parse(adminURL)
	if err != nil {
		return "", err
	}
	u.Path = "/" + name
	return u.String(), nil
}
```

- [ ] **Step 5: Написать тесты репозитория заявок**

Файл: `internal/store/postgres/helpers_test.go`
```go
package postgres_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// newApp возвращает валидную заявку с новым id.
func newApp() application.Application {
	return application.Application{
		ID:             uuid.Must(uuid.NewV7()),
		FullName:       "Иванов Иван Иванович",
		BirthDate:      time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: application.EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

// count выполняет запрос вида SELECT count(*) и возвращает число.
func count(t *testing.T, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// exec выполняет SQL для подготовки состояния в тесте.
func exec(t *testing.T, pool *pgxpool.Pool, query string, args ...any) {
	t.Helper()
	if _, err := pool.Exec(t.Context(), query, args...); err != nil {
		t.Fatalf("exec: %v", err)
	}
}
```

Файл: `internal/store/postgres/applications_test.go`
```go
package postgres_test

import (
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres/pgtest"
)

var bankCodes = []string{"westbank", "northbank"} // нарочно не по алфавиту

func TestCreateStoresApplicationWithQueuedRequests(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewApplications(pool)
	app := newApp()

	id, stored, err := repo.Create(t.Context(), "key-1", []byte("hash-1"), app, bankCodes)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id != app.ID || string(stored) != "hash-1" {
		t.Fatalf("got %s %q, want %s %q", id, stored, app.ID, "hash-1")
	}
	v, err := repo.Get(t.Context(), id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if v.ID != app.ID || v.Amount != app.Amount || v.TermMonths != app.TermMonths || v.CreatedAt.IsZero() {
		t.Fatalf("unexpected view %+v", v)
	}
	if len(v.Banks) != 2 || v.Banks[0].BankCode != "northbank" || v.Banks[1].BankCode != "westbank" {
		t.Fatalf("banks = %+v, want northbank and westbank in this order", v.Banks)
	}
	for _, b := range v.Banks {
		if b.Status != application.RequestQueued || b.Attempts != 0 || b.Decision != nil {
			t.Errorf("bank %s: %+v, want a fresh queued request", b.BankCode, b)
		}
	}
	if v.Status() != application.StatusProcessing {
		t.Fatalf("status = %s, want processing", v.Status())
	}
}

func TestCreateWithSameKeyReturnsExisting(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewApplications(pool)
	first := newApp()
	if _, _, err := repo.Create(t.Context(), "key-1", []byte("hash-1"), first, bankCodes); err != nil {
		t.Fatal(err)
	}

	id, stored, err := repo.Create(t.Context(), "key-1", []byte("hash-2"), newApp(), bankCodes)
	if err != nil {
		t.Fatal(err)
	}
	if id != first.ID || string(stored) != "hash-1" {
		t.Fatalf("got %s %q, want the first application %s %q", id, stored, first.ID, "hash-1")
	}
	if n := count(t, pool, `SELECT count(*) FROM applications`); n != 1 {
		t.Fatalf("applications = %d, want 1", n)
	}
	if n := count(t, pool, `SELECT count(*) FROM bank_requests`); n != 2 {
		t.Fatalf("bank requests = %d, want 2", n)
	}
}

func TestConcurrentCreateWithSameKeyCreatesOneApplication(t *testing.T) {
	pool := pgtest.NewPool(t)
	repo := postgres.NewApplications(pool)
	const n = 10
	ids := make([]uuid.UUID, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := range n {
		wg.Go(func() {
			ids[i], _, errs[i] = repo.Create(t.Context(), "same-key", []byte("h"), newApp(), bankCodes)
		})
	}
	wg.Wait()
	for i := range n {
		if errs[i] != nil {
			t.Fatalf("Create %d: %v", i, errs[i])
		}
		if ids[i] != ids[0] {
			t.Fatalf("Create %d returned %s, want %s", i, ids[i], ids[0])
		}
	}
	if got := count(t, pool, `SELECT count(*) FROM applications`); got != 1 {
		t.Fatalf("applications = %d, want 1", got)
	}
	if got := count(t, pool, `SELECT count(*) FROM bank_requests`); got != 2 {
		t.Fatalf("bank requests = %d, want 2", got)
	}
}

func TestGetUnknownReturnsNotFound(t *testing.T) {
	repo := postgres.NewApplications(pgtest.NewPool(t))
	if _, err := repo.Get(t.Context(), uuid.Must(uuid.NewV7())); !errors.Is(err, application.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 6: Запустить — тесты должны упасть**

Run: `go test ./internal/store/...`
Expected: FAIL — `undefined: postgres.NewApplications`.

- [ ] **Step 7: Реализовать репозиторий заявок**

Файл: `internal/store/postgres/applications.go`
```go
package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Applications — реализация application.Repository.
type Applications struct {
	pool *pgxpool.Pool
}

// NewApplications создаёт репозиторий заявок.
func NewApplications(pool *pgxpool.Pool) *Applications {
	return &Applications{pool: pool}
}

// Create в одной транзакции сохраняет заявку и по запросу queued на каждый банк.
// Повтор ключа решает уникальный индекс: ON CONFLICT DO NOTHING, затем чтение существующей записи.
func (r *Applications) Create(ctx context.Context, key string, hash []byte, app application.Application, bankCodes []string) (uuid.UUID, []byte, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // после Commit — no-op

	tag, err := tx.Exec(ctx, `
		INSERT INTO applications (id, idempotency_key, request_hash, full_name, birth_date, phone,
		                          monthly_income, employment_type, amount, term_months)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		ON CONFLICT (idempotency_key) DO NOTHING`,
		app.ID, key, hash, app.FullName, app.BirthDate, app.Phone,
		app.MonthlyIncome, string(app.EmploymentType), app.Amount, app.TermMonths)
	if err != nil {
		return uuid.Nil, nil, fmt.Errorf("insert application: %w", err)
	}
	if tag.RowsAffected() == 0 {
		var id uuid.UUID
		var stored []byte
		err := tx.QueryRow(ctx, `SELECT id, request_hash FROM applications WHERE idempotency_key = $1`, key).Scan(&id, &stored)
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("load existing application: %w", err)
		}
		return id, stored, nil
	}
	for _, code := range bankCodes {
		reqID, err := uuid.NewV7()
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("generate id: %w", err)
		}
		_, err = tx.Exec(ctx, `
			INSERT INTO bank_requests (id, application_id, bank_code, status)
			VALUES ($1, $2, $3, 'queued')`, reqID, app.ID, code)
		if err != nil {
			return uuid.Nil, nil, fmt.Errorf("insert bank request: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return uuid.Nil, nil, fmt.Errorf("commit: %w", err)
	}
	return app.ID, hash, nil
}

// Get возвращает заявку с результатами по банкам или application.ErrNotFound.
func (r *Applications) Get(ctx context.Context, id uuid.UUID) (application.View, error) {
	v := application.View{ID: id}
	err := r.pool.QueryRow(ctx, `SELECT amount, term_months, created_at FROM applications WHERE id = $1`, id).
		Scan(&v.Amount, &v.TermMonths, &v.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return application.View{}, application.ErrNotFound
	}
	if err != nil {
		return application.View{}, fmt.Errorf("load application: %w", err)
	}

	rows, err := r.pool.Query(ctx, `
		SELECT bank_code, status, attempts, approved_amount, rate_bps, approved_term,
		       monthly_payment, reject_reason, last_error
		FROM bank_requests
		WHERE application_id = $1
		ORDER BY bank_code`, id)
	if err != nil {
		return application.View{}, fmt.Errorf("load bank requests: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var (
			b                       application.BankResult
			status                  string
			amount, payment         *int64
			rate, term              *int
			rejectReason, lastError *string
		)
		if err := rows.Scan(&b.BankCode, &status, &b.Attempts, &amount, &rate, &term, &payment, &rejectReason, &lastError); err != nil {
			return application.View{}, fmt.Errorf("scan bank request: %w", err)
		}
		b.Status = application.RequestStatus(status)
		switch b.Status {
		case application.RequestApproved:
			if amount == nil || rate == nil || term == nil || payment == nil {
				return application.View{}, fmt.Errorf("approved bank request %s has no offer", b.BankCode)
			}
			b.Decision = &application.Decision{Approved: true, Amount: *amount, RateBPS: *rate, TermMonths: *term, MonthlyPayment: *payment}
		case application.RequestRejected:
			b.Decision = &application.Decision{RejectReason: deref(rejectReason)}
		case application.RequestFailed:
			b.LastError = deref(lastError)
		}
		v.Banks = append(v.Banks, b)
	}
	if err := rows.Err(); err != nil {
		return application.View{}, fmt.Errorf("read bank requests: %w", err)
	}
	return v, nil
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
```

- [ ] **Step 8: Запустить — тесты проходят**

Run: `go mod tidy && go test -race ./internal/store/... && go test -short ./internal/store/...`
Expected: PASS (первый запуск скачивает образ `postgres:18-alpine`); с `-short` тесты помечены `SKIP`.

- [ ] **Step 9: Commit**

```bash
git add go.mod go.sum internal/store
git commit -m "feat(store): add migrations and application repository"
```

---

### Task 6: Очередь запросов в банки

**Ветка:** `feature/store-queue` · **Этап 1, локально** · **Спека:** разделы 4.4, 5.3, 8

**Files:**
- Create: `internal/dispatch/types.go`, `internal/store/postgres/queue.go`
- Test: `internal/dispatch/types_test.go`, `internal/store/postgres/queue_test.go`

**Interfaces:**
- Consumes: `application.*` (Task 2); `pgtest.NewPool`, помощники `newApp`, `count`, `exec` (Task 5)
- Produces:
  - пакет `dispatch`: `type Task struct{ RequestID uuid.UUID; BankCode string; Attempts int; Application application.Application }`; `type OutcomeKind int` с `OutcomeDecision`, `OutcomeAccepted`, `OutcomeRetry`, `OutcomeFailed` и методом `String()`; `type Outcome struct{ Kind OutcomeKind; Decision application.Decision; ExternalID string; CallbackTimeout, RetryAfter time.Duration; Error string }`; `type SweepStats struct{ CallbackTimeouts, Requeued, LeaseExpired int64 }`; `type Queue interface{ Claim(ctx, limit int, lease time.Duration) ([]Task, error); Complete(ctx, Task, Outcome) (bool, error); Sweep(ctx, maxAttempts int) (SweepStats, error) }`
  - пакет `postgres`: `type Queue`; `func NewQueue(pool *pgxpool.Pool) *Queue` — реализует `dispatch.Queue`; `(*Queue) ApplyCallback(ctx, bankCode, externalID string, d application.Decision) (application.CallbackResult, error)`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/store-queue
```

- [ ] **Step 2: Написать тест названий исходов**

Файл: `internal/dispatch/types_test.go`
```go
package dispatch

import "testing"

func TestOutcomeKindString(t *testing.T) {
	want := map[OutcomeKind]string{
		OutcomeDecision: "decision",
		OutcomeAccepted: "accepted",
		OutcomeRetry:    "retry",
		OutcomeFailed:   "failed",
		0:               "unknown",
	}
	for kind, name := range want {
		if got := kind.String(); got != name {
			t.Errorf("%d.String() = %q, want %q", int(kind), got, name)
		}
	}
}
```

- [ ] **Step 3: Запустить — тест должен упасть**

Run: `go test ./internal/dispatch/`
Expected: FAIL — `undefined: OutcomeKind`.

- [ ] **Step 4: Описать типы очереди**

Файл: `internal/dispatch/types.go`
```go
// Package dispatch — воркер шлюза: забирает запросы в банки из очереди, вызывает адаптеры,
// ретраит с backoff и возвращает в очередь зависшие задачи.
package dispatch

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Task — захваченный воркером запрос в банк вместе с заявкой.
type Task struct {
	RequestID   uuid.UUID
	BankCode    string
	Attempts    int // номер текущей попытки, уже увеличенный при захвате
	Application application.Application
}

// OutcomeKind — вид результата обработки задачи.
type OutcomeKind int

const (
	OutcomeDecision OutcomeKind = iota + 1 // банк вернул решение
	OutcomeAccepted                        // банк принял заявку, решение придёт вебхуком
	OutcomeRetry                           // временная ошибка, повторить позже
	OutcomeFailed                          // финальная ошибка
)

func (k OutcomeKind) String() string {
	switch k {
	case OutcomeDecision:
		return "decision"
	case OutcomeAccepted:
		return "accepted"
	case OutcomeRetry:
		return "retry"
	case OutcomeFailed:
		return "failed"
	default:
		return "unknown"
	}
}

// Outcome — результат обработки задачи, который воркер записывает в очередь.
type Outcome struct {
	Kind            OutcomeKind
	Decision        application.Decision // для OutcomeDecision
	ExternalID      string               // для OutcomeAccepted
	CallbackTimeout time.Duration        // для OutcomeAccepted: сколько ждать вебхук
	RetryAfter      time.Duration        // для OutcomeRetry
	Error           string               // для OutcomeRetry и OutcomeFailed
}

// SweepStats — сколько задач изменил чистильщик за проход.
type SweepStats struct {
	CallbackTimeouts int64 // waiting_callback → failed
	Requeued         int64 // sending с истёкшей арендой → queued
	LeaseExpired     int64 // sending с истёкшей арендой и исчерпанными попытками → failed
}

// Queue — очередь запросов в банки; реализация — store/postgres.Queue.
type Queue interface {
	// Claim атомарно забирает до limit готовых задач и выдаёт на них аренду lease.
	Claim(ctx context.Context, limit int, lease time.Duration) ([]Task, error)
	// Complete записывает результат; false — аренду перехватили, результат не записан.
	Complete(ctx context.Context, t Task, o Outcome) (bool, error)
	// Sweep закрывает просроченные ожидания вебхуков и возвращает задачи с истёкшей арендой.
	Sweep(ctx context.Context, maxAttempts int) (SweepStats, error)
}
```

- [ ] **Step 5: Запустить — тест проходит**

Run: `go test ./internal/dispatch/`
Expected: PASS

- [ ] **Step 6: Написать тесты очереди**

Файл: `internal/store/postgres/queue_test.go`
```go
package postgres_test

import (
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres/pgtest"
)

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}

// seed создаёт заявку с запросами в указанные банки.
func seed(t *testing.T, pool *pgxpool.Pool, codes ...string) application.Application {
	t.Helper()
	app := newApp()
	if _, _, err := postgres.NewApplications(pool).Create(t.Context(), app.ID.String(), []byte("h"), app, codes); err != nil {
		t.Fatalf("seed: %v", err)
	}
	return app
}

func claim(t *testing.T, q *postgres.Queue, limit int) []dispatch.Task {
	t.Helper()
	tasks, err := q.Claim(t.Context(), limit, time.Minute)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	return tasks
}

func complete(t *testing.T, q *postgres.Queue, task dispatch.Task, o dispatch.Outcome) bool {
	t.Helper()
	ok, err := q.Complete(t.Context(), task, o)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	return ok
}

// result возвращает состояние запроса заявки appID в банк code.
func result(t *testing.T, pool *pgxpool.Pool, appID uuid.UUID, code string) application.BankResult {
	t.Helper()
	v, err := postgres.NewApplications(pool).Get(t.Context(), appID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	for _, b := range v.Banks {
		if b.BankCode == code {
			return b
		}
	}
	t.Fatalf("no bank request for %s", code)
	return application.BankResult{}
}

func TestClaimReturnsTaskWithApplication(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")

	tasks := claim(t, q, 10)
	if len(tasks) != 1 {
		t.Fatalf("claimed %d tasks, want 1", len(tasks))
	}
	task := tasks[0]
	if task.BankCode != "northbank" || task.Attempts != 1 || task.RequestID == uuid.Nil {
		t.Fatalf("task = %+v", task)
	}
	a := task.Application
	if a.ID != app.ID || a.FullName != app.FullName || !a.BirthDate.Equal(app.BirthDate) || a.Phone != app.Phone ||
		a.MonthlyIncome != app.MonthlyIncome || a.EmploymentType != app.EmploymentType ||
		a.Amount != app.Amount || a.TermMonths != app.TermMonths {
		t.Fatalf("application = %+v, want %+v", a, app)
	}
	if s := result(t, pool, app.ID, "northbank").Status; s != application.RequestSending {
		t.Fatalf("status = %s, want sending", s)
	}
	if again := claim(t, q, 10); len(again) != 0 {
		t.Fatalf("claimed %d tasks again, want 0", len(again))
	}
}

func TestClaimRespectsLimitAndSchedule(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	seed(t, pool, "eastbank", "northbank", "westbank")

	if got := claim(t, q, 2); len(got) != 2 {
		t.Fatalf("claimed %d tasks, want 2", len(got))
	}
	exec(t, pool, `UPDATE bank_requests SET next_attempt_at = now() + interval '1 hour' WHERE status = 'queued'`)
	if got := claim(t, q, 10); len(got) != 0 {
		t.Fatalf("claimed %d tasks scheduled for later, want 0", len(got))
	}
}

func TestConcurrentClaimsDoNotOverlap(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	for range 10 {
		seed(t, pool, "eastbank", "northbank")
	}
	var (
		mu   sync.Mutex
		seen = map[uuid.UUID]int{}
		wg   sync.WaitGroup
	)
	for range 4 {
		wg.Go(func() {
			tasks, err := q.Claim(t.Context(), 5, time.Minute)
			if err != nil {
				t.Errorf("Claim: %v", err)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, task := range tasks {
				seen[task.RequestID]++
			}
		})
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("claimed %d distinct tasks, want 20", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("task %s claimed %d times", id, n)
		}
	}
}

func TestCompleteWritesDecision(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank", "westbank")
	byCode := map[string]dispatch.Task{}
	for _, task := range claim(t, q, 2) {
		byCode[task.BankCode] = task
	}

	if !complete(t, q, byCode["northbank"], dispatch.Outcome{Kind: dispatch.OutcomeDecision, Decision: approved}) {
		t.Fatal("approve was not written")
	}
	reject := dispatch.Outcome{Kind: dispatch.OutcomeDecision, Decision: application.Decision{RejectReason: "debt_burden"}}
	if !complete(t, q, byCode["westbank"], reject) {
		t.Fatal("reject was not written")
	}

	north := result(t, pool, app.ID, "northbank")
	if north.Status != application.RequestApproved || north.Decision == nil || *north.Decision != approved {
		t.Fatalf("northbank = %+v, want approved with offer", north)
	}
	west := result(t, pool, app.ID, "westbank")
	if west.Status != application.RequestRejected || west.Decision == nil || west.Decision.RejectReason != "debt_burden" {
		t.Fatalf("westbank = %+v, want rejected with debt_burden", west)
	}
}

func TestCompleteAcceptedThenCallback(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	task := claim(t, q, 1)[0]

	accepted := dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-1", CallbackTimeout: time.Minute}
	if !complete(t, q, task, accepted) {
		t.Fatal("accepted outcome was not written")
	}
	if s := result(t, pool, app.ID, "westbank").Status; s != application.RequestWaitingCallback {
		t.Fatalf("status = %s, want waiting_callback", s)
	}

	apply := func(externalID string, d application.Decision) application.CallbackResult {
		t.Helper()
		res, err := q.ApplyCallback(t.Context(), "westbank", externalID, d)
		if err != nil {
			t.Fatalf("ApplyCallback: %v", err)
		}
		return res
	}
	if res := apply("w-1", approved); res != application.CallbackApplied {
		t.Fatalf("first callback = %d, want applied", res)
	}
	if b := result(t, pool, app.ID, "westbank"); b.Status != application.RequestApproved || *b.Decision != approved {
		t.Fatalf("westbank = %+v, want approved", b)
	}
	if res := apply("w-1", approved); res != application.CallbackDuplicate {
		t.Fatalf("repeated callback = %d, want duplicate", res)
	}
	if res := apply("w-unknown", approved); res != application.CallbackUnknown {
		t.Fatalf("unknown callback = %d, want unknown", res)
	}
}

func TestCallbackWithRejection(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	complete(t, q, claim(t, q, 1)[0], dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-2", CallbackTimeout: time.Minute})

	res, err := q.ApplyCallback(t.Context(), "westbank", "w-2", application.Decision{RejectReason: "insufficient_income"})
	if err != nil || res != application.CallbackApplied {
		t.Fatalf("ApplyCallback = %d, %v", res, err)
	}
	b := result(t, pool, app.ID, "westbank")
	if b.Status != application.RequestRejected || b.Decision.RejectReason != "insufficient_income" {
		t.Fatalf("westbank = %+v, want rejected", b)
	}
}

func TestCompleteRetryReschedules(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "eastbank")
	task := claim(t, q, 1)[0]

	retry := dispatch.Outcome{Kind: dispatch.OutcomeRetry, RetryAfter: time.Hour, Error: "temporary bank error: unexpected status 503"}
	if !complete(t, q, task, retry) {
		t.Fatal("retry was not written")
	}
	if s := result(t, pool, app.ID, "eastbank").Status; s != application.RequestQueued {
		t.Fatalf("status = %s, want queued", s)
	}
	saved := count(t, pool, `SELECT count(*) FROM bank_requests
		WHERE last_error = 'temporary bank error: unexpected status 503' AND locked_until IS NULL`)
	if saved != 1 {
		t.Fatal("last_error is not saved or lease is not released")
	}
	if got := claim(t, q, 1); len(got) != 0 {
		t.Fatal("retry must wait for next_attempt_at")
	}
	exec(t, pool, `UPDATE bank_requests SET next_attempt_at = now()`)
	again := claim(t, q, 1)
	if len(again) != 1 || again[0].Attempts != 2 {
		t.Fatalf("second claim = %+v, want one task with attempt 2", again)
	}
}

func TestCompleteFailed(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")
	failed := dispatch.Outcome{Kind: dispatch.OutcomeFailed, Error: "permanent bank error: unexpected status 400"}
	if !complete(t, q, claim(t, q, 1)[0], failed) {
		t.Fatal("failure was not written")
	}
	b := result(t, pool, app.ID, "northbank")
	if b.Status != application.RequestFailed || b.LastError != "permanent bank error: unexpected status 400" {
		t.Fatalf("northbank = %+v, want failed with error", b)
	}
}

func TestCompleteAfterLostLeaseIsDropped(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")
	stale := claim(t, q, 1)[0]

	exec(t, pool, `UPDATE bank_requests SET locked_until = now() - interval '1 second'`)
	stats, err := q.Sweep(t.Context(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if stats.Requeued != 1 {
		t.Fatalf("requeued = %d, want 1", stats.Requeued)
	}
	fresh := claim(t, q, 1)[0]
	if fresh.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", fresh.Attempts)
	}

	decision := dispatch.Outcome{Kind: dispatch.OutcomeDecision, Decision: approved}
	if complete(t, q, stale, decision) {
		t.Fatal("stale owner must not write the result")
	}
	if s := result(t, pool, app.ID, "northbank").Status; s != application.RequestSending {
		t.Fatalf("status = %s, want sending", s)
	}
	if !complete(t, q, fresh, decision) {
		t.Fatal("current owner must write the result")
	}
}

func TestSweepCallbackTimeout(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "westbank")
	complete(t, q, claim(t, q, 1)[0], dispatch.Outcome{Kind: dispatch.OutcomeAccepted, ExternalID: "w-1", CallbackTimeout: time.Minute})
	exec(t, pool, `UPDATE bank_requests SET callback_deadline = now() - interval '1 second'`)

	stats, err := q.Sweep(t.Context(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if stats.CallbackTimeouts != 1 {
		t.Fatalf("callback timeouts = %d, want 1", stats.CallbackTimeouts)
	}
	b := result(t, pool, app.ID, "westbank")
	if b.Status != application.RequestFailed || b.LastError != "callback timeout" {
		t.Fatalf("westbank = %+v, want failed with callback timeout", b)
	}
}

func TestSweepFailsExpiredLeaseWithoutAttemptsLeft(t *testing.T) {
	pool := pgtest.NewPool(t)
	q := postgres.NewQueue(pool)
	app := seed(t, pool, "northbank")
	claim(t, q, 1)
	exec(t, pool, `UPDATE bank_requests SET locked_until = now() - interval '1 second'`)

	stats, err := q.Sweep(t.Context(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if stats.LeaseExpired != 1 || stats.Requeued != 0 {
		t.Fatalf("stats = %+v, want one expired lease", stats)
	}
	b := result(t, pool, app.ID, "northbank")
	if b.Status != application.RequestFailed || b.LastError != "lease expired" {
		t.Fatalf("northbank = %+v, want failed with lease expired", b)
	}
}
```

- [ ] **Step 7: Запустить — тесты должны упасть**

Run: `go test ./internal/store/...`
Expected: FAIL — `undefined: postgres.NewQueue`.

- [ ] **Step 8: Реализовать очередь**

Файл: `internal/store/postgres/queue.go`
```go
package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
)

// Queue — очередь запросов в банки поверх таблицы bank_requests; реализует dispatch.Queue.
type Queue struct {
	pool *pgxpool.Pool
}

// NewQueue создаёт очередь.
func NewQueue(pool *pgxpool.Pool) *Queue {
	return &Queue{pool: pool}
}

const claimSQL = `
WITH picked AS (
    SELECT id FROM bank_requests
    WHERE status = 'queued' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $1
    FOR UPDATE SKIP LOCKED
)
UPDATE bank_requests AS br
SET status = 'sending',
    attempts = br.attempts + 1,
    locked_until = now() + $2::bigint * interval '1 millisecond',
    updated_at = now()
FROM picked, applications AS a
WHERE br.id = picked.id AND a.id = br.application_id
RETURNING br.id, br.bank_code, br.attempts,
          a.id, a.full_name, a.birth_date, a.phone, a.monthly_income, a.employment_type, a.amount, a.term_months`

// Claim атомарно переводит до limit готовых задач в sending с арендой lease.
func (q *Queue) Claim(ctx context.Context, limit int, lease time.Duration) ([]dispatch.Task, error) {
	rows, err := q.pool.Query(ctx, claimSQL, limit, lease.Milliseconds())
	if err != nil {
		return nil, fmt.Errorf("claim: %w", err)
	}
	defer rows.Close()
	var tasks []dispatch.Task
	for rows.Next() {
		var (
			t          dispatch.Task
			employment string
		)
		a := &t.Application
		if err := rows.Scan(&t.RequestID, &t.BankCode, &t.Attempts,
			&a.ID, &a.FullName, &a.BirthDate, &a.Phone, &a.MonthlyIncome, &employment, &a.Amount, &a.TermMonths); err != nil {
			return nil, fmt.Errorf("scan claimed task: %w", err)
		}
		a.EmploymentType = application.EmploymentType(employment)
		tasks = append(tasks, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read claimed tasks: %w", err)
	}
	return tasks, nil
}

// ownerGuard — защита владения: результат пишет только держатель текущей аренды.
const ownerGuard = ` WHERE id = $1 AND status = 'sending' AND attempts = $2`

// Complete записывает результат обработки задачи. false — аренду перехватили.
func (q *Queue) Complete(ctx context.Context, t dispatch.Task, o dispatch.Outcome) (bool, error) {
	var query string
	args := []any{t.RequestID, t.Attempts}
	switch o.Kind {
	case dispatch.OutcomeDecision:
		if o.Decision.Approved {
			query = `UPDATE bank_requests SET status = 'approved', approved_amount = $3, rate_bps = $4,
				approved_term = $5, monthly_payment = $6, locked_until = NULL, updated_at = now()` + ownerGuard
			args = append(args, o.Decision.Amount, o.Decision.RateBPS, o.Decision.TermMonths, o.Decision.MonthlyPayment)
		} else {
			query = `UPDATE bank_requests SET status = 'rejected', reject_reason = $3,
				locked_until = NULL, updated_at = now()` + ownerGuard
			args = append(args, o.Decision.RejectReason)
		}
	case dispatch.OutcomeAccepted:
		query = `UPDATE bank_requests SET status = 'waiting_callback', external_id = $3,
			callback_deadline = now() + $4::bigint * interval '1 millisecond',
			locked_until = NULL, updated_at = now()` + ownerGuard
		args = append(args, o.ExternalID, o.CallbackTimeout.Milliseconds())
	case dispatch.OutcomeRetry:
		query = `UPDATE bank_requests SET status = 'queued',
			next_attempt_at = now() + $3::bigint * interval '1 millisecond',
			last_error = $4, locked_until = NULL, updated_at = now()` + ownerGuard
		args = append(args, o.RetryAfter.Milliseconds(), o.Error)
	case dispatch.OutcomeFailed:
		query = `UPDATE bank_requests SET status = 'failed', last_error = $3,
			locked_until = NULL, updated_at = now()` + ownerGuard
		args = append(args, o.Error)
	default:
		return false, fmt.Errorf("unknown outcome kind %d", o.Kind)
	}
	tag, err := q.pool.Exec(ctx, query, args...)
	if err != nil {
		return false, fmt.Errorf("complete %s: %w", o.Kind, err)
	}
	return tag.RowsAffected() == 1, nil
}

// Sweep закрывает просроченные ожидания вебхуков и возвращает в очередь задачи
// с истёкшей арендой; если попытки исчерпаны — переводит их в failed.
func (q *Queue) Sweep(ctx context.Context, maxAttempts int) (dispatch.SweepStats, error) {
	var s dispatch.SweepStats
	tag, err := q.pool.Exec(ctx, `
		UPDATE bank_requests SET status = 'failed', last_error = 'callback timeout', updated_at = now()
		WHERE status = 'waiting_callback' AND callback_deadline < now()`)
	if err != nil {
		return s, fmt.Errorf("sweep callback timeouts: %w", err)
	}
	s.CallbackTimeouts = tag.RowsAffected()

	tag, err = q.pool.Exec(ctx, `
		UPDATE bank_requests SET status = 'queued', next_attempt_at = now(), locked_until = NULL, updated_at = now()
		WHERE status = 'sending' AND locked_until < now() AND attempts < $1`, maxAttempts)
	if err != nil {
		return s, fmt.Errorf("sweep expired leases: %w", err)
	}
	s.Requeued = tag.RowsAffected()

	tag, err = q.pool.Exec(ctx, `
		UPDATE bank_requests SET status = 'failed', last_error = 'lease expired', locked_until = NULL, updated_at = now()
		WHERE status = 'sending' AND locked_until < now() AND attempts >= $1`, maxAttempts)
	if err != nil {
		return s, fmt.Errorf("sweep exhausted leases: %w", err)
	}
	s.LeaseExpired = tag.RowsAffected()
	return s, nil
}

// ApplyCallback записывает решение из вебхука, если запрос ещё ждёт его.
func (q *Queue) ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackResult, error) {
	const guard = ` WHERE bank_code = $1 AND external_id = $2 AND status = 'waiting_callback'`
	query := `UPDATE bank_requests SET status = 'rejected', reject_reason = $3, updated_at = now()` + guard
	args := []any{bankCode, externalID, d.RejectReason}
	if d.Approved {
		query = `UPDATE bank_requests SET status = 'approved', approved_amount = $3, rate_bps = $4,
			approved_term = $5, monthly_payment = $6, updated_at = now()` + guard
		args = []any{bankCode, externalID, d.Amount, d.RateBPS, d.TermMonths, d.MonthlyPayment}
	}
	tag, err := q.pool.Exec(ctx, query, args...)
	if err != nil {
		return 0, fmt.Errorf("apply callback: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return application.CallbackApplied, nil
	}
	var exists bool
	err = q.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM bank_requests WHERE bank_code = $1 AND external_id = $2)`,
		bankCode, externalID).Scan(&exists)
	if err != nil {
		return 0, fmt.Errorf("lookup callback request: %w", err)
	}
	if exists {
		return application.CallbackDuplicate, nil
	}
	return application.CallbackUnknown, nil
}
```

- [ ] **Step 9: Запустить — тесты проходят**

Run: `go test -race ./internal/store/... ./internal/dispatch/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 10: Commit**

```bash
git add internal/dispatch internal/store/postgres/queue.go internal/store/postgres/queue_test.go
git commit -m "feat(store): add bank request queue with lease and owner guard"
```

---

### Task 7: Воркер

**Ветка:** `feature/worker` · **Этап 1, локально** · **Спека:** раздел 8

**Files:**
- Create: `internal/dispatch/backoff.go`, `internal/dispatch/worker.go`
- Test: `internal/dispatch/backoff_test.go`, `internal/dispatch/worker_test.go`

**Interfaces:**
- Consumes: `dispatch.Task`, `dispatch.Outcome`, `dispatch.Queue` (Task 6); `bank.Registry`, `bank.SubmitRequest`, `bank.IsPermanent`, `bank.StatusError`, `bank.ErrTemporary` (Task 3)
- Produces (пакет `dispatch`):
  - `func Backoff(attempt int, base, maxDelay time.Duration, rnd func(int64) int64) time.Duration`
  - `const DefaultBankTimeout = 5 * time.Second`
  - `type Config struct{ PollInterval time.Duration; BatchSize, Concurrency int; Lease time.Duration; MaxAttempts int; BaseDelay, MaxDelay, CallbackTimeout, SweepInterval, ShutdownTimeout time.Duration; BankTimeouts map[string]time.Duration }`
  - `func New(queue Queue, banks *bank.Registry, cfg Config, log *slog.Logger) *Worker`; `(*Worker) Run(ctx) error`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/worker
```

- [ ] **Step 2: Написать тесты backoff**

Файл: `internal/dispatch/backoff_test.go`
```go
package dispatch

import (
	"math/rand/v2"
	"testing"
	"time"
)

func TestBackoffUpperBound(t *testing.T) {
	upper := func(n int64) int64 { return n - 1 } // всегда максимум диапазона
	tests := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{7, time.Minute}, // 64 с упираются в потолок
		{100, time.Minute},
	}
	for _, tt := range tests {
		if got := Backoff(tt.attempt, time.Second, time.Minute, upper); got != tt.want {
			t.Errorf("Backoff(%d) = %s, want %s", tt.attempt, got, tt.want)
		}
	}
}

func TestBackoffLowerBound(t *testing.T) {
	zero := func(int64) int64 { return 0 }
	if got := Backoff(3, time.Second, time.Minute, zero); got != 0 {
		t.Fatalf("got %s, want 0", got)
	}
}

func TestBackoffStaysInRange(t *testing.T) {
	for range 1000 {
		if d := Backoff(3, time.Second, time.Minute, rand.Int64N); d < 0 || d > 4*time.Second {
			t.Fatalf("Backoff(3) = %s, want within [0, 4s]", d)
		}
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/dispatch/`
Expected: FAIL — `undefined: Backoff`.

- [ ] **Step 4: Реализовать backoff**

Файл: `internal/dispatch/backoff.go`
```go
package dispatch

import "time"

// Backoff — пауза перед следующей попыткой после неудачной попытки attempt (с 1), full jitter:
// случайная в [0, min(maxDelay, base·2^(attempt−1))]. rnd(n) возвращает число из [0, n).
func Backoff(attempt int, base, maxDelay time.Duration, rnd func(int64) int64) time.Duration {
	d := base
	for i := 1; i < attempt && d < maxDelay; i++ {
		d *= 2
	}
	d = min(d, maxDelay)
	return time.Duration(rnd(int64(d) + 1))
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/dispatch/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/dispatch/backoff.go internal/dispatch/backoff_test.go
git commit -m "feat(dispatch): add full-jitter backoff"
```

- [ ] **Step 7: Написать тесты воркера**

Файл: `internal/dispatch/worker_test.go`
```go
package dispatch

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

type completion struct {
	task    Task
	outcome Outcome
}

// fakeQueue — очередь в памяти.
type fakeQueue struct {
	mu        sync.Mutex
	pending   []Task
	claimErrs int // сколько первых вызовов Claim вернут ошибку
	limits    []int
	completed []completion
	sweeps    int
}

func (q *fakeQueue) Claim(_ context.Context, limit int, _ time.Duration) ([]Task, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.limits = append(q.limits, limit)
	if q.claimErrs > 0 {
		q.claimErrs--
		return nil, errors.New("connection refused")
	}
	n := min(limit, len(q.pending))
	tasks := slices.Clone(q.pending[:n])
	q.pending = q.pending[n:]
	return tasks, nil
}

func (q *fakeQueue) Complete(_ context.Context, t Task, o Outcome) (bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.completed = append(q.completed, completion{task: t, outcome: o})
	return true, nil
}

func (q *fakeQueue) Sweep(context.Context, int) (SweepStats, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.sweeps++
	return SweepStats{}, nil
}

func (q *fakeQueue) completions() []completion {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.completed)
}

func (q *fakeQueue) claimLimits() []int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return slices.Clone(q.limits)
}

func (q *fakeQueue) sweepCount() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.sweeps
}

type submitFunc func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error)

type fakeBank struct {
	code   string
	submit submitFunc
}

func (b fakeBank) Code() string { return b.code }

func (b fakeBank) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	return b.submit(ctx, req)
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}

func decide(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
	d := approved
	return bank.SubmitResult{Decision: &d}, nil
}

func failWith(err error) submitFunc {
	return func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
		return bank.SubmitResult{}, err
	}
}

func testConfig() Config {
	return Config{
		PollInterval:    5 * time.Millisecond,
		BatchSize:       10,
		Concurrency:     4,
		Lease:           time.Minute,
		MaxAttempts:     3,
		BaseDelay:       10 * time.Millisecond,
		MaxDelay:        100 * time.Millisecond,
		CallbackTimeout: time.Minute,
		SweepInterval:   5 * time.Millisecond,
		ShutdownTimeout: time.Second,
		BankTimeouts:    map[string]time.Duration{},
	}
}

func newTestWorker(t *testing.T, q Queue, cfg Config, banks ...bank.Bank) *Worker {
	t.Helper()
	reg, err := bank.NewRegistry(banks...)
	if err != nil {
		t.Fatal(err)
	}
	return New(q, reg, cfg, slog.New(slog.NewTextHandler(t.Output(), nil)))
}

func newTask(code string, attempts int) Task {
	return Task{RequestID: uuid.New(), BankCode: code, Attempts: attempts, Application: application.Application{ID: uuid.New()}}
}

// runWorker запускает Run в фоне; возвращённая функция останавливает воркер и ждёт выхода.
func runWorker(t *testing.T, w *Worker) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()
	stop := sync.OnceFunc(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run: %v", err)
		}
	})
	t.Cleanup(stop)
	return stop
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestCallOutcomes(t *testing.T) {
	tests := []struct {
		name     string
		attempts int
		submit   submitFunc
		want     Outcome
	}{
		{
			name: "decision", attempts: 1, submit: decide,
			want: Outcome{Kind: OutcomeDecision, Decision: approved},
		},
		{
			name: "accepted asynchronously", attempts: 1,
			submit: func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
				return bank.SubmitResult{ExternalID: "w-1"}, nil
			},
			want: Outcome{Kind: OutcomeAccepted, ExternalID: "w-1", CallbackTimeout: time.Minute},
		},
		{
			name: "permanent error fails", attempts: 1, submit: failWith(bank.StatusError(400)),
			want: Outcome{Kind: OutcomeFailed, Error: "permanent bank error: unexpected status 400"},
		},
		{
			name: "temporary error on last attempt fails", attempts: 3, submit: failWith(bank.StatusError(503)),
			want: Outcome{Kind: OutcomeFailed, Error: "temporary bank error: unexpected status 503"},
		},
		{
			name: "empty result fails", attempts: 1,
			submit: func(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
				return bank.SubmitResult{}, nil
			},
			want: Outcome{Kind: OutcomeFailed, Error: "bank returned neither decision nor external id"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: tt.submit})
			if got := w.call(t.Context(), newTask("northbank", tt.attempts)); got != tt.want {
				t.Fatalf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestCallRetriesTemporaryErrors(t *testing.T) {
	for _, err := range []error{bank.StatusError(503), errors.New("unclassified")} {
		w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: failWith(err)})
		got := w.call(t.Context(), newTask("northbank", 2))
		if got.Kind != OutcomeRetry || got.Error != err.Error() {
			t.Fatalf("got %+v, want retry with %q", got, err)
		}
		if got.RetryAfter < 0 || got.RetryAfter > 20*time.Millisecond { // попытка 2: не больше base·2
			t.Fatalf("retry after %s, want within [0, 20ms]", got.RetryAfter)
		}
	}
}

func TestCallPassesRequestToBank(t *testing.T) {
	var got bank.SubmitRequest
	submit := func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
		got = req
		return decide(ctx, req)
	}
	w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: submit})
	task := newTask("northbank", 1)
	w.call(t.Context(), task)
	if got.RequestID != task.RequestID || got.Application.ID != task.Application.ID {
		t.Fatalf("bank got %+v, want request %s for application %s", got, task.RequestID, task.Application.ID)
	}
}

func TestCallUnknownBankFails(t *testing.T) {
	w := newTestWorker(t, &fakeQueue{}, testConfig(), fakeBank{code: "northbank", submit: decide})
	if got := w.call(t.Context(), newTask("nobank", 1)); got != (Outcome{Kind: OutcomeFailed, Error: "unknown bank"}) {
		t.Fatalf("got %+v", got)
	}
}

func TestCallAppliesBankTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.BankTimeouts["slowbank"] = 20 * time.Millisecond
	slow := func(ctx context.Context, _ bank.SubmitRequest) (bank.SubmitResult, error) {
		<-ctx.Done()
		return bank.SubmitResult{}, fmt.Errorf("%w: %w", bank.ErrTemporary, ctx.Err())
	}
	w := newTestWorker(t, &fakeQueue{}, cfg, fakeBank{code: "slowbank", submit: slow})
	start := time.Now()
	got := w.call(t.Context(), newTask("slowbank", 1))
	if got.Kind != OutcomeRetry {
		t.Fatalf("got %+v, want retry", got)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("call took %s, bank timeout is not applied", elapsed)
	}
}

func TestRunProcessesClaimedTasks(t *testing.T) {
	q := &fakeQueue{pending: []Task{newTask("northbank", 1), newTask("northbank", 1), newTask("northbank", 1)}}
	stop := runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: decide}))
	eventually(t, "3 completions", func() bool { return len(q.completions()) == 3 })
	stop()
	for _, c := range q.completions() {
		if c.outcome.Kind != OutcomeDecision {
			t.Fatalf("outcome = %+v, want decision", c.outcome)
		}
	}
}

func TestRunKeepsWorkingAfterClaimError(t *testing.T) {
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}, claimErrs: 2}
	runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: decide}))
	eventually(t, "completion after claim errors", func() bool { return len(q.completions()) == 1 })
}

func TestRunSweeps(t *testing.T) {
	q := &fakeQueue{}
	runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: decide}))
	eventually(t, "a sweep", func() bool { return q.sweepCount() > 0 })
}

func TestRunRespectsConcurrency(t *testing.T) {
	cfg := testConfig()
	cfg.Concurrency = 2
	var inFlight, maxInFlight atomic.Int32
	release := make(chan struct{})
	submit := func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
		n := inFlight.Add(1)
		for {
			m := maxInFlight.Load()
			if n <= m || maxInFlight.CompareAndSwap(m, n) {
				break
			}
		}
		<-release
		inFlight.Add(-1)
		return decide(ctx, req)
	}
	q := &fakeQueue{}
	for range 5 {
		q.pending = append(q.pending, newTask("northbank", 1))
	}
	runWorker(t, newTestWorker(t, q, cfg, fakeBank{code: "northbank", submit: submit}))

	eventually(t, "2 tasks in flight", func() bool { return inFlight.Load() == 2 })
	time.Sleep(30 * time.Millisecond) // дать воркеру шанс взять лишнее, если ограничение сломано
	close(release)
	eventually(t, "5 completions", func() bool { return len(q.completions()) == 5 })
	if m := maxInFlight.Load(); m > 2 {
		t.Fatalf("max in flight = %d, want <= 2", m)
	}
	for _, limit := range q.claimLimits() {
		if limit > 2 {
			t.Fatalf("claimed with limit %d, want <= free slots", limit)
		}
	}
}

func TestRunWaitsForInFlightTaskOnShutdown(t *testing.T) {
	started := make(chan struct{})
	submit := func(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
		close(started)
		time.Sleep(50 * time.Millisecond) // банк отвечает, не глядя на отмену
		return decide(ctx, req)
	}
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}}
	stop := runWorker(t, newTestWorker(t, q, testConfig(), fakeBank{code: "northbank", submit: submit}))
	<-started
	stop()
	if n := len(q.completions()); n != 1 {
		t.Fatalf("completions after shutdown = %d, want 1", n)
	}
}

func TestRunCancelsInFlightTaskAfterShutdownTimeout(t *testing.T) {
	cfg := testConfig()
	cfg.ShutdownTimeout = 20 * time.Millisecond
	started := make(chan struct{})
	submit := func(ctx context.Context, _ bank.SubmitRequest) (bank.SubmitResult, error) {
		close(started)
		<-ctx.Done()
		return bank.SubmitResult{}, fmt.Errorf("%w: %w", bank.ErrTemporary, ctx.Err())
	}
	q := &fakeQueue{pending: []Task{newTask("northbank", 1)}}
	stop := runWorker(t, newTestWorker(t, q, cfg, fakeBank{code: "northbank", submit: submit}))
	<-started
	begin := time.Now()
	stop()
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("shutdown took %s, want about ShutdownTimeout", elapsed)
	}
}
```

- [ ] **Step 8: Запустить — тесты должны упасть**

Run: `go test ./internal/dispatch/`
Expected: FAIL — `undefined: Config`, `undefined: New`, `undefined: Worker`.

- [ ] **Step 9: Реализовать воркер**

Файл: `internal/dispatch/worker.go`
```go
package dispatch

import (
	"context"
	"log/slog"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// DefaultBankTimeout — таймаут вызова банка, если он не задан в Config.BankTimeouts.
const DefaultBankTimeout = 5 * time.Second

// Config — параметры воркера (раздел 9 спеки).
type Config struct {
	PollInterval    time.Duration
	BatchSize       int
	Concurrency     int
	Lease           time.Duration
	MaxAttempts     int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	CallbackTimeout time.Duration
	SweepInterval   time.Duration
	ShutdownTimeout time.Duration
	BankTimeouts    map[string]time.Duration // таймаут вызова по коду банка
}

// Worker забирает задачи из очереди, вызывает банки и записывает результаты.
type Worker struct {
	queue Queue
	banks *bank.Registry
	cfg   Config
	log   *slog.Logger
	rnd   func(int64) int64
}

// New создаёт воркер.
func New(queue Queue, banks *bank.Registry, cfg Config, log *slog.Logger) *Worker {
	return &Worker{queue: queue, banks: banks, cfg: cfg, log: log, rnd: rand.Int64N}
}

// Run обрабатывает очередь до отмены ctx. После отмены перестаёт брать задачи и ждёт текущие
// не дольше ShutdownTimeout, затем отменяет их; незавершённые задачи вернёт чистильщик.
func (w *Worker) Run(ctx context.Context) error {
	// У задач свой контекст: отмена ctx не должна обрывать вызовы банков сразу.
	taskCtx, cancelTasks := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelTasks()

	slots := make(chan struct{}, w.cfg.Concurrency)
	var wg sync.WaitGroup
	poll := time.NewTicker(w.cfg.PollInterval)
	defer poll.Stop()
	sweep := time.NewTicker(w.cfg.SweepInterval)
	defer sweep.Stop()

	w.log.Info("worker started", "concurrency", w.cfg.Concurrency)
	for {
		select {
		case <-ctx.Done():
			w.drain(&wg, cancelTasks)
			w.log.Info("worker stopped")
			return nil
		case <-sweep.C:
			w.sweep(ctx)
		case <-poll.C:
			w.poll(ctx, taskCtx, slots, &wg)
		}
	}
}

// poll забирает столько задач, сколько свободных слотов, и запускает их обработку.
func (w *Worker) poll(ctx, taskCtx context.Context, slots chan struct{}, wg *sync.WaitGroup) {
	limit := min(w.cfg.BatchSize, cap(slots)-len(slots))
	if limit <= 0 {
		return
	}
	tasks, err := w.queue.Claim(ctx, limit, w.cfg.Lease)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("claim failed", "error", err)
		}
		return
	}
	for _, t := range tasks {
		slots <- struct{}{}
		wg.Go(func() {
			defer func() { <-slots }()
			w.process(taskCtx, t)
		})
	}
}

func (w *Worker) sweep(ctx context.Context) {
	s, err := w.queue.Sweep(ctx, w.cfg.MaxAttempts)
	if err != nil {
		if ctx.Err() == nil {
			w.log.Error("sweep failed", "error", err)
		}
		return
	}
	if s != (SweepStats{}) {
		w.log.Info("sweep", "callback_timeouts", s.CallbackTimeouts, "requeued", s.Requeued, "lease_expired", s.LeaseExpired)
	}
}

// drain ждёт текущие задачи не дольше ShutdownTimeout, затем отменяет их и дожидается выхода.
func (w *Worker) drain(wg *sync.WaitGroup, cancelTasks context.CancelFunc) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	timer := time.NewTimer(w.cfg.ShutdownTimeout)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		w.log.Warn("shutdown timeout: cancelling in-flight bank calls")
		cancelTasks()
		<-done
	}
}

// process вызывает банк и записывает результат. В лог попадают только идентификаторы — без ПДн.
func (w *Worker) process(ctx context.Context, t Task) {
	log := w.log.With("bank_request_id", t.RequestID, "application_id", t.Application.ID,
		"bank_code", t.BankCode, "attempt", t.Attempts)
	out := w.call(ctx, t)
	ok, err := w.queue.Complete(ctx, t, out)
	switch {
	case err != nil:
		log.Error("save bank result failed", "outcome", out.Kind.String(), "error", err)
	case !ok:
		log.Warn("lease lost, bank result dropped", "outcome", out.Kind.String())
	case out.Kind == OutcomeRetry || out.Kind == OutcomeFailed:
		log.Warn("bank request failed", "outcome", out.Kind.String(), "error", out.Error, "retry_after", out.RetryAfter.String())
	default:
		log.Info("bank request processed", "outcome", out.Kind.String())
	}
}

// call вызывает банк с таймаутом и превращает ответ в Outcome.
func (w *Worker) call(ctx context.Context, t Task) Outcome {
	b, ok := w.banks.Get(t.BankCode)
	if !ok {
		return Outcome{Kind: OutcomeFailed, Error: "unknown bank"}
	}
	callCtx, cancel := context.WithTimeout(ctx, w.bankTimeout(t.BankCode))
	defer cancel()
	res, err := b.Submit(callCtx, bank.SubmitRequest{RequestID: t.RequestID, Application: t.Application})
	if err != nil {
		if bank.IsPermanent(err) || t.Attempts >= w.cfg.MaxAttempts {
			return Outcome{Kind: OutcomeFailed, Error: err.Error()}
		}
		return Outcome{
			Kind:       OutcomeRetry,
			Error:      err.Error(),
			RetryAfter: Backoff(t.Attempts, w.cfg.BaseDelay, w.cfg.MaxDelay, w.rnd),
		}
	}
	if res.Decision != nil {
		return Outcome{Kind: OutcomeDecision, Decision: *res.Decision}
	}
	if res.ExternalID == "" {
		return Outcome{Kind: OutcomeFailed, Error: "bank returned neither decision nor external id"}
	}
	return Outcome{Kind: OutcomeAccepted, ExternalID: res.ExternalID, CallbackTimeout: w.cfg.CallbackTimeout}
}

func (w *Worker) bankTimeout(code string) time.Duration {
	if d, ok := w.cfg.BankTimeouts[code]; ok && d > 0 {
		return d
	}
	return DefaultBankTimeout
}
```

- [ ] **Step 10: Запустить — тесты проходят**

Run: `go test -race -count=3 ./internal/dispatch/ && make lint`
Expected: PASS трижды подряд (проверка на нестабильность), линтер без замечаний.

- [ ] **Step 11: Commit**

```bash
git add internal/dispatch/worker.go internal/dispatch/worker_test.go
git commit -m "feat(dispatch): add worker with retries, sweeper and graceful shutdown"
```

---

### Task 8: HTTP API заявок и health-проверки

**Ветка:** `feature/http-applications` · **Этап 1, локально** · **Спека:** разделы 5.1, 5.2, 5.4

**Files:**
- Create: `internal/httpapi/server.go`, `internal/httpapi/problem.go`, `internal/httpapi/applications.go`, `internal/httpapi/health.go`
- Test: `internal/httpapi/helpers_test.go`, `internal/httpapi/applications_test.go`, `internal/httpapi/health_test.go`

**Interfaces:**
- Consumes: `application.Input`, `application.View`, `application.ValidationError`, `application.FieldError`, `application.ErrNotFound`, `application.ErrIdempotencyConflict` (Task 2)
- Produces (пакет `httpapi`):
  - `type ApplicationService interface{ Create(ctx, key string, in application.Input) (application.View, error); Get(ctx, id uuid.UUID) (application.View, error) }`
  - `type Pinger interface{ Ping(ctx) error }`
  - `type Deps struct{ Applications ApplicationService; DB Pinger; Log *slog.Logger }` (Task 9 добавит поля вебхуков)
  - `func NewHandler(d Deps) http.Handler`
  - внутри пакета: `writeJSON`, `writeProblem`, `type problem`, `const maxBodyBytes`; тестовые помощники `fakeService`, `fakePinger`, `testLogger`, `do`, `decodeProblem`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/http-applications
```

- [ ] **Step 2: Написать тестовые помощники и тесты**

Файл: `internal/httpapi/helpers_test.go`
```go
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

type fakeService struct {
	create func(ctx context.Context, key string, in application.Input) (application.View, error)
	get    func(ctx context.Context, id uuid.UUID) (application.View, error)
}

func (f *fakeService) Create(ctx context.Context, key string, in application.Input) (application.View, error) {
	return f.create(ctx, key, in)
}

func (f *fakeService) Get(ctx context.Context, id uuid.UUID) (application.View, error) {
	return f.get(ctx, id)
}

type fakePinger struct{ err error }

func (p fakePinger) Ping(context.Context) error { return p.err }

func testLogger(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(t.Output(), nil))
}

func do(h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeProblem проверяет Content-Type и разбирает тело problem+json.
func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder) problem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", ct)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode problem: %v", err)
	}
	if p.Status != rec.Code {
		t.Fatalf("problem status %d != response code %d", p.Status, rec.Code)
	}
	return p
}
```

Файл: `internal/httpapi/applications_test.go`
```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

const validBody = `{"full_name":"Иванов Иван","birth_date":"1990-05-17","phone":"+79991234567","monthly_income":12000000,"employment_type":"employed","amount":30000000,"term_months":24}`

func withKey(key string) map[string]string {
	return map[string]string{"Idempotency-Key": key}
}

func newTestHandler(t *testing.T, svc *fakeService) http.Handler {
	return NewHandler(Deps{Applications: svc, DB: fakePinger{}, Log: testLogger(t)})
}

func TestCreateApplicationAccepted(t *testing.T) {
	id := uuid.New()
	var (
		gotKey   string
		gotInput application.Input
	)
	svc := &fakeService{create: func(_ context.Context, key string, in application.Input) (application.View, error) {
		gotKey, gotInput = key, in
		return application.View{ID: id, Banks: []application.BankResult{{Status: application.RequestQueued}}}, nil
	}}
	rec := do(newTestHandler(t, svc), http.MethodPost, "/v1/applications", validBody, withKey("key-1"))
	if rec.Code != http.StatusAccepted {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content type = %q", ct)
	}
	var resp createResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.ID != id || resp.Status != application.StatusProcessing {
		t.Fatalf("response = %+v", resp)
	}
	if gotKey != "key-1" || gotInput.Amount != 30_000_000 || gotInput.FullName != "Иванов Иван" {
		t.Fatalf("service got key %q and input %+v", gotKey, gotInput)
	}
}

func TestCreateApplicationRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name    string
		headers map[string]string
		body    string
	}{
		{"missing key", nil, validBody},
		{"key too long", withKey(strings.Repeat("k", 129)), validBody},
		{"non-ascii key", withKey("ключ"), validBody},
		{"malformed json", withKey("k"), `{`},
		{"unknown field", withKey("k"), `{"full_name":"Иван","extra":1}`},
		{"wrong type", withKey("k"), `{"amount":"300000"}`},
		{"trailing data", withKey("k"), validBody + `{}`},
	}
	svc := &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
		t.Error("service must not be called")
		return application.View{}, nil
	}}
	h := newTestHandler(t, svc)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := do(h, http.MethodPost, "/v1/applications", tt.body, tt.headers)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("code = %d, want 400; body %s", rec.Code, rec.Body)
			}
			decodeProblem(t, rec)
		})
	}
}

func TestCreateApplicationBodyTooLarge(t *testing.T) {
	body := `{"full_name":"` + strings.Repeat("a", 1<<20) + `"}`
	rec := do(newTestHandler(t, &fakeService{}), http.MethodPost, "/v1/applications", body, withKey("k"))
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("code = %d, want 413", rec.Code)
	}
	decodeProblem(t, rec)
}

func TestCreateApplicationServiceErrors(t *testing.T) {
	validation := &application.ValidationError{Fields: []application.FieldError{{Field: "amount", Message: "must be between"}}}
	tests := []struct {
		name string
		err  error
		code int
	}{
		{"validation", validation, http.StatusBadRequest},
		{"conflict", application.ErrIdempotencyConflict, http.StatusConflict},
		{"internal", errors.New("db is down: secret details"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &fakeService{create: func(context.Context, string, application.Input) (application.View, error) {
				return application.View{}, tt.err
			}}
			rec := do(newTestHandler(t, svc), http.MethodPost, "/v1/applications", validBody, withKey("k"))
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d", rec.Code, tt.code)
			}
			p := decodeProblem(t, rec)
			if strings.Contains(rec.Body.String(), "secret details") {
				t.Fatal("internal error details leaked to the client")
			}
			if tt.code == http.StatusBadRequest && (len(p.Errors) != 1 || p.Errors[0].Field != "amount") {
				t.Fatalf("errors = %+v, want one for amount", p.Errors)
			}
		})
	}
}

func TestGetApplication(t *testing.T) {
	id := uuid.New()
	view := application.View{
		ID:         id,
		Amount:     30_000_000,
		TermMonths: 24,
		CreatedAt:  time.Date(2026, 10, 13, 15, 0, 0, 0, time.FixedZone("MSK", 3*3600)),
		Banks: []application.BankResult{
			{BankCode: "eastbank", Status: application.RequestRejected, Attempts: 3,
				Decision: &application.Decision{RejectReason: "insufficient_income"}},
			{BankCode: "northbank", Status: application.RequestApproved, Attempts: 1,
				Decision: &application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}},
			{BankCode: "westbank", Status: application.RequestFailed, Attempts: 1, LastError: "callback timeout"},
		},
	}
	svc := &fakeService{get: func(_ context.Context, got uuid.UUID) (application.View, error) {
		if got != id {
			return application.View{}, application.ErrNotFound
		}
		return view, nil
	}}
	rec := do(newTestHandler(t, svc), http.MethodGet, "/v1/applications/"+id.String(), "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	want := `{"id":"` + id.String() + `","status":"completed","created_at":"2026-10-13T12:00:00Z","amount":30000000,"term_months":24,"banks":[` +
		`{"bank_code":"eastbank","status":"rejected","attempts":3,"reject_reason":"insufficient_income"},` +
		`{"bank_code":"northbank","status":"approved","attempts":1,"offer":{"amount":30000000,"rate_bps":1590,"term_months":24,"monthly_payment":1467460}},` +
		`{"bank_code":"westbank","status":"failed","attempts":1,"error":"callback timeout"}]}`
	if got := strings.TrimSpace(rec.Body.String()); got != want {
		t.Fatalf("body:\n%s\nwant:\n%s", got, want)
	}
}

func TestGetApplicationErrors(t *testing.T) {
	svc := &fakeService{get: func(context.Context, uuid.UUID) (application.View, error) {
		return application.View{}, application.ErrNotFound
	}}
	h := newTestHandler(t, svc)
	if rec := do(h, http.MethodGet, "/v1/applications/not-a-uuid", "", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: code = %d, want 400", rec.Code)
	}
	rec := do(h, http.MethodGet, "/v1/applications/"+uuid.NewString(), "", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown id: code = %d, want 404", rec.Code)
	}
	decodeProblem(t, rec)
}
```

Файл: `internal/httpapi/health_test.go`
```go
package httpapi

import (
	"errors"
	"net/http"
	"testing"
)

func TestHealth(t *testing.T) {
	up := NewHandler(Deps{Applications: &fakeService{}, DB: fakePinger{}, Log: testLogger(t)})
	down := NewHandler(Deps{Applications: &fakeService{}, DB: fakePinger{err: errors.New("down")}, Log: testLogger(t)})
	tests := []struct {
		name string
		h    http.Handler
		path string
		code int
	}{
		{"liveness", up, "/healthz", http.StatusOK},
		{"liveness ignores database", down, "/healthz", http.StatusOK},
		{"ready", up, "/readyz", http.StatusOK},
		{"not ready without database", down, "/readyz", http.StatusServiceUnavailable},
	}
	for _, tt := range tests {
		if rec := do(tt.h, http.MethodGet, tt.path, "", nil); rec.Code != tt.code {
			t.Errorf("%s: code = %d, want %d", tt.name, rec.Code, tt.code)
		}
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/httpapi/`
Expected: FAIL — `undefined: NewHandler`, `undefined: Deps`, `undefined: problem`.

- [ ] **Step 4: Реализовать роутер, ответы и обработчики**

Файл: `internal/httpapi/server.go`
```go
// Package httpapi — HTTP API шлюза: заявки, вебхуки банков, health-проверки.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// ApplicationService — сценарии заявок; реализация — application.Service.
type ApplicationService interface {
	Create(ctx context.Context, key string, in application.Input) (application.View, error)
	Get(ctx context.Context, id uuid.UUID) (application.View, error)
}

// Pinger проверяет доступность БД; подходит *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// Deps — зависимости API.
type Deps struct {
	Applications ApplicationService
	DB           Pinger
	Log          *slog.Logger
}

type api struct {
	Deps
}

// NewHandler собирает роутер API.
func NewHandler(d Deps) http.Handler {
	a := &api{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/applications", a.createApplication)
	mux.HandleFunc("GET /v1/applications/{id}", a.getApplication)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	return mux
}
```

Файл: `internal/httpapi/problem.go`
```go
package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// maxBodyBytes — предел размера тела запроса.
const maxBodyBytes = 1 << 20

// problem — ошибка в формате RFC 9457 (application/problem+json).
type problem struct {
	Type   string                   `json:"type"`
	Title  string                   `json:"title"`
	Status int                      `json:"status"`
	Detail string                   `json:"detail,omitempty"`
	Errors []application.FieldError `json:"errors,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeProblem(w http.ResponseWriter, status int, title, detail string, fields ...application.FieldError) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(problem{Type: "about:blank", Title: title, Status: status, Detail: detail, Errors: fields})
}
```

Файл: `internal/httpapi/applications.go`
```go
package httpapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

type createResponse struct {
	ID     uuid.UUID          `json:"id"`
	Status application.Status `json:"status"`
}

type offerResponse struct {
	Amount         int64 `json:"amount"`
	RateBPS        int   `json:"rate_bps"`
	TermMonths     int   `json:"term_months"`
	MonthlyPayment int64 `json:"monthly_payment"`
}

type bankResponse struct {
	BankCode     string                    `json:"bank_code"`
	Status       application.RequestStatus `json:"status"`
	Attempts     int                       `json:"attempts"`
	Offer        *offerResponse            `json:"offer,omitempty"`
	RejectReason string                    `json:"reject_reason,omitempty"`
	Error        string                    `json:"error,omitempty"`
}

type applicationResponse struct {
	ID         uuid.UUID          `json:"id"`
	Status     application.Status `json:"status"`
	CreatedAt  time.Time          `json:"created_at"`
	Amount     int64              `json:"amount"`
	TermMonths int                `json:"term_months"`
	Banks      []bankResponse     `json:"banks"`
}

// createApplication — POST /v1/applications (раздел 5.1 спеки).
func (a *api) createApplication(w http.ResponseWriter, r *http.Request) {
	key := r.Header.Get("Idempotency-Key")
	if !validIdempotencyKey(key) {
		writeProblem(w, http.StatusBadRequest, "Invalid Idempotency-Key",
			"header Idempotency-Key is required: 1-128 printable ASCII characters")
		return
	}
	in, status, err := decodeInput(w, r)
	if err != nil {
		writeProblem(w, status, "Invalid request body", err.Error())
		return
	}
	v, err := a.Applications.Create(r.Context(), key, in)
	var verr *application.ValidationError
	switch {
	case errors.As(err, &verr):
		writeProblem(w, http.StatusBadRequest, "Validation failed", "request body has invalid fields", verr.Fields...)
	case errors.Is(err, application.ErrIdempotencyConflict):
		writeProblem(w, http.StatusConflict, "Idempotency key conflict", err.Error())
	case err != nil:
		a.Log.Error("create application failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
	default:
		writeJSON(w, http.StatusAccepted, createResponse{ID: v.ID, Status: v.Status()})
	}
}

// getApplication — GET /v1/applications/{id} (раздел 5.2 спеки).
func (a *api) getApplication(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "Invalid id", "id must be a UUID")
		return
	}
	v, err := a.Applications.Get(r.Context(), id)
	switch {
	case errors.Is(err, application.ErrNotFound):
		writeProblem(w, http.StatusNotFound, "Not found", "application not found")
	case err != nil:
		a.Log.Error("get application failed", "application_id", id, "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
	default:
		writeJSON(w, http.StatusOK, toApplicationResponse(v))
	}
}

// decodeInput читает тело: не больше maxBodyBytes, ровно один JSON-объект, без неизвестных полей.
// Возвращает HTTP-статус для ошибки.
func decodeInput(w http.ResponseWriter, r *http.Request) (application.Input, int, error) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	var in application.Input
	if err := dec.Decode(&in); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			return in, http.StatusRequestEntityTooLarge, fmt.Errorf("body exceeds %d bytes", maxBodyBytes)
		}
		return in, http.StatusBadRequest, fmt.Errorf("malformed JSON: %w", err)
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return in, http.StatusBadRequest, errors.New("body must contain a single JSON object")
	}
	return in, 0, nil
}

func validIdempotencyKey(k string) bool {
	if len(k) < 1 || len(k) > 128 {
		return false
	}
	for i := 0; i < len(k); i++ {
		if k[i] < 0x20 || k[i] > 0x7e {
			return false
		}
	}
	return true
}

func toApplicationResponse(v application.View) applicationResponse {
	resp := applicationResponse{
		ID:         v.ID,
		Status:     v.Status(),
		CreatedAt:  v.CreatedAt.UTC(),
		Amount:     v.Amount,
		TermMonths: v.TermMonths,
		Banks:      make([]bankResponse, 0, len(v.Banks)),
	}
	for _, b := range v.Banks {
		br := bankResponse{BankCode: b.BankCode, Status: b.Status, Attempts: b.Attempts}
		switch {
		case b.Status == application.RequestApproved && b.Decision != nil:
			d := b.Decision
			br.Offer = &offerResponse{Amount: d.Amount, RateBPS: d.RateBPS, TermMonths: d.TermMonths, MonthlyPayment: d.MonthlyPayment}
		case b.Status == application.RequestRejected && b.Decision != nil:
			br.RejectReason = b.Decision.RejectReason
		case b.Status == application.RequestFailed:
			br.Error = b.LastError
		}
		resp.Banks = append(resp.Banks, br)
	}
	return resp
}
```

Файл: `internal/httpapi/health.go`
```go
package httpapi

import (
	"context"
	"net/http"
	"time"
)

// healthz — процесс жив.
func (a *api) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz — процесс готов принимать запросы: БД отвечает.
func (a *api) readyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := a.DB.Ping(ctx); err != nil {
		a.Log.Warn("readiness check failed", "error", err)
		writeProblem(w, http.StatusServiceUnavailable, "Not ready", "database is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/httpapi/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 6: Commit**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): add application endpoints and health checks"
```

---

### Task 9: Приём вебхуков банков

**Ветка:** `feature/http-webhooks` · **Этап 1, локально** · **Спека:** раздел 5.3

**Files:**
- Modify: `internal/httpapi/server.go` (целиком, новая версия ниже)
- Create: `internal/httpapi/webhooks.go`
- Test: `internal/httpapi/webhooks_test.go`

**Interfaces:**
- Consumes: `bank.CallbackHandler`, `bank.VerifyWebhook`, `bank.SignWebhook`, `bank.HeaderWebhookTimestamp`, `bank.HeaderWebhookSignature` (Tasks 3–4); `application.CallbackResult` (Task 2); помощники тестов Task 8
- Produces (пакет `httpapi`):
  - `type CallbackStore interface{ ApplyCallback(ctx, bankCode, externalID string, d application.Decision) (application.CallbackResult, error) }` — реализует `postgres.Queue`
  - `type WebhookBank struct{ Handler bank.CallbackHandler; Secret []byte }`
  - `Deps` получает поля `Callbacks CallbackStore`, `WebhookBanks map[string]WebhookBank`, `WebhookMaxSkew time.Duration`, `Now func() time.Time` (по умолчанию `time.Now`)
  - маршрут `POST /v1/webhooks/banks/{code}`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/http-webhooks
```

- [ ] **Step 2: Написать тесты вебхуков**

Файл: `internal/httpapi/webhooks_test.go`
```go
package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

var (
	webhookSecret = []byte("west-secret")
	webhookNow    = time.Unix(1_760_000_000, 0)
)

type callbackCall struct {
	bankCode, externalID string
	decision             application.Decision
}

type fakeCallbacks struct {
	result application.CallbackResult
	err    error
	calls  []callbackCall
}

func (f *fakeCallbacks) ApplyCallback(_ context.Context, bankCode, externalID string, d application.Decision) (application.CallbackResult, error) {
	f.calls = append(f.calls, callbackCall{bankCode: bankCode, externalID: externalID, decision: d})
	return f.result, f.err
}

// testCallbackHandler разбирает тело вида {"id":"...","approved":true,"reason":"..."}.
type testCallbackHandler struct{}

func (testCallbackHandler) ParseCallback(body []byte) (string, application.Decision, error) {
	var p struct {
		ID       string `json:"id"`
		Approved bool   `json:"approved"`
		Reason   string `json:"reason"`
	}
	if err := json.Unmarshal(body, &p); err != nil || p.ID == "" {
		return "", application.Decision{}, errors.New("bad callback")
	}
	if p.Approved {
		return p.ID, application.Decision{Approved: true, Amount: 100, RateBPS: 1890, TermMonths: 12, MonthlyPayment: 10}, nil
	}
	return p.ID, application.Decision{RejectReason: p.Reason}, nil
}

func newWebhookHandler(t *testing.T, cb *fakeCallbacks) http.Handler {
	return NewHandler(Deps{
		Applications:   &fakeService{},
		Callbacks:      cb,
		DB:             fakePinger{},
		WebhookBanks:   map[string]WebhookBank{"westbank": {Handler: testCallbackHandler{}, Secret: webhookSecret}},
		WebhookMaxSkew: 5 * time.Minute,
		Log:            testLogger(t),
		Now:            func() time.Time { return webhookNow },
	})
}

func signedAt(at time.Time, body string) map[string]string {
	ts := strconv.FormatInt(at.Unix(), 10)
	return map[string]string{
		bank.HeaderWebhookTimestamp: ts,
		bank.HeaderWebhookSignature: bank.SignWebhook(webhookSecret, ts, []byte(body)),
	}
}

const westPath = "/v1/webhooks/banks/westbank"

func TestWebhookAppliesDecision(t *testing.T) {
	cb := &fakeCallbacks{result: application.CallbackApplied}
	body := `{"id":"w-1","approved":true}`
	rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, signedAt(webhookNow, body))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d, body %s", rec.Code, rec.Body)
	}
	if len(cb.calls) != 1 {
		t.Fatalf("ApplyCallback calls = %d, want 1", len(cb.calls))
	}
	call := cb.calls[0]
	if call.bankCode != "westbank" || call.externalID != "w-1" || !call.decision.Approved {
		t.Fatalf("call = %+v", call)
	}
}

func TestWebhookResults(t *testing.T) {
	tests := []struct {
		name   string
		result application.CallbackResult
		err    error
		code   int
	}{
		{"duplicate or late is acknowledged", application.CallbackDuplicate, nil, http.StatusOK},
		{"unknown request", application.CallbackUnknown, nil, http.StatusNotFound},
		{"store error", 0, errors.New("db down"), http.StatusInternalServerError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &fakeCallbacks{result: tt.result, err: tt.err}
			body := `{"id":"w-1","reason":"debt_burden"}`
			rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, signedAt(webhookNow, body))
			if rec.Code != tt.code {
				t.Fatalf("code = %d, want %d", rec.Code, tt.code)
			}
		})
	}
}

func TestWebhookRejectsBadSignatures(t *testing.T) {
	body := `{"id":"w-1","approved":true}`
	tests := []struct {
		name    string
		headers map[string]string
	}{
		{"no headers", nil},
		{"signature of another body", signedAt(webhookNow, `{"id":"w-2","approved":true}`)},
		{"stale timestamp", signedAt(webhookNow.Add(-10*time.Minute), body)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cb := &fakeCallbacks{result: application.CallbackApplied}
			rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, tt.headers)
			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("code = %d, want 401", rec.Code)
			}
			decodeProblem(t, rec)
			if len(cb.calls) != 0 {
				t.Fatal("unsigned webhook must not reach the store")
			}
		})
	}
}

func TestWebhookUnknownBank(t *testing.T) {
	body := `{"id":"w-1","approved":true}`
	rec := do(newWebhookHandler(t, &fakeCallbacks{}), http.MethodPost, "/v1/webhooks/banks/northbank", body, signedAt(webhookNow, body))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("code = %d, want 404", rec.Code)
	}
}

func TestWebhookBadBody(t *testing.T) {
	cb := &fakeCallbacks{result: application.CallbackApplied}
	body := `not json`
	rec := do(newWebhookHandler(t, cb), http.MethodPost, westPath, body, signedAt(webhookNow, body))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
	if len(cb.calls) != 0 {
		t.Fatal("unparsable webhook must not reach the store")
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/httpapi/`
Expected: FAIL — `unknown field Callbacks in struct literal of type Deps`, `undefined: WebhookBank`.

- [ ] **Step 4: Расширить зависимости API**

Файл: `internal/httpapi/server.go`
```go
// Package httpapi — HTTP API шлюза: заявки, вебхуки банков, health-проверки.
package httpapi

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// ApplicationService — сценарии заявок; реализация — application.Service.
type ApplicationService interface {
	Create(ctx context.Context, key string, in application.Input) (application.View, error)
	Get(ctx context.Context, id uuid.UUID) (application.View, error)
}

// CallbackStore применяет решения из вебхуков; реализация — store/postgres.Queue.
type CallbackStore interface {
	ApplyCallback(ctx context.Context, bankCode, externalID string, d application.Decision) (application.CallbackResult, error)
}

// Pinger проверяет доступность БД; подходит *pgxpool.Pool.
type Pinger interface {
	Ping(ctx context.Context) error
}

// WebhookBank — асинхронный банк: разбор вебхука и секрет подписи.
type WebhookBank struct {
	Handler bank.CallbackHandler
	Secret  []byte
}

// Deps — зависимости API.
type Deps struct {
	Applications   ApplicationService
	Callbacks      CallbackStore
	DB             Pinger
	WebhookBanks   map[string]WebhookBank // асинхронные банки по коду
	WebhookMaxSkew time.Duration
	Log            *slog.Logger
	Now            func() time.Time // по умолчанию time.Now
}

type api struct {
	Deps
}

// NewHandler собирает роутер API.
func NewHandler(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	a := &api{Deps: d}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/applications", a.createApplication)
	mux.HandleFunc("GET /v1/applications/{id}", a.getApplication)
	mux.HandleFunc("POST /v1/webhooks/banks/{code}", a.bankWebhook)
	mux.HandleFunc("GET /healthz", a.healthz)
	mux.HandleFunc("GET /readyz", a.readyz)
	return mux
}
```

- [ ] **Step 5: Реализовать обработчик вебхука**

Файл: `internal/httpapi/webhooks.go`
```go
package httpapi

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

type webhookResponse struct {
	Result string `json:"result"`
}

// bankWebhook — POST /v1/webhooks/banks/{code}: асинхронное решение банка (раздел 5.3 спеки).
func (a *api) bankWebhook(w http.ResponseWriter, r *http.Request) {
	code := r.PathValue("code")
	wb, ok := a.WebhookBanks[code]
	if !ok {
		writeProblem(w, http.StatusNotFound, "Unknown bank", fmt.Sprintf("bank %q does not accept webhooks", code))
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			writeProblem(w, http.StatusRequestEntityTooLarge, "Body too large", fmt.Sprintf("body exceeds %d bytes", maxBodyBytes))
			return
		}
		writeProblem(w, http.StatusBadRequest, "Invalid request body", "cannot read body")
		return
	}

	log := a.Log.With("bank_code", code)
	ts, sig := r.Header.Get(bank.HeaderWebhookTimestamp), r.Header.Get(bank.HeaderWebhookSignature)
	if err := bank.VerifyWebhook(wb.Secret, ts, sig, body, a.Now(), a.WebhookMaxSkew); err != nil {
		log.Warn("webhook rejected", "error", err)
		writeProblem(w, http.StatusUnauthorized, "Invalid signature", "webhook signature is missing, invalid or expired")
		return
	}
	externalID, decision, err := wb.Handler.ParseCallback(body)
	if err != nil {
		log.Warn("webhook body rejected", "error", err)
		writeProblem(w, http.StatusBadRequest, "Invalid callback", err.Error())
		return
	}

	log = log.With("external_id", externalID)
	res, err := a.Callbacks.ApplyCallback(r.Context(), code, externalID, decision)
	if err != nil {
		log.Error("apply webhook failed", "error", err)
		writeProblem(w, http.StatusInternalServerError, "Internal error", "")
		return
	}
	switch res {
	case application.CallbackApplied:
		log.Info("webhook applied")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "applied"})
	case application.CallbackDuplicate:
		log.Warn("webhook ignored: request is not waiting for a callback")
		writeJSON(w, http.StatusOK, webhookResponse{Result: "ignored"})
	default:
		log.Warn("webhook for unknown request")
		writeProblem(w, http.StatusNotFound, "Unknown request", "no request with this external id")
	}
}
```

- [ ] **Step 6: Запустить — тесты проходят**

Run: `go test ./internal/httpapi/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 7: Commit**

```bash
git add internal/httpapi
git commit -m "feat(httpapi): accept signed bank webhooks"
```

---

### Task 10: Заглушка банков: правила, общая механика, диалект north

**Ветка:** `feature/mockbank-north` · **Этап 1, локально** · **Спека:** разделы 6.3 (northbank), 6.4, 6.5

**Files:**
- Create: `internal/mockbank/rules.go`, `internal/mockbank/server.go`, `internal/mockbank/north.go`
- Test: `internal/mockbank/rules_test.go`, `internal/mockbank/server_test.go`, `internal/mockbank/north_test.go`

**Interfaces:**
- Consumes: `application.Age`, `application.Decision`, `application.EmploymentType` (Task 2); `bank.ParseRubles`, `bank.FormatRubles`, `bank.FormatPercent` (Task 4)
- Produces (пакет `mockbank`):
  - `type Profile struct{ MinAge, MaxAge int; RejectEmployment []application.EmploymentType; MinIncome, MaxAmount int64; RateBPS int }`; `var NorthProfile, EastProfile, WestProfile Profile`
  - `type LoanRequest struct{ BirthDate time.Time; MonthlyIncome int64; Employment application.EmploymentType; Amount int64; TermMonths int }`
  - `func (Profile) Decide(req LoanRequest, now time.Time) application.Decision`; `func Annuity(amount int64, rateBPS, months int) int64`
  - `type Options struct{ Latency time.Duration; FailFirst int; Now func() time.Time }`
  - `func NewNorth(opts Options) http.Handler`
  - внутри пакета: `newServer(opts) *server`, `(*server).handle(w, r, key, produce func(body []byte) (int, any))`, `type errorResponse`, `writeJSON`, `writeRaw`; в тестах — `var testNow`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/mockbank-north
```

- [ ] **Step 2: Написать тесты правил решений**

Файл: `internal/mockbank/rules_test.go`
```go
package mockbank

import (
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

var testNow = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func sampleRequest() LoanRequest {
	return LoanRequest{
		BirthDate:     time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		MonthlyIncome: 12_000_000,
		Employment:    application.EmploymentEmployed,
		Amount:        30_000_000,
		TermMonths:    24,
	}
}

func TestAnnuity(t *testing.T) {
	tests := []struct {
		rateBPS int
		want    int64
	}{
		{1590, 1_467_460},
		{1490, 1_453_175},
		{1890, 1_510_801},
	}
	for _, tt := range tests {
		if got := Annuity(30_000_000, tt.rateBPS, 24); got != tt.want {
			t.Errorf("Annuity(300000 ₽, %d bps, 24) = %d, want %d", tt.rateBPS, got, tt.want)
		}
	}
}

func TestProfilesApproveSampleRequest(t *testing.T) {
	for name, p := range map[string]Profile{"north": NorthProfile, "east": EastProfile, "west": WestProfile} {
		want := application.Decision{
			Approved:       true,
			Amount:         30_000_000,
			RateBPS:        p.RateBPS,
			TermMonths:     24,
			MonthlyPayment: Annuity(30_000_000, p.RateBPS, 24),
		}
		if got := p.Decide(sampleRequest(), testNow); got != want {
			t.Errorf("%s: got %+v, want %+v", name, got, want)
		}
	}
}

func TestDecideRejections(t *testing.T) {
	tests := []struct {
		name    string
		profile Profile
		edit    func(*LoanRequest)
		want    string
	}{
		{"north: 20 years old", NorthProfile, func(r *LoanRequest) { r.BirthDate = time.Date(2006, 1, 1, 0, 0, 0, 0, time.UTC) }, "age_out_of_range"},
		{"east: 66 years old", EastProfile, func(r *LoanRequest) { r.BirthDate = time.Date(1960, 1, 1, 0, 0, 0, 0, time.UTC) }, "age_out_of_range"},
		{"east: pensioner", EastProfile, func(r *LoanRequest) { r.Employment = application.EmploymentPensioner }, "employment_not_accepted"},
		{"north: income below minimum", NorthProfile, func(r *LoanRequest) { r.MonthlyIncome = 2_999_999 }, "insufficient_income"},
		{"west: amount above limit", WestProfile, func(r *LoanRequest) { r.Amount = 200_000_001 }, "amount_exceeds_limit"},
		{"north: payment above half of income", NorthProfile, func(r *LoanRequest) {
			r.MonthlyIncome = 5_000_000
			r.Amount = 100_000_000
		}, "debt_burden"},
		{"east: age is checked before employment", EastProfile, func(r *LoanRequest) {
			r.Employment = application.EmploymentPensioner
			r.BirthDate = time.Date(1956, 1, 1, 0, 0, 0, 0, time.UTC)
		}, "age_out_of_range"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := sampleRequest()
			tt.edit(&req)
			got := tt.profile.Decide(req, testNow)
			if got.Approved || got.RejectReason != tt.want {
				t.Fatalf("got %+v, want rejection %q", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/mockbank/`
Expected: FAIL — `undefined: LoanRequest`, `undefined: Annuity`.

- [ ] **Step 4: Реализовать правила**

Файл: `internal/mockbank/rules.go`
```go
// Package mockbank — заглушки банков-партнёров: правила решений (раздел 6.4 спеки),
// общая механика (задержка, искусственные сбои, идемпотентность) и диалекты банков.
package mockbank

import (
	"math"
	"slices"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Profile — правила решений одного банка.
type Profile struct {
	MinAge, MaxAge   int
	RejectEmployment []application.EmploymentType
	MinIncome        int64 // копейки
	MaxAmount        int64 // копейки
	RateBPS          int
}

// Профили банков из раздела 6.4 спеки.
var (
	NorthProfile = Profile{MinAge: 21, MaxAge: 70, MinIncome: 3_000_000, MaxAmount: 300_000_000, RateBPS: 1590}
	EastProfile  = Profile{MinAge: 18, MaxAge: 65, RejectEmployment: []application.EmploymentType{application.EmploymentPensioner}, MinIncome: 5_000_000, MaxAmount: 500_000_000, RateBPS: 1490}
	WestProfile  = Profile{MinAge: 18, MaxAge: 75, MinIncome: 2_500_000, MaxAmount: 200_000_000, RateBPS: 1890}
)

// LoanRequest — заявка в терминах заглушки, независимо от диалекта.
type LoanRequest struct {
	BirthDate     time.Time
	MonthlyIncome int64
	Employment    application.EmploymentType
	Amount        int64
	TermMonths    int
}

// Decide применяет проверки по порядку; первая сработавшая даёт отказ.
// Одобряется запрошенная сумма на запрошенный срок.
func (p Profile) Decide(req LoanRequest, now time.Time) application.Decision {
	if age := application.Age(req.BirthDate, now); age < p.MinAge || age > p.MaxAge {
		return reject("age_out_of_range")
	}
	if slices.Contains(p.RejectEmployment, req.Employment) {
		return reject("employment_not_accepted")
	}
	if req.MonthlyIncome < p.MinIncome {
		return reject("insufficient_income")
	}
	if req.Amount > p.MaxAmount {
		return reject("amount_exceeds_limit")
	}
	payment := Annuity(req.Amount, p.RateBPS, req.TermMonths)
	if payment*2 > req.MonthlyIncome {
		return reject("debt_burden")
	}
	return application.Decision{
		Approved:       true,
		Amount:         req.Amount,
		RateBPS:        p.RateBPS,
		TermMonths:     req.TermMonths,
		MonthlyPayment: payment,
	}
}

func reject(reason string) application.Decision {
	return application.Decision{RejectReason: reason}
}

// Annuity — ежемесячный аннуитетный платёж в копейках: P = S·i / (1 − (1+i)^(−n)), i = ставка/12,
// округление до копейки, половина — вверх. Единственное место в проекте, где допустим float.
func Annuity(amount int64, rateBPS, months int) int64 {
	i := float64(rateBPS) / 10000 / 12
	p := float64(amount) * i / (1 - math.Pow(1+i, -float64(months)))
	return int64(math.Floor(p + 0.5))
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/mockbank/`
Expected: PASS

- [ ] **Step 6: Commit**

```bash
git add internal/mockbank/rules.go internal/mockbank/rules_test.go
git commit -m "feat(mockbank): add deterministic decision rules"
```

- [ ] **Step 7: Написать тесты общей механики**

Файл: `internal/mockbank/server_test.go`
```go
package mockbank

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// echoHandler — минимальный диалект для проверки общей механики server.
func echoHandler(s *server, status int, calls *int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.handle(w, r, r.Header.Get("Key"), func(body []byte) (int, any) {
			*calls++ // produce вызывается под мьютексом server
			return status, map[string]string{"echo": string(body)}
		})
	})
}

func send(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServerFailFirstPerKey(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{FailFirst: 2}), http.StatusOK, &calls)
	for i, want := range []int{503, 503, 200, 200} {
		if rec := send(h, "k-1", "a"); rec.Code != want {
			t.Fatalf("request %d: code %d, want %d", i+1, rec.Code, want)
		}
	}
	if rec := send(h, "k-2", "a"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("new key: code %d, want 503 — failures are counted per key", rec.Code)
	}
	if calls != 1 {
		t.Fatalf("produce called %d times, want 1", calls)
	}
}

func TestServerReplaysSuccessfulResponse(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{}), http.StatusOK, &calls)
	first := send(h, "k-1", "a")
	second := send(h, "k-1", "b")
	if second.Code != http.StatusOK || second.Body.String() != first.Body.String() {
		t.Fatalf("repeat returned %d %s, want the saved %s", second.Code, second.Body, first.Body)
	}
	if calls != 1 {
		t.Fatalf("produce called %d times, want 1", calls)
	}
}

func TestServerDoesNotSaveErrors(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{}), http.StatusBadRequest, &calls)
	send(h, "k-1", "a")
	send(h, "k-1", "a")
	if calls != 2 {
		t.Fatalf("produce called %d times, want 2 — errors must not be cached", calls)
	}
}

func TestServerRequiresKey(t *testing.T) {
	calls := 0
	if rec := send(echoHandler(newServer(Options{}), http.StatusOK, &calls), "", "a"); rec.Code != http.StatusBadRequest {
		t.Fatalf("code = %d, want 400", rec.Code)
	}
}

func TestServerLatency(t *testing.T) {
	calls := 0
	h := echoHandler(newServer(Options{Latency: 50 * time.Millisecond}), http.StatusOK, &calls)
	start := time.Now()
	send(h, "k-1", "a")
	if elapsed := time.Since(start); elapsed < 50*time.Millisecond {
		t.Fatalf("response took %s, want at least 50ms", elapsed)
	}
}
```

- [ ] **Step 8: Запустить — тесты должны упасть**

Run: `go test ./internal/mockbank/`
Expected: FAIL — `undefined: server`, `undefined: newServer`, `undefined: Options`.

- [ ] **Step 9: Реализовать общую механику**

Файл: `internal/mockbank/server.go`
```go
package mockbank

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Options — поведение заглушки, общее для всех диалектов (раздел 6.5 спеки).
type Options struct {
	Latency   time.Duration    // задержка перед каждым ответом
	FailFirst int              // первые N запросов с каждым ключом идемпотентности получают 503
	Now       func() time.Time // по умолчанию time.Now
}

type savedResponse struct {
	status int
	body   []byte
}

// server — общая для диалектов механика: задержка, искусственные сбои, идемпотентность.
// Состояние хранится в памяти.
type server struct {
	opts     Options
	mu       sync.Mutex
	failures map[string]int
	saved    map[string]savedResponse
}

func newServer(opts Options) *server {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &server{opts: opts, failures: map[string]int{}, saved: map[string]savedResponse{}}
}

type errorResponse struct {
	Error string `json:"error"`
}

// handle обслуживает запрос диалекта. produce получает тело и возвращает статус и ответ.
// Успешный (2xx) ответ сохраняется и отдаётся на повторы с тем же ключом; искусственные
// 503 и ошибки не сохраняются. produce вызывается под мьютексом.
func (s *server) handle(w http.ResponseWriter, r *http.Request, key string, produce func(body []byte) (int, any)) {
	if s.opts.Latency > 0 {
		select {
		case <-time.After(s.opts.Latency):
		case <-r.Context().Done():
			return
		}
	}
	if key == "" {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "missing idempotency key"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: "cannot read body"})
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if saved, ok := s.saved[key]; ok {
		writeRaw(w, saved.status, saved.body)
		return
	}
	if s.failures[key] < s.opts.FailFirst {
		s.failures[key]++
		writeJSON(w, http.StatusServiceUnavailable, errorResponse{Error: "temporarily unavailable"})
		return
	}
	status, v := produce(body)
	data, err := json.Marshal(v)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, errorResponse{Error: "cannot encode response"})
		return
	}
	if status >= 200 && status < 300 {
		s.saved[key] = savedResponse{status: status, body: data}
	}
	writeRaw(w, status, data)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	data, _ := json.Marshal(v) // v — простые структуры, ошибки быть не может
	writeRaw(w, status, data)
}

func writeRaw(w http.ResponseWriter, status int, data []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}
```

- [ ] **Step 10: Запустить — тесты проходят**

Run: `go test ./internal/mockbank/`
Expected: PASS

- [ ] **Step 11: Commit**

```bash
git add internal/mockbank/server.go internal/mockbank/server_test.go
git commit -m "feat(mockbank): add latency, fail-first and idempotent replay"
```

- [ ] **Step 12: Написать тесты диалекта north**

Файл: `internal/mockbank/north_test.go`
```go
package mockbank

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const northSample = `{"client":{"name":"Иванов Иван","birthDate":"1990-05-17","phone":"+79991234567"},"income":"120000.00","employment":"EMPLOYED","loan":{"sum":"300000.00","months":24}}`

func postNorth(h http.Handler, requestID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/loan-requests", strings.NewReader(body))
	if requestID != "" {
		req.Header.Set("X-Request-Id", requestID)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestNorth() http.Handler {
	return NewNorth(Options{Now: func() time.Time { return testNow }})
}

func TestNorthApproves(t *testing.T) {
	rec := postNorth(newTestNorth(), "r-1", northSample)
	want := `{"decision":"APPROVED","offer":{"sum":"300000.00","ratePercent":"15.90","months":24,"payment":"14674.60"}}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestNorthDeclines(t *testing.T) {
	body := strings.Replace(northSample, `"income":"120000.00"`, `"income":"20000.00"`, 1)
	rec := postNorth(newTestNorth(), "r-1", body)
	want := `{"decision":"DECLINED","reason":"insufficient_income"}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestNorthRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, requestID, body string
	}{
		{"missing request id", "", northSample},
		{"malformed json", "r-1", `{`},
		{"bad birth date", "r-1", strings.Replace(northSample, "1990-05-17", "17.05.1990", 1)},
		{"bad sum", "r-1", strings.Replace(northSample, `"sum":"300000.00"`, `"sum":"abc"`, 1)},
		{"zero months", "r-1", strings.Replace(northSample, `"months":24`, `"months":0`, 1)},
	}
	for _, tt := range tests {
		if rec := postNorth(newTestNorth(), tt.requestID, tt.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", tt.name, rec.Code)
		}
	}
}
```

- [ ] **Step 13: Запустить — тесты должны упасть**

Run: `go test ./internal/mockbank/`
Expected: FAIL — `undefined: NewNorth`.

- [ ] **Step 14: Реализовать диалект north**

Файл: `internal/mockbank/north.go`
```go
package mockbank

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Диалект north: синхронный JSON, рубли и проценты строками, camelCase (раздел 6.3 спеки).

type northClient struct {
	Name      string `json:"name"`
	BirthDate string `json:"birthDate"`
	Phone     string `json:"phone"`
}

type northLoan struct {
	Sum    string `json:"sum"`
	Months int    `json:"months"`
}

type northRequest struct {
	Client     northClient `json:"client"`
	Income     string      `json:"income"`
	Employment string      `json:"employment"`
	Loan       northLoan   `json:"loan"`
}

type northOffer struct {
	Sum         string `json:"sum"`
	RatePercent string `json:"ratePercent"`
	Months      int    `json:"months"`
	Payment     string `json:"payment"`
}

type northResponse struct {
	Decision string      `json:"decision"`
	Offer    *northOffer `json:"offer,omitempty"`
	Reason   string      `json:"reason,omitempty"`
}

// NewNorth — заглушка northbank: POST /api/v1/loan-requests, ключ идемпотентности — X-Request-Id.
func NewNorth(opts Options) http.Handler {
	s := newServer(opts)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/loan-requests", func(w http.ResponseWriter, r *http.Request) {
		s.handle(w, r, r.Header.Get("X-Request-Id"), func(body []byte) (int, any) {
			var req northRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return http.StatusBadRequest, errorResponse{Error: "invalid json"}
			}
			lr, err := req.toLoanRequest()
			if err != nil {
				return http.StatusBadRequest, errorResponse{Error: err.Error()}
			}
			return http.StatusOK, northResponseFrom(NorthProfile.Decide(lr, s.opts.Now()))
		})
	})
	return mux
}

func (r northRequest) toLoanRequest() (LoanRequest, error) {
	birth, err := time.Parse(time.DateOnly, r.Client.BirthDate)
	if err != nil {
		return LoanRequest{}, errors.New("client.birthDate must be YYYY-MM-DD")
	}
	income, err := bank.ParseRubles(r.Income)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("income: %w", err)
	}
	sum, err := bank.ParseRubles(r.Loan.Sum)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("loan.sum: %w", err)
	}
	if r.Loan.Months <= 0 {
		return LoanRequest{}, errors.New("loan.months must be positive")
	}
	return LoanRequest{
		BirthDate:     birth,
		MonthlyIncome: income,
		Employment:    application.EmploymentType(strings.ToLower(r.Employment)),
		Amount:        sum,
		TermMonths:    r.Loan.Months,
	}, nil
}

func northResponseFrom(d application.Decision) northResponse {
	if !d.Approved {
		return northResponse{Decision: "DECLINED", Reason: d.RejectReason}
	}
	return northResponse{Decision: "APPROVED", Offer: &northOffer{
		Sum:         bank.FormatRubles(d.Amount),
		RatePercent: bank.FormatPercent(d.RateBPS),
		Months:      d.TermMonths,
		Payment:     bank.FormatRubles(d.MonthlyPayment),
	}}
}
```

- [ ] **Step 15: Запустить — тесты проходят**

Run: `go test -race ./internal/mockbank/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 16: Commit**

```bash
git add internal/mockbank/north.go internal/mockbank/north_test.go
git commit -m "feat(mockbank): add north dialect"
```

---

### Task 11: Адаптер northbank

**Ветка:** `feature/northbank` · **Этап 1, локально** · **Спека:** разделы 6.1–6.3

**Files:**
- Create: `internal/bank/northbank/northbank.go`
- Test: `internal/bank/northbank/northbank_test.go`

**Interfaces:**
- Consumes: `bank.Bank`, `bank.SubmitRequest`, `bank.SubmitResult`, `bank.PostJSON`, `bank.StatusError`, `bank.ErrPermanent`, `bank.IsPermanent`, `bank.FormatRubles`, `bank.ParseRubles`, `bank.ParsePercent` (Tasks 3–4); `mockbank.NewNorth`, `mockbank.Options` (Task 10)
- Produces (пакет `northbank`): `const Code = "northbank"`; `func New(baseURL string, httpClient *http.Client) *Client` — реализует `bank.Bank`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/northbank
```

- [ ] **Step 2: Написать тесты адаптера**

Файл: `internal/bank/northbank/northbank_test.go`
```go
package northbank

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func testApp() application.Application {
	return application.Application{
		ID:             uuid.New(),
		FullName:       "Иванов Иван",
		BirthDate:      time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: application.EmploymentSelfEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}

const approvedBody = `{"decision":"APPROVED","offer":{"sum":"300000.00","ratePercent":"15.90","months":24,"payment":"14674.60"}}`

func TestSubmitSendsNorthbankRequest(t *testing.T) {
	reqID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/loan-requests" || r.Header.Get("X-Request-Id") != reqID.String() {
			t.Errorf("unexpected request %s %s, X-Request-Id %q", r.Method, r.URL.Path, r.Header.Get("X-Request-Id"))
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"client":{"name":"Иванов Иван","birthDate":"1990-05-17","phone":"+79991234567"},"income":"120000.00","employment":"SELF_EMPLOYED","loan":{"sum":"300000.00","months":24}}`
		if string(body) != want {
			t.Errorf("body:\n%s\nwant:\n%s", body, want)
		}
		_, _ = w.Write([]byte(approvedBody))
	}))
	defer srv.Close()

	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: reqID, Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved || res.ExternalID != "" {
		t.Fatalf("result = %+v, want synchronous approval", res)
	}
}

func TestSubmitKeepsKopecks(t *testing.T) {
	var got struct {
		Income string `json:"income"`
		Loan   struct {
			Sum string `json:"sum"`
		} `json:"loan"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		_, _ = w.Write([]byte(approvedBody))
	}))
	defer srv.Close()

	app := testApp()
	app.Amount = 30_000_050
	app.MonthlyIncome = 12_000_001
	if _, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app}); err != nil {
		t.Fatal(err)
	}
	if got.Loan.Sum != "300000.50" || got.Income != "120000.01" {
		t.Fatalf("sum %q, income %q; want 300000.50 and 120000.01", got.Loan.Sum, got.Income)
	}
}

func TestSubmitDeclined(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"decision":"DECLINED","reason":"debt_burden"}`))
	}))
	defer srv.Close()
	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || res.Decision.Approved || res.Decision.RejectReason != "debt_burden" {
		t.Fatalf("result = %+v, want rejection", res)
	}
}

func TestSubmitErrorClasses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"server error", http.StatusServiceUnavailable, `{}`, false},
		{"rate limited", http.StatusTooManyRequests, `{}`, false},
		{"bad request", http.StatusBadRequest, `{}`, true},
		{"malformed json", http.StatusOK, `not json`, true},
		{"unknown decision", http.StatusOK, `{"decision":"MAYBE"}`, true},
		{"approved without offer", http.StatusOK, `{"decision":"APPROVED"}`, true},
		{"bad offer", http.StatusOK, `{"decision":"APPROVED","offer":{"sum":"abc","ratePercent":"15.90","months":24,"payment":"1.00"}}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
			if err == nil {
				t.Fatal("want error")
			}
			if bank.IsPermanent(err) != tt.permanent {
				t.Fatalf("err = %v, permanent = %v, want %v", err, bank.IsPermanent(err), tt.permanent)
			}
		})
	}
}

func TestSubmitAgainstMockbank(t *testing.T) {
	srv := httptest.NewServer(mockbank.NewNorth(mockbank.Options{}))
	defer srv.Close()
	app := testApp()
	app.EmploymentType = application.EmploymentEmployed
	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved {
		t.Fatalf("result = %+v, want %+v", res, approved)
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/northbank/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 4: Реализовать адаптер**

Файл: `internal/bank/northbank/northbank.go`
```go
// Package northbank — адаптер northbank: синхронный JSON, рубли и проценты строками,
// camelCase (раздел 6.3 спеки).
package northbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Code — код банка в шлюзе.
const Code = "northbank"

type client struct {
	Name      string `json:"name"`
	BirthDate string `json:"birthDate"`
	Phone     string `json:"phone"`
}

type loan struct {
	Sum    string `json:"sum"`
	Months int    `json:"months"`
}

type request struct {
	Client     client `json:"client"`
	Income     string `json:"income"`
	Employment string `json:"employment"`
	Loan       loan   `json:"loan"`
}

type offer struct {
	Sum         string `json:"sum"`
	RatePercent string `json:"ratePercent"`
	Months      int    `json:"months"`
	Payment     string `json:"payment"`
}

type response struct {
	Decision string `json:"decision"`
	Offer    *offer `json:"offer"`
	Reason   string `json:"reason"`
}

// Client — адаптер northbank.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт адаптер; baseURL — адрес банка без завершающего слеша.
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, http: httpClient}
}

// Code возвращает код банка.
func (c *Client) Code() string { return Code }

// Submit отправляет заявку и возвращает синхронное решение.
func (c *Client) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	a := req.Application
	body := request{
		Client:     client{Name: a.FullName, BirthDate: a.BirthDate.Format(time.DateOnly), Phone: a.Phone},
		Income:     bank.FormatRubles(a.MonthlyIncome),
		Employment: strings.ToUpper(string(a.EmploymentType)),
		Loan:       loan{Sum: bank.FormatRubles(a.Amount), Months: a.TermMonths},
	}
	headers := map[string]string{"X-Request-Id": req.RequestID.String()}
	status, data, err := bank.PostJSON(ctx, c.http, c.baseURL+"/api/v1/loan-requests", headers, body)
	if err != nil {
		return bank.SubmitResult{}, err
	}
	if status != http.StatusOK {
		return bank.SubmitResult{}, bank.StatusError(status)
	}
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return bank.SubmitResult{}, fmt.Errorf("%w: decode response: %w", bank.ErrPermanent, err)
	}
	d, err := resp.decision()
	if err != nil {
		return bank.SubmitResult{}, err
	}
	return bank.SubmitResult{Decision: &d}, nil
}

func (r response) decision() (application.Decision, error) {
	switch r.Decision {
	case "DECLINED":
		return application.Decision{RejectReason: r.Reason}, nil
	case "APPROVED":
		if r.Offer == nil {
			return application.Decision{}, fmt.Errorf("%w: approved without offer", bank.ErrPermanent)
		}
		amount, errAmount := bank.ParseRubles(r.Offer.Sum)
		rate, errRate := bank.ParsePercent(r.Offer.RatePercent)
		payment, errPayment := bank.ParseRubles(r.Offer.Payment)
		if err := errors.Join(errAmount, errRate, errPayment); err != nil {
			return application.Decision{}, fmt.Errorf("%w: bad offer: %w", bank.ErrPermanent, err)
		}
		return application.Decision{
			Approved:       true,
			Amount:         amount,
			RateBPS:        rate,
			TermMonths:     r.Offer.Months,
			MonthlyPayment: payment,
		}, nil
	default:
		return application.Decision{}, fmt.Errorf("%w: unknown decision %q", bank.ErrPermanent, r.Decision)
	}
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/bank/... && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 6: Commit**

```bash
git add internal/bank/northbank
git commit -m "feat(bank): add northbank adapter"
```

---

### Task 12: Конфигурация из переменных окружения

**Ветка:** `feature/config` · **Этап 1, локально** · **Спека:** разделы 6.5, 9

**Files:**
- Create: `internal/config/config.go`
- Test: `internal/config/config_test.go`

**Interfaces:**
- Consumes: —
- Produces (пакет `config`):
  - `type Bank struct{ Code, URL string; Timeout time.Duration; WebhookSecret string }`
  - `type Gateway struct{ DatabaseURL, HTTPAddr string; LogLevel slog.Level; ShutdownTimeout, PollInterval time.Duration; BatchSize, Concurrency int; Lease time.Duration; MaxAttempts int; BaseDelay, MaxDelay, CallbackTimeout, SweepInterval, WebhookMaxSkew time.Duration; Banks []Bank }`
  - `func LoadGateway(getenv func(string) string, knownBanks []string) (Gateway, error)`
  - `type Mockbank struct{ Addr, Dialect string; Latency time.Duration; FailFirst int; CallbackURL, CallbackSecret string; CallbackDelay time.Duration; CallbackSkip bool }`
  - `func LoadMockbank(getenv func(string) string) (Mockbank, error)`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/config
```

- [ ] **Step 2: Написать тесты**

Файл: `internal/config/config_test.go`
```go
package config

import (
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"
)

func env(vars map[string]string) func(string) string {
	return func(name string) string { return vars[name] }
}

var knownBanks = []string{"eastbank", "northbank", "westbank"}

func TestLoadGatewayDefaults(t *testing.T) {
	cfg, err := LoadGateway(env(map[string]string{
		"DATABASE_URL":       "postgres://db",
		"BANK_NORTHBANK_URL": "http://northbank:8090",
	}), knownBanks)
	if err != nil {
		t.Fatal(err)
	}
	want := Gateway{
		DatabaseURL:     "postgres://db",
		HTTPAddr:        ":8080",
		LogLevel:        slog.LevelInfo,
		ShutdownTimeout: 20 * time.Second,
		PollInterval:    500 * time.Millisecond,
		BatchSize:       10,
		Concurrency:     10,
		Lease:           30 * time.Second,
		MaxAttempts:     5,
		BaseDelay:       time.Second,
		MaxDelay:        time.Minute,
		CallbackTimeout: 10 * time.Minute,
		SweepInterval:   5 * time.Second,
		WebhookMaxSkew:  5 * time.Minute,
		Banks:           []Bank{{Code: "northbank", URL: "http://northbank:8090", Timeout: 5 * time.Second}},
	}
	if !reflect.DeepEqual(cfg, want) {
		t.Fatalf("got  %+v\nwant %+v", cfg, want)
	}
}

func TestLoadGatewayOverrides(t *testing.T) {
	cfg, err := LoadGateway(env(map[string]string{
		"DATABASE_URL":                 "postgres://db",
		"HTTP_ADDR":                    ":9090",
		"LOG_LEVEL":                    "debug",
		"WORKER_CONCURRENCY":           "3",
		"RETRY_MAX_ATTEMPTS":           "7",
		"CALLBACK_TIMEOUT":             "30s",
		"BANK_WESTBANK_URL":            "http://westbank:8090",
		"BANK_WESTBANK_TIMEOUT":        "2s",
		"BANK_WESTBANK_WEBHOOK_SECRET": "secret",
	}), knownBanks)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTPAddr != ":9090" || cfg.LogLevel != slog.LevelDebug || cfg.Concurrency != 3 ||
		cfg.MaxAttempts != 7 || cfg.CallbackTimeout != 30*time.Second {
		t.Fatalf("overrides not applied: %+v", cfg)
	}
	wantBanks := []Bank{{Code: "westbank", URL: "http://westbank:8090", Timeout: 2 * time.Second, WebhookSecret: "secret"}}
	if !reflect.DeepEqual(cfg.Banks, wantBanks) {
		t.Fatalf("banks = %+v, want %+v", cfg.Banks, wantBanks)
	}
}

func TestLoadGatewayWithoutBanksIsAllowedForMigrate(t *testing.T) {
	cfg, err := LoadGateway(env(map[string]string{"DATABASE_URL": "postgres://db"}), knownBanks)
	if err != nil || len(cfg.Banks) != 0 {
		t.Fatalf("got %+v, %v", cfg.Banks, err)
	}
}

func TestLoadGatewayErrors(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{"missing database url", map[string]string{}, "DATABASE_URL is required"},
		{"bad duration", map[string]string{"DATABASE_URL": "x", "WORKER_LEASE": "soon"}, "WORKER_LEASE"},
		{"zero batch size", map[string]string{"DATABASE_URL": "x", "WORKER_BATCH_SIZE": "0"}, "WORKER_BATCH_SIZE"},
		{"bad log level", map[string]string{"DATABASE_URL": "x", "LOG_LEVEL": "loud"}, "LOG_LEVEL"},
		{"lease not above bank timeout", map[string]string{
			"DATABASE_URL": "x", "WORKER_LEASE": "5s", "BANK_NORTHBANK_URL": "http://northbank",
		}, "WORKER_LEASE (5s) must be greater than BANK_NORTHBANK_TIMEOUT (5s)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadGateway(env(tt.vars), knownBanks)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

func TestLoadMockbankDefaults(t *testing.T) {
	cfg, err := LoadMockbank(env(map[string]string{"MOCKBANK_DIALECT": "north"}))
	if err != nil {
		t.Fatal(err)
	}
	want := Mockbank{Addr: ":8090", Dialect: "north", CallbackDelay: 5 * time.Second}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadMockbankWest(t *testing.T) {
	cfg, err := LoadMockbank(env(map[string]string{
		"MOCKBANK_DIALECT":         "west",
		"MOCKBANK_LATENCY":         "1s",
		"MOCKBANK_FAIL_FIRST":      "2",
		"MOCKBANK_CALLBACK_URL":    "http://api:8080/v1/webhooks/banks/westbank",
		"MOCKBANK_CALLBACK_SECRET": "secret",
		"MOCKBANK_CALLBACK_DELAY":  "1s",
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := Mockbank{
		Addr:           ":8090",
		Dialect:        "west",
		Latency:        time.Second,
		FailFirst:      2,
		CallbackURL:    "http://api:8080/v1/webhooks/banks/westbank",
		CallbackSecret: "secret",
		CallbackDelay:  time.Second,
	}
	if cfg != want {
		t.Fatalf("got %+v, want %+v", cfg, want)
	}
}

func TestLoadMockbankWestWithSkipNeedsNoCallback(t *testing.T) {
	if _, err := LoadMockbank(env(map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_SKIP": "true"})); err != nil {
		t.Fatal(err)
	}
}

func TestLoadMockbankErrors(t *testing.T) {
	tests := []struct {
		name    string
		vars    map[string]string
		wantErr string
	}{
		{"missing dialect", map[string]string{}, "MOCKBANK_DIALECT is required"},
		{"unknown dialect", map[string]string{"MOCKBANK_DIALECT": "south"}, "MOCKBANK_DIALECT must be"},
		{"west without callback url", map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_SECRET": "s"}, "MOCKBANK_CALLBACK_URL"},
		{"west without secret", map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_URL": "http://api"}, "MOCKBANK_CALLBACK_SECRET"},
		{"negative fail first", map[string]string{"MOCKBANK_DIALECT": "north", "MOCKBANK_FAIL_FIRST": "-1"}, "MOCKBANK_FAIL_FIRST"},
		{"bad skip flag", map[string]string{"MOCKBANK_DIALECT": "west", "MOCKBANK_CALLBACK_SKIP": "maybe"}, "MOCKBANK_CALLBACK_SKIP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadMockbank(env(tt.vars))
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/config/`
Expected: FAIL — `undefined: LoadGateway`, `undefined: LoadMockbank`.

- [ ] **Step 4: Реализовать конфигурацию**

Файл: `internal/config/config.go`
```go
// Package config читает конфигурацию шлюза и заглушки банков из переменных окружения.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"
)

// Bank — настройки одного включённого банка.
type Bank struct {
	Code          string
	URL           string
	Timeout       time.Duration
	WebhookSecret string
}

// Gateway — конфигурация бинарника gateway (раздел 9 спеки).
type Gateway struct {
	DatabaseURL     string
	HTTPAddr        string
	LogLevel        slog.Level
	ShutdownTimeout time.Duration
	PollInterval    time.Duration
	BatchSize       int
	Concurrency     int
	Lease           time.Duration
	MaxAttempts     int
	BaseDelay       time.Duration
	MaxDelay        time.Duration
	CallbackTimeout time.Duration
	SweepInterval   time.Duration
	WebhookMaxSkew  time.Duration
	Banks           []Bank // только банки с заданным BANK_<CODE>_URL, в порядке knownBanks
}

// LoadGateway читает конфигурацию gateway. knownBanks — коды банков, для которых есть адаптеры;
// банк включён, если задан BANK_<CODE>_URL. Отсутствие банков не ошибка: migrate они не нужны.
func LoadGateway(getenv func(string) string, knownBanks []string) (Gateway, error) {
	r := reader{getenv: getenv}
	cfg := Gateway{
		DatabaseURL:     r.required("DATABASE_URL"),
		HTTPAddr:        r.str("HTTP_ADDR", ":8080"),
		LogLevel:        r.logLevel("LOG_LEVEL", slog.LevelInfo),
		ShutdownTimeout: r.positiveDuration("SHUTDOWN_TIMEOUT", 20*time.Second),
		PollInterval:    r.positiveDuration("WORKER_POLL_INTERVAL", 500*time.Millisecond),
		BatchSize:       r.positiveInt("WORKER_BATCH_SIZE", 10),
		Concurrency:     r.positiveInt("WORKER_CONCURRENCY", 10),
		Lease:           r.positiveDuration("WORKER_LEASE", 30*time.Second),
		MaxAttempts:     r.positiveInt("RETRY_MAX_ATTEMPTS", 5),
		BaseDelay:       r.positiveDuration("RETRY_BASE_DELAY", time.Second),
		MaxDelay:        r.positiveDuration("RETRY_MAX_DELAY", time.Minute),
		CallbackTimeout: r.positiveDuration("CALLBACK_TIMEOUT", 10*time.Minute),
		SweepInterval:   r.positiveDuration("SWEEP_INTERVAL", 5*time.Second),
		WebhookMaxSkew:  r.positiveDuration("WEBHOOK_MAX_SKEW", 5*time.Minute),
	}
	for _, code := range knownBanks {
		prefix := "BANK_" + strings.ToUpper(code) + "_"
		baseURL := getenv(prefix + "URL")
		if baseURL == "" {
			continue
		}
		b := Bank{
			Code:          code,
			URL:           baseURL,
			Timeout:       r.positiveDuration(prefix+"TIMEOUT", 5*time.Second),
			WebhookSecret: getenv(prefix + "WEBHOOK_SECRET"),
		}
		if cfg.Lease <= b.Timeout {
			r.fail(fmt.Errorf("WORKER_LEASE (%s) must be greater than %sTIMEOUT (%s)", cfg.Lease, prefix, b.Timeout))
		}
		cfg.Banks = append(cfg.Banks, b)
	}
	return cfg, r.err()
}

// Mockbank — конфигурация заглушки банка (раздел 6.5 спеки).
type Mockbank struct {
	Addr           string
	Dialect        string // north | east | west
	Latency        time.Duration
	FailFirst      int
	CallbackURL    string
	CallbackSecret string
	CallbackDelay  time.Duration
	CallbackSkip   bool
}

// LoadMockbank читает конфигурацию заглушки.
func LoadMockbank(getenv func(string) string) (Mockbank, error) {
	r := reader{getenv: getenv}
	cfg := Mockbank{
		Addr:           r.str("MOCKBANK_ADDR", ":8090"),
		Dialect:        r.required("MOCKBANK_DIALECT"),
		Latency:        r.nonNegativeDuration("MOCKBANK_LATENCY", 0),
		FailFirst:      r.nonNegativeInt("MOCKBANK_FAIL_FIRST", 0),
		CallbackURL:    getenv("MOCKBANK_CALLBACK_URL"),
		CallbackSecret: getenv("MOCKBANK_CALLBACK_SECRET"),
		CallbackDelay:  r.nonNegativeDuration("MOCKBANK_CALLBACK_DELAY", 5*time.Second),
		CallbackSkip:   r.boolean("MOCKBANK_CALLBACK_SKIP", false),
	}
	switch cfg.Dialect {
	case "", "north", "east": // пустой диалект уже отмечен в required
	case "west":
		if !cfg.CallbackSkip && cfg.CallbackURL == "" {
			r.fail(errors.New("MOCKBANK_CALLBACK_URL is required for the west dialect"))
		}
		if !cfg.CallbackSkip && cfg.CallbackSecret == "" {
			r.fail(errors.New("MOCKBANK_CALLBACK_SECRET is required for the west dialect"))
		}
	default:
		r.fail(fmt.Errorf("MOCKBANK_DIALECT must be north, east or west, got %q", cfg.Dialect))
	}
	return cfg, r.err()
}

// reader читает переменные и копит ошибки, чтобы показать их все разом.
type reader struct {
	getenv func(string) string
	errs   []error
}

func (r *reader) fail(err error) {
	r.errs = append(r.errs, err)
}

func (r *reader) err() error {
	return errors.Join(r.errs...)
}

func (r *reader) str(name, def string) string {
	if v := r.getenv(name); v != "" {
		return v
	}
	return def
}

func (r *reader) required(name string) string {
	v := r.getenv(name)
	if v == "" {
		r.fail(fmt.Errorf("%s is required", name))
	}
	return v
}

func (r *reader) positiveDuration(name string, def time.Duration) time.Duration {
	return r.duration(name, def, false)
}

func (r *reader) nonNegativeDuration(name string, def time.Duration) time.Duration {
	return r.duration(name, def, true)
}

func (r *reader) duration(name string, def time.Duration, allowZero bool) time.Duration {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 || (d == 0 && !allowZero) {
		r.fail(fmt.Errorf("%s: invalid duration %q", name, v))
		return def
	}
	return d
}

func (r *reader) positiveInt(name string, def int) int {
	return r.integer(name, def, 1)
}

func (r *reader) nonNegativeInt(name string, def int) int {
	return r.integer(name, def, 0)
}

func (r *reader) integer(name string, def, minValue int) int {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < minValue {
		r.fail(fmt.Errorf("%s: must be an integer >= %d, got %q", name, minValue, v))
		return def
	}
	return n
}

func (r *reader) boolean(name string, def bool) bool {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		r.fail(fmt.Errorf("%s: must be true or false, got %q", name, v))
		return def
	}
	return b
}

func (r *reader) logLevel(name string, def slog.Level) slog.Level {
	v := r.getenv(name)
	if v == "" {
		return def
	}
	var level slog.Level
	if err := level.UnmarshalText([]byte(v)); err != nil {
		r.fail(fmt.Errorf("%s: invalid level %q", name, v))
		return def
	}
	return level
}
```

- [ ] **Step 5: Запустить — тесты проходят**

Run: `go test ./internal/config/ && make lint`
Expected: PASS, линтер без замечаний.

- [ ] **Step 6: Commit**

```bash
git add internal/config
git commit -m "feat(config): load gateway and mockbank settings from env"
```

---

### Task 13: Бинарники gateway и mockbank

**Ветка:** `feature/binaries` · **Этап 1, локально** · **Спека:** разделы 3, 8 (остановка), 9

**Files:**
- Modify: `cmd/gateway/main.go` (целиком, новая версия ниже)
- Create: `cmd/gateway/banks.go`, `cmd/gateway/api.go`, `cmd/gateway/worker.go`, `cmd/mockbank/main.go`
- Test: `cmd/gateway/banks_test.go`, `cmd/mockbank/main_test.go`

**Interfaces:**
- Consumes: `config.LoadGateway`, `config.LoadMockbank`, `config.Gateway`, `config.Mockbank` (Task 12); `postgres.Connect`, `postgres.Migrate`, `postgres.NewApplications`, `postgres.NewQueue` (Tasks 5–6); `dispatch.New`, `dispatch.Config` (Task 7); `httpapi.NewHandler`, `httpapi.Deps`, `httpapi.WebhookBank` (Tasks 8–9); `application.NewService` (Task 2); `bank.NewRegistry`, `bank.CallbackHandler` (Task 3); `northbank.New`, `northbank.Code` (Task 11); `mockbank.NewNorth`, `mockbank.Options` (Task 10)
- Produces:
  - `cmd/gateway`: `var adapters map[string]func(baseURL string, c *http.Client) bank.Bank` — реестр адаптеров, сюда добавляются новые банки; `func bankCodes() []string`; `func buildBanks(cfg config.Gateway) (*bank.Registry, map[string]httpapi.WebhookBank, error)`; `func bankTimeouts(cfg config.Gateway) map[string]time.Duration`
  - `cmd/mockbank`: `func newHandler(ctx context.Context, cfg config.Mockbank, log *slog.Logger) (http.Handler, error)` — выбор диалекта

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/binaries
```

- [ ] **Step 2: Написать тесты сборки банков**

Файл: `cmd/gateway/banks_test.go`
```go
package main

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
)

// asyncBank — фейковый асинхронный банк для проверки требований к секрету вебхука.
type asyncBank struct{}

func (asyncBank) Code() string { return "asyncbank" }

func (asyncBank) Submit(context.Context, bank.SubmitRequest) (bank.SubmitResult, error) {
	return bank.SubmitResult{}, nil
}

func (asyncBank) ParseCallback([]byte) (string, application.Decision, error) {
	return "", application.Decision{}, nil
}

func withAsyncBank(t *testing.T) {
	t.Helper()
	adapters["asyncbank"] = func(string, *http.Client) bank.Bank { return asyncBank{} }
	t.Cleanup(func() { delete(adapters, "asyncbank") })
}

func TestBankCodesAreSorted(t *testing.T) {
	codes := bankCodes()
	if !slices.IsSorted(codes) || !slices.Contains(codes, "northbank") {
		t.Fatalf("codes = %v", codes)
	}
}

func TestBuildBanks(t *testing.T) {
	cfg := config.Gateway{Banks: []config.Bank{{Code: "northbank", URL: "http://northbank:8090", Timeout: 2 * time.Second}}}
	reg, hooks, err := buildBanks(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(reg.Codes(), []string{"northbank"}) || len(hooks) != 0 {
		t.Fatalf("codes = %v, hooks = %v", reg.Codes(), hooks)
	}
	if got := bankTimeouts(cfg)["northbank"]; got != 2*time.Second {
		t.Fatalf("timeout = %s, want 2s", got)
	}
}

func TestBuildBanksRequiresAtLeastOneBank(t *testing.T) {
	if _, _, err := buildBanks(config.Gateway{}); err == nil {
		t.Fatal("want error without banks")
	}
}

func TestBuildBanksRequiresWebhookSecretForAsyncBank(t *testing.T) {
	withAsyncBank(t)
	cfg := config.Gateway{Banks: []config.Bank{{Code: "asyncbank", URL: "http://async"}}}
	if _, _, err := buildBanks(cfg); err == nil || !strings.Contains(err.Error(), "BANK_ASYNCBANK_WEBHOOK_SECRET") {
		t.Fatalf("err = %v, want missing secret error", err)
	}
	cfg.Banks[0].WebhookSecret = "s"
	_, hooks, err := buildBanks(cfg)
	if err != nil || string(hooks["asyncbank"].Secret) != "s" {
		t.Fatalf("hooks = %v, err = %v", hooks, err)
	}
}
```

Файл: `cmd/mockbank/main_test.go`
```go
package main

import (
	"log/slog"
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
)

func TestNewHandlerSelectsDialect(t *testing.T) {
	log := slog.New(slog.DiscardHandler)
	if h, err := newHandler(t.Context(), config.Mockbank{Dialect: "north"}, log); err != nil || h == nil {
		t.Fatalf("north: handler %v, err %v", h, err)
	}
	if _, err := newHandler(t.Context(), config.Mockbank{Dialect: "south"}, log); err == nil {
		t.Fatal("unknown dialect must fail")
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./cmd/...`
Expected: FAIL — `undefined: adapters`, `undefined: buildBanks`, `undefined: newHandler`.

- [ ] **Step 4: Реализовать gateway**

Файл: `cmd/gateway/banks.go`
```go
package main

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/northbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
)

// adapters — банки, которые умеет шлюз. Новый банк = новый адаптер + строка здесь.
var adapters = map[string]func(baseURL string, c *http.Client) bank.Bank{
	northbank.Code: func(u string, c *http.Client) bank.Bank { return northbank.New(u, c) },
}

// bankCodes — коды банков с адаптерами, по алфавиту.
func bankCodes() []string {
	return slices.Sorted(maps.Keys(adapters))
}

// buildBanks собирает реестр включённых банков и настройки вебхуков асинхронных банков.
func buildBanks(cfg config.Gateway) (*bank.Registry, map[string]httpapi.WebhookBank, error) {
	client := &http.Client{}
	var banks []bank.Bank
	hooks := map[string]httpapi.WebhookBank{}
	for _, bc := range cfg.Banks {
		newAdapter, ok := adapters[bc.Code]
		if !ok {
			return nil, nil, fmt.Errorf("no adapter for bank %q", bc.Code)
		}
		b := newAdapter(bc.URL, client)
		banks = append(banks, b)
		if h, ok := b.(bank.CallbackHandler); ok {
			if bc.WebhookSecret == "" {
				return nil, nil, fmt.Errorf("BANK_%s_WEBHOOK_SECRET is required: the bank sends decisions via webhook",
					strings.ToUpper(bc.Code))
			}
			hooks[bc.Code] = httpapi.WebhookBank{Handler: h, Secret: []byte(bc.WebhookSecret)}
		}
	}
	reg, err := bank.NewRegistry(banks...)
	if err != nil {
		return nil, nil, err
	}
	return reg, hooks, nil
}

// bankTimeouts — таймауты вызова по коду банка.
func bankTimeouts(cfg config.Gateway) map[string]time.Duration {
	timeouts := make(map[string]time.Duration, len(cfg.Banks))
	for _, b := range cfg.Banks {
		timeouts[b.Code] = b.Timeout
	}
	return timeouts
}
```

Файл: `cmd/gateway/main.go`
```go
// Command gateway — шлюз заявок на кредит. Подкоманды: api, worker, migrate.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

const usage = "usage: gateway api|worker|migrate"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "gateway:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) != 1 {
		return errors.New(usage)
	}
	cfg, err := config.LoadGateway(os.Getenv, bankCodes())
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: cfg.LogLevel}))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	switch args[0] {
	case "migrate":
		if err := postgres.Migrate(ctx, cfg.DatabaseURL); err != nil {
			return err
		}
		log.Info("migrations applied")
		return nil
	case "api":
		return runAPI(ctx, cfg, log)
	case "worker":
		return runWorker(ctx, cfg, log)
	default:
		return fmt.Errorf("unknown command %q; %s", args[0], usage)
	}
}
```

Файл: `cmd/gateway/api.go`
```go
package main

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

// runAPI обслуживает HTTP API до отмены ctx, затем корректно останавливает сервер.
func runAPI(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, hooks, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	handler := httpapi.NewHandler(httpapi.Deps{
		Applications:   application.NewService(postgres.NewApplications(pool), reg.Codes(), time.Now),
		Callbacks:      postgres.NewQueue(pool),
		DB:             pool,
		WebhookBanks:   hooks,
		WebhookMaxSkew: cfg.WebhookMaxSkew,
		Log:            log,
	})
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("api started", "addr", cfg.HTTPAddr, "banks", reg.Codes())

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	log.Info("api stopping")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.ShutdownTimeout)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
```

Файл: `cmd/gateway/worker.go`
```go
package main

import (
	"context"
	"log/slog"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
)

// runWorker обрабатывает очередь до отмены ctx.
func runWorker(ctx context.Context, cfg config.Gateway, log *slog.Logger) error {
	reg, _, err := buildBanks(cfg)
	if err != nil {
		return err
	}
	pool, err := postgres.Connect(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()

	w := dispatch.New(postgres.NewQueue(pool), reg, dispatch.Config{
		PollInterval:    cfg.PollInterval,
		BatchSize:       cfg.BatchSize,
		Concurrency:     cfg.Concurrency,
		Lease:           cfg.Lease,
		MaxAttempts:     cfg.MaxAttempts,
		BaseDelay:       cfg.BaseDelay,
		MaxDelay:        cfg.MaxDelay,
		CallbackTimeout: cfg.CallbackTimeout,
		SweepInterval:   cfg.SweepInterval,
		ShutdownTimeout: cfg.ShutdownTimeout,
		BankTimeouts:    bankTimeouts(cfg),
	}, log)
	return w.Run(ctx)
}
```

- [ ] **Step 5: Реализовать mockbank**

Файл: `cmd/mockbank/main.go`
```go
// Command mockbank — заглушка банка-партнёра; диалект выбирается переменной MOCKBANK_DIALECT.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/config"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "mockbank:", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.LoadMockbank(os.Getenv)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	handler, err := newHandler(ctx, cfg, log)
	if err != nil {
		return err
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: handler, ReadHeaderTimeout: 5 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	log.Info("mockbank started", "dialect", cfg.Dialect, "addr", cfg.Addr)

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// newHandler выбирает диалект заглушки. ctx и log понадобятся асинхронному диалекту west.
func newHandler(_ context.Context, cfg config.Mockbank, _ *slog.Logger) (http.Handler, error) {
	opts := mockbank.Options{Latency: cfg.Latency, FailFirst: cfg.FailFirst}
	switch cfg.Dialect {
	case "north":
		return mockbank.NewNorth(opts), nil
	default:
		return nil, fmt.Errorf("dialect %q is not implemented", cfg.Dialect)
	}
}
```

- [ ] **Step 6: Запустить — тесты проходят, бинарники собираются**

Run: `go test ./cmd/... && go build ./... && go run ./cmd/gateway; echo "exit=$?"`
Expected: тесты PASS; последняя команда печатает `gateway: usage: gateway api|worker|migrate` и `exit=1`.

- [ ] **Step 7: Commit**

```bash
git add cmd
git commit -m "feat(cmd): wire gateway api, worker, migrate and mockbank binaries"
```

---

### Task 14: End-to-end тесты на northbank

**Ветка:** `feature/e2e-northbank` · **Этап 1, локально** · **Спека:** раздел 11 (E2E)

**Files:**
- Create: `test/e2e/harness_test.go`
- Test: `test/e2e/northbank_test.go`

**Interfaces:**
- Consumes: всё из Tasks 2–11: `pgtest.NewPool`, `postgres.NewApplications`, `postgres.NewQueue`, `application.NewService`, `httpapi.NewHandler`, `httpapi.Deps`, `httpapi.WebhookBank`, `dispatch.New`, `dispatch.Config`, `bank.NewRegistry`, `bank.CallbackHandler`, `mockbank.NewNorth`, `northbank.New`
- Produces (пакет `e2e`, только тесты): `type mockBank struct{ code string; mock func(apiURL string) http.Handler; adapter func(mockURL string, c *http.Client) bank.Bank; secret []byte; timeout time.Duration }`; `type options struct{ banks []mockBank; maxAttempts int; callbackTimeout time.Duration }`; `func start(t, options) *env`; `(*env).post(key, body) (int, applicationView)`, `(*env).get(id)`, `(*env).waitCompleted(id)`, `(*env).submit(edit func(map[string]any)) applicationView`; `func sampleInput() map[string]any`; `(applicationView).bank(t, code) bankView`; `type offerView`, `type bankView`; `func northbankMock(o mockbank.Options) mockBank`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/e2e-northbank
```

- [ ] **Step 2: Написать окружение e2e**

Файл: `test/e2e/harness_test.go`
```go
// Package e2e — end-to-end тесты: API и воркер в процессе, заглушки банков через httptest,
// PostgreSQL в Docker. Пропускаются при -short.
package e2e

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/dispatch"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/httpapi"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/store/postgres/pgtest"
)

// mockBank описывает банк в e2e-окружении.
type mockBank struct {
	code    string
	mock    func(apiURL string) http.Handler               // заглушка; apiURL — адрес шлюза для вебхуков
	adapter func(mockURL string, c *http.Client) bank.Bank // адаптер шлюза, направленный на заглушку
	secret  []byte                                         // секрет вебхуков асинхронного банка
	timeout time.Duration                                  // таймаут вызова; 0 — 500 мс
}

type options struct {
	banks           []mockBank
	maxAttempts     int           // 0 — 5
	callbackTimeout time.Duration // 0 — 5 с
}

type env struct {
	t    *testing.T
	url  string
	pool *pgxpool.Pool
}

// start поднимает шлюз (API и воркер) с заглушками банков; всё останавливается в t.Cleanup.
func start(t *testing.T, o options) *env {
	t.Helper()
	pool := pgtest.NewPool(t)
	log := slog.New(slog.NewTextHandler(t.Output(), &slog.HandlerOptions{Level: slog.LevelWarn}))

	// Адрес API нужен заглушкам для вебхуков раньше, чем собран его обработчик.
	api := httptest.NewUnstartedServer(nil)
	apiURL := "http://" + api.Listener.Addr().String()

	client := &http.Client{}
	var banks []bank.Bank
	hooks := map[string]httpapi.WebhookBank{}
	timeouts := map[string]time.Duration{}
	for _, mb := range o.banks {
		mock := httptest.NewServer(mb.mock(apiURL))
		t.Cleanup(mock.Close)
		b := mb.adapter(mock.URL, client)
		banks = append(banks, b)
		if h, ok := b.(bank.CallbackHandler); ok {
			hooks[mb.code] = httpapi.WebhookBank{Handler: h, Secret: mb.secret}
		}
		timeouts[mb.code] = cmp.Or(mb.timeout, 500*time.Millisecond)
	}
	reg, err := bank.NewRegistry(banks...)
	if err != nil {
		t.Fatal(err)
	}

	queue := postgres.NewQueue(pool)
	api.Config.Handler = httpapi.NewHandler(httpapi.Deps{
		Applications:   application.NewService(postgres.NewApplications(pool), reg.Codes(), time.Now),
		Callbacks:      queue,
		DB:             pool,
		WebhookBanks:   hooks,
		WebhookMaxSkew: 5 * time.Minute,
		Log:            log,
	})
	api.Start()
	t.Cleanup(api.Close)

	worker := dispatch.New(queue, reg, dispatch.Config{
		PollInterval:    20 * time.Millisecond,
		BatchSize:       10,
		Concurrency:     10,
		Lease:           5 * time.Second,
		MaxAttempts:     cmp.Or(o.maxAttempts, 5),
		BaseDelay:       10 * time.Millisecond,
		MaxDelay:        50 * time.Millisecond,
		CallbackTimeout: cmp.Or(o.callbackTimeout, 5*time.Second),
		SweepInterval:   50 * time.Millisecond,
		ShutdownTimeout: 2 * time.Second,
		BankTimeouts:    timeouts,
	}, log)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = worker.Run(ctx)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	return &env{t: t, url: apiURL, pool: pool}
}

type offerView struct {
	Amount         int64 `json:"amount"`
	RateBPS        int   `json:"rate_bps"`
	TermMonths     int   `json:"term_months"`
	MonthlyPayment int64 `json:"monthly_payment"`
}

type bankView struct {
	BankCode     string     `json:"bank_code"`
	Status       string     `json:"status"`
	Attempts     int        `json:"attempts"`
	Offer        *offerView `json:"offer"`
	RejectReason string     `json:"reject_reason"`
	Error        string     `json:"error"`
}

type applicationView struct {
	ID     string     `json:"id"`
	Status string     `json:"status"`
	Banks  []bankView `json:"banks"`
}

// bank возвращает результат по банку code.
func (v applicationView) bank(t *testing.T, code string) bankView {
	t.Helper()
	for _, b := range v.Banks {
		if b.BankCode == code {
			return b
		}
	}
	t.Fatalf("no result for bank %s in %+v", code, v)
	return bankView{}
}

// sampleInput — анкета из раздела 5.1 спеки.
func sampleInput() map[string]any {
	return map[string]any{
		"full_name":       "Иванов Иван Иванович",
		"birth_date":      "1990-05-17",
		"phone":           "+79991234567",
		"monthly_income":  12_000_000,
		"employment_type": "employed",
		"amount":          30_000_000,
		"term_months":     24,
	}
}

// post отправляет заявку и возвращает код ответа и тело (для 202).
func (e *env) post(key string, body map[string]any) (int, applicationView) {
	e.t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		e.t.Fatal(err)
	}
	req, err := http.NewRequest(http.MethodPost, e.url+"/v1/applications", bytes.NewReader(payload))
	if err != nil {
		e.t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("POST: %v", err)
	}
	defer resp.Body.Close()
	var v applicationView
	if resp.StatusCode == http.StatusAccepted {
		if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
			e.t.Fatalf("decode POST response: %v", err)
		}
	} else {
		_, _ = io.Copy(io.Discard, resp.Body)
	}
	return resp.StatusCode, v
}

// get читает заявку.
func (e *env) get(id string) applicationView {
	e.t.Helper()
	resp, err := http.Get(e.url + "/v1/applications/" + id)
	if err != nil {
		e.t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		e.t.Fatalf("GET returned %d", resp.StatusCode)
	}
	var v applicationView
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil {
		e.t.Fatalf("decode GET response: %v", err)
	}
	return v
}

// waitCompleted опрашивает заявку, пока она не станет completed (не дольше 10 с).
func (e *env) waitCompleted(id string) applicationView {
	e.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v := e.get(id)
		if v.Status == "completed" {
			return v
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("application %s is not completed in 10s: %+v", id, v)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// submit отправляет sampleInput, изменённый edit, и ждёт завершения заявки.
func (e *env) submit(edit func(map[string]any)) applicationView {
	e.t.Helper()
	in := sampleInput()
	if edit != nil {
		edit(in)
	}
	code, created := e.post(uuid.NewString(), in)
	if code != http.StatusAccepted {
		e.t.Fatalf("POST returned %d, want 202", code)
	}
	return e.waitCompleted(created.ID)
}
```

- [ ] **Step 3: Написать сценарии northbank**

Файл: `test/e2e/northbank_test.go`
```go
package e2e

import (
	"net/http"
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/northbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func northbankMock(o mockbank.Options) mockBank {
	return mockBank{
		code:    northbank.Code,
		mock:    func(string) http.Handler { return mockbank.NewNorth(o) },
		adapter: func(url string, c *http.Client) bank.Bank { return northbank.New(url, c) },
	}
}

func TestNorthbankApproves(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	got := e.submit(nil).bank(t, northbank.Code)
	want := offerView{Amount: 30_000_000, RateBPS: 1590, TermMonths: 24, MonthlyPayment: 1_467_460}
	if got.Status != "approved" || got.Attempts != 1 || got.Offer == nil || *got.Offer != want {
		t.Fatalf("northbank = %+v, want approved with %+v", got, want)
	}
}

func TestNorthbankRejects(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	got := e.submit(func(in map[string]any) { in["monthly_income"] = 2_000_000 }).bank(t, northbank.Code)
	if got.Status != "rejected" || got.RejectReason != "insufficient_income" {
		t.Fatalf("northbank = %+v, want rejected with insufficient_income", got)
	}
}

func TestRepeatedPostReturnsSameApplication(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	code1, first := e.post("repeat-key", sampleInput())
	code2, second := e.post("repeat-key", sampleInput())
	if code1 != http.StatusAccepted || code2 != http.StatusAccepted || first.ID != second.ID {
		t.Fatalf("got %d %s and %d %s, want the same application twice", code1, first.ID, code2, second.ID)
	}
	var n int
	err := e.pool.QueryRow(t.Context(), `SELECT count(*) FROM bank_requests WHERE application_id = $1`, first.ID).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("bank requests = %d, want 1", n)
	}
}

func TestSameKeyWithDifferentBodyConflicts(t *testing.T) {
	e := start(t, options{banks: []mockBank{northbankMock(mockbank.Options{})}})
	if code, _ := e.post("conflict-key", sampleInput()); code != http.StatusAccepted {
		t.Fatalf("first POST returned %d", code)
	}
	other := sampleInput()
	other["amount"] = 40_000_000
	if code, _ := e.post("conflict-key", other); code != http.StatusConflict {
		t.Fatalf("second POST returned %d, want 409", code)
	}
}
```

- [ ] **Step 4: Запустить — тесты проходят**

Run: `go test -race -count=1 ./test/e2e/ && go test -short ./test/e2e/ && make lint`
Expected: PASS; с `-short` тесты пропускаются; линтер без замечаний.

Эта задача проверяет уже написанный код, поэтому падающего шага нет. Если тест падает — это найденный дефект: разобраться по `superpowers:systematic-debugging` и исправить в пакете, где он живёт.

- [ ] **Step 5: Commit**

```bash
git add test/e2e
git commit -m "test(e2e): cover northbank flow and idempotency end to end"
```

---

### Task 15: Docker-образ, docker compose и демо

**Ветка:** `feature/compose-demo` · **Этап 1, локально** · **Спека:** раздел 12

**Files:**
- Create: `Dockerfile`, `.dockerignore`, `compose.yaml`, `scripts/demo.sh`

**Interfaces:**
- Consumes: бинарники `gateway` и `mockbank` (Task 13); цели `make up`, `make down`, `make demo` (Task 1)
- Produces: локальное окружение `docker compose`; сервисы `postgres`, `migrate`, `api`, `worker`, `northbank`; якоря YAML `x-app` и `x-gateway-env`, в которые Tasks 16–17 добавляют банки

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/compose-demo
```

- [ ] **Step 2: Создать образ**

Файл: `Dockerfile`
```dockerfile
# Сборка обоих бинарников; финальный образ — distroless без shell.
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/gateway ./cmd/gateway && \
    CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/mockbank ./cmd/mockbank

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/gateway /out/mockbank /usr/local/bin/
USER nonroot:nonroot
```

Файл: `.dockerignore`
```
.git
.github
.idea
.claude
docs
test
*.md
```

- [ ] **Step 3: Создать окружение compose**

Файл: `compose.yaml`
```yaml
# Локальное окружение: PostgreSQL, миграции, API, воркер и заглушки банков.
x-app: &app
  image: loan-gateway:dev
  build: .

x-gateway-env: &gateway-env
  DATABASE_URL: postgres://gateway:gateway@postgres:5432/gateway?sslmode=disable
  LOG_LEVEL: info
  BANK_NORTHBANK_URL: http://northbank:8090

services:
  postgres:
    image: postgres:18-alpine
    environment:
      POSTGRES_USER: gateway
      POSTGRES_PASSWORD: gateway
      POSTGRES_DB: gateway
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U gateway -d gateway"]
      interval: 2s
      timeout: 3s
      retries: 15

  migrate:
    <<: *app
    command: ["gateway", "migrate"]
    environment: *gateway-env
    depends_on:
      postgres:
        condition: service_healthy

  api:
    <<: *app
    command: ["gateway", "api"]
    environment: *gateway-env
    ports:
      - "8080:8080"
    depends_on:
      migrate:
        condition: service_completed_successfully

  worker:
    <<: *app
    command: ["gateway", "worker"]
    environment: *gateway-env
    depends_on:
      migrate:
        condition: service_completed_successfully

  northbank:
    <<: *app
    command: ["mockbank"]
    environment:
      MOCKBANK_DIALECT: north
```

- [ ] **Step 4: Создать демо-скрипт**

Файл: `scripts/demo.sh`
```bash
#!/usr/bin/env bash
# Отправляет заявку в локальный шлюз и ждёт решений банков. Нужны curl и jq.
set -euo pipefail

API="${API:-http://localhost:8080}"
command -v jq >/dev/null || { echo "нужен jq: sudo apt install jq" >&2; exit 1; }

echo "Жду готовности API на $API ..."
for _ in $(seq 1 30); do
  curl -sf "$API/readyz" >/dev/null && break
  sleep 1
done
curl -sf "$API/readyz" >/dev/null || { echo "API не готов за 30 с" >&2; exit 1; }

KEY="demo-$(date +%s)-$RANDOM"
BODY='{
  "full_name": "Иванов Иван Иванович",
  "birth_date": "1990-05-17",
  "phone": "+79991234567",
  "monthly_income": 12000000,
  "employment_type": "employed",
  "amount": 30000000,
  "term_months": 24
}'

echo "POST /v1/applications (Idempotency-Key: $KEY)"
ID=$(curl -sf -X POST "$API/v1/applications" \
  -H "Content-Type: application/json" -H "Idempotency-Key: $KEY" -d "$BODY" | jq -r .id)
echo "Заявка $ID принята, жду решений банков ..."

for _ in $(seq 1 60); do
  RESP=$(curl -sf "$API/v1/applications/$ID")
  if [ "$(jq -r .status <<<"$RESP")" = "completed" ]; then
    jq . <<<"$RESP"
    exit 0
  fi
  sleep 1
done
echo "Заявка не завершилась за 60 с:" >&2
jq . <<<"$RESP" >&2
exit 1
```

```bash
chmod +x scripts/demo.sh
```

- [ ] **Step 5: Проверить систему целиком**

Run: `make up && make demo; make down`
Expected: `make demo` печатает JSON со `"status": "completed"` и банком `northbank` в статусе `approved` с `"monthly_payment": 1467460`. Логи при необходимости: `docker compose logs api worker northbank`.

- [ ] **Step 6: Commit**

```bash
git add Dockerfile .dockerignore compose.yaml scripts/demo.sh
git commit -m "feat: add docker image, compose environment and demo script"
```

---

### Task 16: Банк eastbank (облачная сессия)

**Ветка:** `feature/eastbank` · **Этап 2, облако, параллельно с Task 17** · **Спека:** разделы 6.3 (eastbank), 6.4, 11, 12

Задача независима от Task 17. Обе правят `cmd/gateway/banks.go`, `cmd/mockbank/main.go`, `cmd/mockbank/main_test.go` и `compose.yaml` — точечно, в разных строках; конфликт при merge второго PR решается объединением обеих правок.

**Промпт для облачной сессии:** «Выполни Task 16 из docs/plan.md в ветке feature/eastbank от develop. Следуй CLAUDE.md. В конце — `make lint && make test`, строка в docs/dev-log.md и PR в develop.»

**Files:**
- Create: `internal/mockbank/east.go`, `internal/bank/eastbank/eastbank.go`
- Modify: `cmd/gateway/banks.go` (реестр `adapters`), `cmd/mockbank/main.go` (`newHandler`), `cmd/mockbank/main_test.go`, `compose.yaml`
- Test: `internal/mockbank/east_test.go`, `internal/bank/eastbank/eastbank_test.go`, `test/e2e/eastbank_test.go`

**Interfaces:**
- Consumes: `mockbank.newServer`, `(*server).handle`, `errorResponse`, `EastProfile`, `LoanRequest`, `testNow` (Task 10); `bank.PostJSON`, `bank.StatusError`, `bank.ErrPermanent`, `bank.IsPermanent` (Task 3); e2e-окружение `start`, `options`, `mockBank`, `offerView`, `(*env).submit` (Task 14)
- Produces: `func mockbank.NewEast(opts Options) http.Handler`; `const eastbank.Code = "eastbank"`; `func eastbank.New(baseURL string, httpClient *http.Client) *eastbank.Client`; в e2e — `func eastbankMock(o mockbank.Options, timeout time.Duration) mockBank`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/eastbank
```

- [ ] **Step 2: Написать тесты диалекта east**

Файл: `internal/mockbank/east_test.go`
```go
package mockbank

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const eastSample = `{"applicant_name":"Иванов Иван","date_of_birth":"1990-05-17","phone_number":"+79991234567","income_kopecks":12000000,"employment_kind":"employed","requested_kopecks":30000000,"term":24}`

func postEast(h http.Handler, key, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v2/applications", strings.NewReader(body))
	if key != "" {
		req.Header.Set("Idempotency-Key", key)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func newTestEast() http.Handler {
	return NewEast(Options{Now: func() time.Time { return testNow }})
}

func TestEastApproves(t *testing.T) {
	rec := postEast(newTestEast(), "k-1", eastSample)
	want := `{"status":"approved","approved_kopecks":30000000,"rate_bps":1490,"term":24,"monthly_payment_kopecks":1453175}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestEastRejectsPensioner(t *testing.T) {
	body := strings.Replace(eastSample, `"employment_kind":"employed"`, `"employment_kind":"pensioner"`, 1)
	rec := postEast(newTestEast(), "k-1", body)
	want := `{"status":"rejected","reject_code":"employment_not_accepted"}`
	if rec.Code != http.StatusOK || rec.Body.String() != want {
		t.Fatalf("got %d %s\nwant 200 %s", rec.Code, rec.Body, want)
	}
}

func TestEastRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, key, body string
	}{
		{"missing idempotency key", "", eastSample},
		{"malformed json", "k-1", `{`},
		{"bad birth date", "k-1", strings.Replace(eastSample, "1990-05-17", "17.05.1990", 1)},
		{"zero term", "k-1", strings.Replace(eastSample, `"term":24`, `"term":0`, 1)},
	}
	for _, tt := range tests {
		if rec := postEast(newTestEast(), tt.key, tt.body); rec.Code != http.StatusBadRequest {
			t.Errorf("%s: code = %d, want 400", tt.name, rec.Code)
		}
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/mockbank/`
Expected: FAIL — `undefined: NewEast`.

- [ ] **Step 4: Реализовать диалект east**

Файл: `internal/mockbank/east.go`
```go
package mockbank

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
)

// Диалект east: синхронный, копейки и базисные пункты, snake_case (раздел 6.3 спеки).

type eastRequest struct {
	ApplicantName    string `json:"applicant_name"`
	DateOfBirth      string `json:"date_of_birth"`
	PhoneNumber      string `json:"phone_number"`
	IncomeKopecks    int64  `json:"income_kopecks"`
	EmploymentKind   string `json:"employment_kind"`
	RequestedKopecks int64  `json:"requested_kopecks"`
	Term             int    `json:"term"`
}

type eastResponse struct {
	Status                string `json:"status"`
	ApprovedKopecks       int64  `json:"approved_kopecks,omitempty"`
	RateBPS               int    `json:"rate_bps,omitempty"`
	Term                  int    `json:"term,omitempty"`
	MonthlyPaymentKopecks int64  `json:"monthly_payment_kopecks,omitempty"`
	RejectCode            string `json:"reject_code,omitempty"`
}

// NewEast — заглушка eastbank: POST /v2/applications, ключ идемпотентности — Idempotency-Key.
func NewEast(opts Options) http.Handler {
	s := newServer(opts)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v2/applications", func(w http.ResponseWriter, r *http.Request) {
		s.handle(w, r, r.Header.Get("Idempotency-Key"), func(body []byte) (int, any) {
			var req eastRequest
			if err := json.Unmarshal(body, &req); err != nil {
				return http.StatusBadRequest, errorResponse{Error: "invalid json"}
			}
			lr, err := req.toLoanRequest()
			if err != nil {
				return http.StatusBadRequest, errorResponse{Error: err.Error()}
			}
			return http.StatusOK, eastResponseFrom(EastProfile.Decide(lr, s.opts.Now()))
		})
	})
	return mux
}

func (r eastRequest) toLoanRequest() (LoanRequest, error) {
	birth, err := time.Parse(time.DateOnly, r.DateOfBirth)
	if err != nil {
		return LoanRequest{}, errors.New("date_of_birth must be YYYY-MM-DD")
	}
	if r.IncomeKopecks <= 0 || r.RequestedKopecks <= 0 || r.Term <= 0 {
		return LoanRequest{}, errors.New("income_kopecks, requested_kopecks and term must be positive")
	}
	return LoanRequest{
		BirthDate:     birth,
		MonthlyIncome: r.IncomeKopecks,
		Employment:    application.EmploymentType(r.EmploymentKind),
		Amount:        r.RequestedKopecks,
		TermMonths:    r.Term,
	}, nil
}

func eastResponseFrom(d application.Decision) eastResponse {
	if !d.Approved {
		return eastResponse{Status: "rejected", RejectCode: d.RejectReason}
	}
	return eastResponse{
		Status:                "approved",
		ApprovedKopecks:       d.Amount,
		RateBPS:               d.RateBPS,
		Term:                  d.TermMonths,
		MonthlyPaymentKopecks: d.MonthlyPayment,
	}
}
```

- [ ] **Step 5: Запустить — тесты проходят, commit**

Run: `go test ./internal/mockbank/`
Expected: PASS

```bash
git add internal/mockbank/east.go internal/mockbank/east_test.go
git commit -m "feat(mockbank): add east dialect"
```

- [ ] **Step 6: Написать тесты адаптера eastbank**

Файл: `internal/bank/eastbank/eastbank_test.go`
```go
package eastbank

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func testApp() application.Application {
	return application.Application{
		ID:             uuid.New(),
		FullName:       "Иванов Иван",
		BirthDate:      time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: application.EmploymentSelfEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1490, TermMonths: 24, MonthlyPayment: 1_453_175}

func submit(t *testing.T, url string, app application.Application) (bank.SubmitResult, error) {
	t.Helper()
	return New(url, http.DefaultClient).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: app})
}

func TestSubmitSendsEastbankRequest(t *testing.T) {
	reqID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v2/applications" || r.Header.Get("Idempotency-Key") != reqID.String() {
			t.Errorf("unexpected request %s, Idempotency-Key %q", r.URL.Path, r.Header.Get("Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"applicant_name":"Иванов Иван","date_of_birth":"1990-05-17","phone_number":"+79991234567","income_kopecks":12000000,"employment_kind":"self_employed","requested_kopecks":30000000,"term":24}`
		if string(body) != want {
			t.Errorf("body:\n%s\nwant:\n%s", body, want)
		}
		_, _ = w.Write([]byte(`{"status":"approved","approved_kopecks":30000000,"rate_bps":1490,"term":24,"monthly_payment_kopecks":1453175}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: reqID, Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved {
		t.Fatalf("result = %+v, want %+v", res, approved)
	}
}

func TestSubmitRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"rejected","reject_code":"debt_burden"}`))
	}))
	defer srv.Close()
	res, err := submit(t, srv.URL, testApp())
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || res.Decision.Approved || res.Decision.RejectReason != "debt_burden" {
		t.Fatalf("result = %+v, want rejection", res)
	}
}

func TestSubmitErrorClasses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"server error", http.StatusServiceUnavailable, `{}`, false},
		{"rate limited", http.StatusTooManyRequests, `{}`, false},
		{"bad request", http.StatusBadRequest, `{}`, true},
		{"malformed json", http.StatusOK, `not json`, true},
		{"unknown status", http.StatusOK, `{"status":"maybe"}`, true},
		{"incomplete approval", http.StatusOK, `{"status":"approved"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := submit(t, srv.URL, testApp())
			if err == nil {
				t.Fatal("want error")
			}
			if bank.IsPermanent(err) != tt.permanent {
				t.Fatalf("err = %v, permanent = %v, want %v", err, bank.IsPermanent(err), tt.permanent)
			}
		})
	}
}

func TestSubmitAgainstMockbank(t *testing.T) {
	srv := httptest.NewServer(mockbank.NewEast(mockbank.Options{}))
	defer srv.Close()
	app := testApp()
	app.EmploymentType = application.EmploymentEmployed
	res, err := submit(t, srv.URL, app)
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision == nil || *res.Decision != approved {
		t.Fatalf("result = %+v, want %+v", res, approved)
	}
}
```

- [ ] **Step 7: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/eastbank/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 8: Реализовать адаптер eastbank**

Файл: `internal/bank/eastbank/eastbank.go`
```go
// Package eastbank — адаптер eastbank: синхронный JSON, копейки и базисные пункты,
// snake_case (раздел 6.3 спеки).
package eastbank

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Code — код банка в шлюзе.
const Code = "eastbank"

type request struct {
	ApplicantName    string `json:"applicant_name"`
	DateOfBirth      string `json:"date_of_birth"`
	PhoneNumber      string `json:"phone_number"`
	IncomeKopecks    int64  `json:"income_kopecks"`
	EmploymentKind   string `json:"employment_kind"`
	RequestedKopecks int64  `json:"requested_kopecks"`
	Term             int    `json:"term"`
}

type response struct {
	Status                string `json:"status"`
	ApprovedKopecks       int64  `json:"approved_kopecks"`
	RateBPS               int    `json:"rate_bps"`
	Term                  int    `json:"term"`
	MonthlyPaymentKopecks int64  `json:"monthly_payment_kopecks"`
	RejectCode            string `json:"reject_code"`
}

// Client — адаптер eastbank.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт адаптер; baseURL — адрес банка без завершающего слеша.
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, http: httpClient}
}

// Code возвращает код банка.
func (c *Client) Code() string { return Code }

// Submit отправляет заявку и возвращает синхронное решение.
func (c *Client) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	a := req.Application
	body := request{
		ApplicantName:    a.FullName,
		DateOfBirth:      a.BirthDate.Format(time.DateOnly),
		PhoneNumber:      a.Phone,
		IncomeKopecks:    a.MonthlyIncome,
		EmploymentKind:   string(a.EmploymentType),
		RequestedKopecks: a.Amount,
		Term:             a.TermMonths,
	}
	headers := map[string]string{"Idempotency-Key": req.RequestID.String()}
	status, data, err := bank.PostJSON(ctx, c.http, c.baseURL+"/v2/applications", headers, body)
	if err != nil {
		return bank.SubmitResult{}, err
	}
	if status != http.StatusOK {
		return bank.SubmitResult{}, bank.StatusError(status)
	}
	var resp response
	if err := json.Unmarshal(data, &resp); err != nil {
		return bank.SubmitResult{}, fmt.Errorf("%w: decode response: %w", bank.ErrPermanent, err)
	}
	var d application.Decision
	switch resp.Status {
	case "rejected":
		d = application.Decision{RejectReason: resp.RejectCode}
	case "approved":
		if resp.ApprovedKopecks <= 0 || resp.RateBPS <= 0 || resp.Term <= 0 || resp.MonthlyPaymentKopecks <= 0 {
			return bank.SubmitResult{}, fmt.Errorf("%w: incomplete approval", bank.ErrPermanent)
		}
		d = application.Decision{
			Approved:       true,
			Amount:         resp.ApprovedKopecks,
			RateBPS:        resp.RateBPS,
			TermMonths:     resp.Term,
			MonthlyPayment: resp.MonthlyPaymentKopecks,
		}
	default:
		return bank.SubmitResult{}, fmt.Errorf("%w: unknown status %q", bank.ErrPermanent, resp.Status)
	}
	return bank.SubmitResult{Decision: &d}, nil
}
```

- [ ] **Step 9: Запустить — тесты проходят, commit**

Run: `go test ./internal/bank/...`
Expected: PASS

```bash
git add internal/bank/eastbank
git commit -m "feat(bank): add eastbank adapter"
```

- [ ] **Step 10: Подключить банк в бинарниках**

В `cmd/gateway/banks.go` добавить импорт `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/eastbank"` и строку в реестр (коды по алфавиту):
```go
var adapters = map[string]func(baseURL string, c *http.Client) bank.Bank{
	eastbank.Code:  func(u string, c *http.Client) bank.Bank { return eastbank.New(u, c) },
	northbank.Code: func(u string, c *http.Client) bank.Bank { return northbank.New(u, c) },
}
```

В `cmd/mockbank/main.go` в `switch` функции `newHandler` добавить ветку:
```go
	case "east":
		return mockbank.NewEast(opts), nil
```

В `cmd/mockbank/main_test.go` в конец `TestNewHandlerSelectsDialect` добавить:
```go
	if h, err := newHandler(t.Context(), config.Mockbank{Dialect: "east"}, log); err != nil || h == nil {
		t.Fatalf("east: handler %v, err %v", h, err)
	}
```

- [ ] **Step 11: Добавить банк в compose**

В `compose.yaml` в якорь `x-gateway-env` добавить строку:
```yaml
  BANK_EASTBANK_URL: http://eastbank:8090
```
и в `services` — сервис (медленный и нестабильный, чтобы в демо были видны ретраи):
```yaml
  eastbank:
    <<: *app
    command: ["mockbank"]
    environment:
      MOCKBANK_DIALECT: east
      MOCKBANK_LATENCY: 1s
      MOCKBANK_FAIL_FIRST: "2"
```

- [ ] **Step 12: Написать e2e-сценарии ретраев и таймаута**

Файл: `test/e2e/eastbank_test.go`
```go
package e2e

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/eastbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func eastbankMock(o mockbank.Options, timeout time.Duration) mockBank {
	return mockBank{
		code:    eastbank.Code,
		mock:    func(string) http.Handler { return mockbank.NewEast(o) },
		adapter: func(url string, c *http.Client) bank.Bank { return eastbank.New(url, c) },
		timeout: timeout,
	}
}

func TestEastbankRecoversAfterRetries(t *testing.T) {
	e := start(t, options{banks: []mockBank{eastbankMock(mockbank.Options{FailFirst: 2}, 0)}})
	got := e.submit(nil).bank(t, eastbank.Code)
	want := offerView{Amount: 30_000_000, RateBPS: 1490, TermMonths: 24, MonthlyPayment: 1_453_175}
	if got.Status != "approved" || got.Attempts != 3 || got.Offer == nil || *got.Offer != want {
		t.Fatalf("eastbank = %+v, want approved on attempt 3", got)
	}
}

func TestEastbankFailsWhenAttemptsExhausted(t *testing.T) {
	e := start(t, options{banks: []mockBank{eastbankMock(mockbank.Options{FailFirst: 10}, 0)}, maxAttempts: 3})
	got := e.submit(nil).bank(t, eastbank.Code)
	if got.Status != "failed" || got.Attempts != 3 || !strings.Contains(got.Error, "503") {
		t.Fatalf("eastbank = %+v, want failed after 3 attempts with 503", got)
	}
}

func TestEastbankTimeoutIsRetried(t *testing.T) {
	slow := eastbankMock(mockbank.Options{Latency: 300 * time.Millisecond}, 50*time.Millisecond)
	e := start(t, options{banks: []mockBank{slow}, maxAttempts: 2})
	got := e.submit(nil).bank(t, eastbank.Code)
	if got.Status != "failed" || got.Attempts != 2 || !strings.Contains(got.Error, "deadline exceeded") {
		t.Fatalf("eastbank = %+v, want failed after 2 timed out attempts", got)
	}
}

func TestEastbankRejectsPensioner(t *testing.T) {
	e := start(t, options{banks: []mockBank{eastbankMock(mockbank.Options{}, 0)}})
	got := e.submit(func(in map[string]any) { in["employment_type"] = "pensioner" }).bank(t, eastbank.Code)
	if got.Status != "rejected" || got.RejectReason != "employment_not_accepted" {
		t.Fatalf("eastbank = %+v, want rejected with employment_not_accepted", got)
	}
}
```

- [ ] **Step 13: Проверить всё и систему целиком**

Run: `gofmt -w cmd && make lint && make test && make up && make demo; make down`
Expected: всё зелёное; в выводе `make demo` у `eastbank` статус `approved` и `"attempts": 3`.

- [ ] **Step 14: Commit**

```bash
git add cmd compose.yaml test/e2e/eastbank_test.go
git commit -m "feat: connect eastbank with retries and timeout scenarios"
```

---

### Task 17: Банк westbank — асинхронный, вебхуком (облачная сессия)

**Ветка:** `feature/westbank` · **Этап 2, облако, параллельно с Task 16** · **Спека:** разделы 5.3, 6.3 (westbank), 6.4, 11, 12

Задача независима от Task 16. Обе правят `cmd/gateway/banks.go`, `cmd/mockbank/main.go`, `cmd/mockbank/main_test.go` и `compose.yaml` — точечно, в разных строках; конфликт при merge второго PR решается объединением обеих правок.

**Промпт для облачной сессии:** «Выполни Task 17 из docs/plan.md в ветке feature/westbank от develop. Следуй CLAUDE.md. В конце — `make lint && make test`, строка в docs/dev-log.md и PR в develop.»

**Files:**
- Create: `internal/mockbank/west.go`, `internal/bank/westbank/westbank.go`
- Modify: `cmd/gateway/banks.go` (реестр `adapters`), `cmd/mockbank/main.go` (`newHandler`), `cmd/mockbank/main_test.go`, `compose.yaml`
- Test: `internal/mockbank/west_test.go`, `internal/bank/westbank/westbank_test.go`, `test/e2e/westbank_test.go`

**Interfaces:**
- Consumes: `mockbank.newServer`, `(*server).handle`, `errorResponse`, `WestProfile`, `LoanRequest`, `testNow` (Task 10); `bank.PostJSON`, `bank.StatusError`, `bank.ErrPermanent`, `bank.IsPermanent`, `bank.SignWebhook`, `bank.VerifyWebhook`, `bank.HeaderWebhook*`, `bank.ParseRubles`, `bank.ParsePercent`, `bank.FormatRubles`, `bank.FormatPercent` (Tasks 3–4); обработчик вебхуков шлюза (Task 9); e2e-окружение (Task 14)
- Produces: `type mockbank.CallbackOptions struct{ URL string; Secret []byte; Delay time.Duration; Skip bool; Retries int; RetryInterval time.Duration; Client *http.Client; Log *slog.Logger }`; `func mockbank.NewWest(ctx context.Context, opts Options, cb CallbackOptions) http.Handler`; `const westbank.Code = "westbank"`; `func westbank.New(baseURL string, httpClient *http.Client) *westbank.Client` — реализует `bank.Bank` и `bank.CallbackHandler`; в e2e — `func westbankMock(t *testing.T, skip bool) mockBank`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/westbank
```

- [ ] **Step 2: Написать тесты диалекта west**

Файл: `internal/mockbank/west_test.go`
```go
package mockbank

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

const westSample = `{"fio":"Иванов Иван","dob":"1990-05-17","tel":"+79991234567","salary":"120000.00","job":"employed","amount":"300000.00","period_months":24}`

var westSecret = []byte("west-secret")

// receiver — приёмник вебхуков: проверяет подпись и отвечает кодами из statuses по очереди,
// после их исчерпания — 200.
type receiver struct {
	mu       sync.Mutex
	statuses []int
	bodies   []string
}

func (rc *receiver) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	err := bank.VerifyWebhook(westSecret, r.Header.Get(bank.HeaderWebhookTimestamp),
		r.Header.Get(bank.HeaderWebhookSignature), body, testNow, time.Minute)
	rc.mu.Lock()
	defer rc.mu.Unlock()
	if err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	rc.bodies = append(rc.bodies, string(body))
	status := http.StatusOK
	if len(rc.statuses) > 0 {
		status, rc.statuses = rc.statuses[0], rc.statuses[1:]
	}
	w.WriteHeader(status)
}

func (rc *receiver) received() []string {
	rc.mu.Lock()
	defer rc.mu.Unlock()
	return slices.Clone(rc.bodies)
}

func newTestWest(t *testing.T, callbackURL string, skip bool) http.Handler {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewWest(ctx, Options{Now: func() time.Time { return testNow }}, CallbackOptions{
		URL:           callbackURL,
		Secret:        westSecret,
		Delay:         10 * time.Millisecond,
		Skip:          skip,
		Retries:       3,
		RetryInterval: 10 * time.Millisecond,
	})
}

func applyWest(t *testing.T, h http.Handler, key, body string) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/loans/apply", strings.NewReader(body))
	req.Header.Set("X-Idempotency-Key", key)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("apply: code %d, body %s", rec.Code, rec.Body)
	}
	var acc westAccepted
	if err := json.Unmarshal(rec.Body.Bytes(), &acc); err != nil || !strings.HasPrefix(acc.ApplicationID, "w-") {
		t.Fatalf("apply: body %s, err %v", rec.Body, err)
	}
	return acc.ApplicationID
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestWestAcceptsAndSendsSignedCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	id := applyWest(t, newTestWest(t, srv.URL, false), "k-1", westSample)
	waitFor(t, "callback", func() bool { return len(rc.received()) == 1 })
	want := `{"application_id":"` + id + `","result":"approved","offer":{"amount":"300000.00","rate":"18.90","period_months":24,"monthly":"15108.01"}}`
	if got := rc.received()[0]; got != want {
		t.Fatalf("callback:\n%s\nwant:\n%s", got, want)
	}
}

func TestWestDeclineCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	body := strings.Replace(westSample, `"salary":"120000.00"`, `"salary":"20000.00"`, 1)
	id := applyWest(t, newTestWest(t, srv.URL, false), "k-1", body)
	waitFor(t, "callback", func() bool { return len(rc.received()) == 1 })
	want := `{"application_id":"` + id + `","result":"declined","reason":"insufficient_income"}`
	if got := rc.received()[0]; got != want {
		t.Fatalf("callback:\n%s\nwant:\n%s", got, want)
	}
}

func TestWestRetriesCallbackUntilAccepted(t *testing.T) {
	rc := &receiver{statuses: []int{http.StatusNotFound, http.StatusNotFound}}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	applyWest(t, newTestWest(t, srv.URL, false), "k-1", westSample)
	waitFor(t, "3 deliveries", func() bool { return len(rc.received()) == 3 })
	time.Sleep(50 * time.Millisecond)
	if n := len(rc.received()); n != 3 {
		t.Fatalf("deliveries = %d, want 3 — retries must stop after success", n)
	}
}

func TestWestRepeatSchedulesOneCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	h := newTestWest(t, srv.URL, false)
	first := applyWest(t, h, "k-1", westSample)
	second := applyWest(t, h, "k-1", westSample)
	if first != second {
		t.Fatalf("repeat returned %s, want %s", second, first)
	}
	waitFor(t, "callback", func() bool { return len(rc.received()) == 1 })
	time.Sleep(50 * time.Millisecond)
	if n := len(rc.received()); n != 1 {
		t.Fatalf("deliveries = %d, want 1", n)
	}
}

func TestWestSkipsCallback(t *testing.T) {
	rc := &receiver{}
	srv := httptest.NewServer(rc)
	defer srv.Close()

	applyWest(t, newTestWest(t, srv.URL, true), "k-1", westSample)
	time.Sleep(100 * time.Millisecond)
	if n := len(rc.received()); n != 0 {
		t.Fatalf("deliveries = %d, want 0", n)
	}
}
```

- [ ] **Step 3: Запустить — тесты должны упасть**

Run: `go test ./internal/mockbank/`
Expected: FAIL — `undefined: NewWest`, `undefined: CallbackOptions`, `undefined: westAccepted`.

- [ ] **Step 4: Реализовать диалект west**

Файл: `internal/mockbank/west.go`
```go
package mockbank

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Диалект west: асинхронный — 202 с id заявки, решение вебхуком с HMAC-подписью (раздел 6.3 спеки).

// CallbackOptions — доставка вебхуков диалекта west.
type CallbackOptions struct {
	URL           string        // куда слать вебхук
	Secret        []byte        // секрет HMAC
	Delay         time.Duration // задержка перед первой отправкой
	Skip          bool          // не слать вебхук вовсе
	Retries       int           // повторов при не-2xx или сетевой ошибке (по спеке 3)
	RetryInterval time.Duration // интервал между повторами (по спеке 2 с)
	Client        *http.Client  // nil — http.DefaultClient
	Log           *slog.Logger  // nil — без логов
}

type westRequest struct {
	FIO          string `json:"fio"`
	DOB          string `json:"dob"`
	Tel          string `json:"tel"`
	Salary       string `json:"salary"`
	Job          string `json:"job"`
	Amount       string `json:"amount"`
	PeriodMonths int    `json:"period_months"`
}

type westAccepted struct {
	ApplicationID string `json:"application_id"`
}

type westOffer struct {
	Amount       string `json:"amount"`
	Rate         string `json:"rate"`
	PeriodMonths int    `json:"period_months"`
	Monthly      string `json:"monthly"`
}

type westCallback struct {
	ApplicationID string     `json:"application_id"`
	Result        string     `json:"result"` // approved | declined
	Offer         *westOffer `json:"offer,omitempty"`
	Reason        string     `json:"reason,omitempty"`
}

type west struct {
	ctx context.Context
	srv *server
	cb  CallbackOptions
}

// NewWest — заглушка westbank: POST /loans/apply с ключом X-Idempotency-Key отвечает 202
// с application_id, решение уходит вебхуком на cb.URL. Отправка прекращается при отмене ctx.
func NewWest(ctx context.Context, opts Options, cb CallbackOptions) http.Handler {
	if cb.Client == nil {
		cb.Client = http.DefaultClient
	}
	if cb.Log == nil {
		cb.Log = slog.New(slog.DiscardHandler)
	}
	w := &west{ctx: ctx, srv: newServer(opts), cb: cb}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /loans/apply", w.apply)
	return mux
}

func (w *west) apply(rw http.ResponseWriter, r *http.Request) {
	w.srv.handle(rw, r, r.Header.Get("X-Idempotency-Key"), func(body []byte) (int, any) {
		var req westRequest
		if err := json.Unmarshal(body, &req); err != nil {
			return http.StatusBadRequest, errorResponse{Error: "invalid json"}
		}
		lr, err := req.toLoanRequest()
		if err != nil {
			return http.StatusBadRequest, errorResponse{Error: err.Error()}
		}
		id := "w-" + uuid.NewString()
		callback := westCallbackFrom(id, WestProfile.Decide(lr, w.srv.opts.Now()))
		if !w.cb.Skip {
			go w.deliver(callback)
		}
		return http.StatusAccepted, westAccepted{ApplicationID: id}
	})
}

// deliver отправляет вебхук после задержки и повторяет его при неудаче.
func (w *west) deliver(c westCallback) {
	body, _ := json.Marshal(c) // westCallback состоит из строк и чисел
	if !w.sleep(w.cb.Delay) {
		return
	}
	for attempt := 0; attempt <= w.cb.Retries; attempt++ {
		if attempt > 0 && !w.sleep(w.cb.RetryInterval) {
			return
		}
		err := w.send(body)
		if err == nil {
			return
		}
		w.cb.Log.Warn("callback delivery failed", "application_id", c.ApplicationID, "attempt", attempt+1, "error", err)
	}
}

// sleep ждёт d; false — ctx отменён.
func (w *west) sleep(d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return true
	case <-w.ctx.Done():
		return false
	}
}

func (w *west) send(body []byte) error {
	ts := strconv.FormatInt(w.srv.opts.Now().Unix(), 10)
	req, err := http.NewRequestWithContext(w.ctx, http.MethodPost, w.cb.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(bank.HeaderWebhookTimestamp, ts)
	req.Header.Set(bank.HeaderWebhookSignature, bank.SignWebhook(w.cb.Secret, ts, body))
	resp, err := w.cb.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}

func (r westRequest) toLoanRequest() (LoanRequest, error) {
	birth, err := time.Parse(time.DateOnly, r.DOB)
	if err != nil {
		return LoanRequest{}, errors.New("dob must be YYYY-MM-DD")
	}
	salary, err := bank.ParseRubles(r.Salary)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("salary: %w", err)
	}
	amount, err := bank.ParseRubles(r.Amount)
	if err != nil {
		return LoanRequest{}, fmt.Errorf("amount: %w", err)
	}
	if r.PeriodMonths <= 0 {
		return LoanRequest{}, errors.New("period_months must be positive")
	}
	return LoanRequest{
		BirthDate:     birth,
		MonthlyIncome: salary,
		Employment:    application.EmploymentType(r.Job),
		Amount:        amount,
		TermMonths:    r.PeriodMonths,
	}, nil
}

func westCallbackFrom(id string, d application.Decision) westCallback {
	if !d.Approved {
		return westCallback{ApplicationID: id, Result: "declined", Reason: d.RejectReason}
	}
	return westCallback{ApplicationID: id, Result: "approved", Offer: &westOffer{
		Amount:       bank.FormatRubles(d.Amount),
		Rate:         bank.FormatPercent(d.RateBPS),
		PeriodMonths: d.TermMonths,
		Monthly:      bank.FormatRubles(d.MonthlyPayment),
	}}
}
```

- [ ] **Step 5: Запустить — тесты проходят, commit**

Run: `go test -race ./internal/mockbank/`
Expected: PASS

```bash
git add internal/mockbank/west.go internal/mockbank/west_test.go
git commit -m "feat(mockbank): add async west dialect with signed callbacks"
```

- [ ] **Step 6: Написать тесты адаптера westbank**

Файл: `internal/bank/westbank/westbank_test.go`
```go
package westbank

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func testApp() application.Application {
	return application.Application{
		ID:             uuid.New(),
		FullName:       "Иванов Иван",
		BirthDate:      time.Date(1990, 5, 17, 0, 0, 0, 0, time.UTC),
		Phone:          "+79991234567",
		MonthlyIncome:  12_000_000,
		EmploymentType: application.EmploymentEmployed,
		Amount:         30_000_000,
		TermMonths:     24,
	}
}

var approved = application.Decision{Approved: true, Amount: 30_000_000, RateBPS: 1890, TermMonths: 24, MonthlyPayment: 1_510_801}

func TestSubmitSendsWestbankRequest(t *testing.T) {
	reqID := uuid.New()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/loans/apply" || r.Header.Get("X-Idempotency-Key") != reqID.String() {
			t.Errorf("unexpected request %s, X-Idempotency-Key %q", r.URL.Path, r.Header.Get("X-Idempotency-Key"))
		}
		body, _ := io.ReadAll(r.Body)
		want := `{"fio":"Иванов Иван","dob":"1990-05-17","tel":"+79991234567","salary":"120000.00","job":"employed","amount":"300000.00","period_months":24}`
		if string(body) != want {
			t.Errorf("body:\n%s\nwant:\n%s", body, want)
		}
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"application_id":"w-42"}`))
	}))
	defer srv.Close()

	res, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: reqID, Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	if res.Decision != nil || res.ExternalID != "w-42" {
		t.Fatalf("result = %+v, want external id w-42 without decision", res)
	}
}

func TestSubmitErrorClasses(t *testing.T) {
	tests := []struct {
		name      string
		status    int
		body      string
		permanent bool
	}{
		{"server error", http.StatusServiceUnavailable, `{}`, false},
		{"unexpected 200", http.StatusOK, `{"application_id":"w-1"}`, true},
		{"malformed json", http.StatusAccepted, `not json`, true},
		{"empty application id", http.StatusAccepted, `{}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			_, err := New(srv.URL, srv.Client()).Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
			if err == nil {
				t.Fatal("want error")
			}
			if bank.IsPermanent(err) != tt.permanent {
				t.Fatalf("err = %v, permanent = %v, want %v", err, bank.IsPermanent(err), tt.permanent)
			}
		})
	}
}

func TestParseCallback(t *testing.T) {
	c := New("http://unused", http.DefaultClient)
	id, d, err := c.ParseCallback([]byte(`{"application_id":"w-1","result":"approved","offer":{"amount":"300000.00","rate":"18.90","period_months":24,"monthly":"15108.01"}}`))
	if err != nil || id != "w-1" || d != approved {
		t.Fatalf("approved: got %s %+v %v", id, d, err)
	}
	id, d, err = c.ParseCallback([]byte(`{"application_id":"w-2","result":"declined","reason":"debt_burden"}`))
	if err != nil || id != "w-2" || d != (application.Decision{RejectReason: "debt_burden"}) {
		t.Fatalf("declined: got %s %+v %v", id, d, err)
	}
	for _, bad := range []string{
		`not json`,
		`{"result":"approved"}`,
		`{"application_id":"w-1","result":"approved"}`,
		`{"application_id":"w-1","result":"maybe"}`,
		`{"application_id":"w-1","result":"approved","offer":{"amount":"abc","rate":"18.90","period_months":24,"monthly":"1.00"}}`,
	} {
		if _, _, err := c.ParseCallback([]byte(bad)); err == nil {
			t.Errorf("ParseCallback(%s) must fail", bad)
		}
	}
}

func TestAgainstMockbank(t *testing.T) {
	var (
		mu       sync.Mutex
		callback []byte
	)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		callback = body
		mu.Unlock()
	}))
	defer receiver.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	mock := httptest.NewServer(mockbank.NewWest(ctx, mockbank.Options{}, mockbank.CallbackOptions{
		URL: receiver.URL, Secret: []byte("s"), Delay: 10 * time.Millisecond,
	}))
	defer mock.Close()

	c := New(mock.URL, mock.Client())
	res, err := c.Submit(t.Context(), bank.SubmitRequest{RequestID: uuid.New(), Application: testApp()})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		got := callback
		mu.Unlock()
		if got != nil {
			id, d, err := c.ParseCallback(got)
			if err != nil || id != res.ExternalID || d != approved {
				t.Fatalf("callback parsed as %s %+v %v; want %s %+v", id, d, err, res.ExternalID, approved)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("no callback from mockbank")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
```

- [ ] **Step 7: Запустить — тесты должны упасть**

Run: `go test ./internal/bank/westbank/`
Expected: FAIL — `undefined: New`.

- [ ] **Step 8: Реализовать адаптер westbank**

Файл: `internal/bank/westbank/westbank.go`
```go
// Package westbank — адаптер westbank: асинхронный — заявка принимается с 202, решение приходит
// вебхуком (разделы 5.3 и 6.3 спеки).
package westbank

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/application"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
)

// Code — код банка в шлюзе.
const Code = "westbank"

type request struct {
	FIO          string `json:"fio"`
	DOB          string `json:"dob"`
	Tel          string `json:"tel"`
	Salary       string `json:"salary"`
	Job          string `json:"job"`
	Amount       string `json:"amount"`
	PeriodMonths int    `json:"period_months"`
}

type accepted struct {
	ApplicationID string `json:"application_id"`
}

type offer struct {
	Amount       string `json:"amount"`
	Rate         string `json:"rate"`
	PeriodMonths int    `json:"period_months"`
	Monthly      string `json:"monthly"`
}

type callback struct {
	ApplicationID string `json:"application_id"`
	Result        string `json:"result"`
	Offer         *offer `json:"offer"`
	Reason        string `json:"reason"`
}

// Client — адаптер westbank; реализует bank.Bank и bank.CallbackHandler.
type Client struct {
	baseURL string
	http    *http.Client
}

// New создаёт адаптер; baseURL — адрес банка без завершающего слеша.
func New(baseURL string, httpClient *http.Client) *Client {
	return &Client{baseURL: baseURL, http: httpClient}
}

// Code возвращает код банка.
func (c *Client) Code() string { return Code }

// Submit отправляет заявку. Решение придёт вебхуком, поэтому возвращается только ExternalID.
func (c *Client) Submit(ctx context.Context, req bank.SubmitRequest) (bank.SubmitResult, error) {
	a := req.Application
	body := request{
		FIO:          a.FullName,
		DOB:          a.BirthDate.Format(time.DateOnly),
		Tel:          a.Phone,
		Salary:       bank.FormatRubles(a.MonthlyIncome),
		Job:          string(a.EmploymentType),
		Amount:       bank.FormatRubles(a.Amount),
		PeriodMonths: a.TermMonths,
	}
	headers := map[string]string{"X-Idempotency-Key": req.RequestID.String()}
	status, data, err := bank.PostJSON(ctx, c.http, c.baseURL+"/loans/apply", headers, body)
	if err != nil {
		return bank.SubmitResult{}, err
	}
	if status != http.StatusAccepted {
		return bank.SubmitResult{}, bank.StatusError(status)
	}
	var resp accepted
	if err := json.Unmarshal(data, &resp); err != nil {
		return bank.SubmitResult{}, fmt.Errorf("%w: decode response: %w", bank.ErrPermanent, err)
	}
	if resp.ApplicationID == "" {
		return bank.SubmitResult{}, fmt.Errorf("%w: empty application_id", bank.ErrPermanent)
	}
	return bank.SubmitResult{ExternalID: resp.ApplicationID}, nil
}

// ParseCallback разбирает вебхук westbank с решением.
func (c *Client) ParseCallback(body []byte) (string, application.Decision, error) {
	var cb callback
	if err := json.Unmarshal(body, &cb); err != nil {
		return "", application.Decision{}, fmt.Errorf("decode callback: %w", err)
	}
	if cb.ApplicationID == "" {
		return "", application.Decision{}, errors.New("callback without application_id")
	}
	switch cb.Result {
	case "declined":
		return cb.ApplicationID, application.Decision{RejectReason: cb.Reason}, nil
	case "approved":
		if cb.Offer == nil {
			return "", application.Decision{}, errors.New("approved callback without offer")
		}
		amount, errAmount := bank.ParseRubles(cb.Offer.Amount)
		rate, errRate := bank.ParsePercent(cb.Offer.Rate)
		monthly, errMonthly := bank.ParseRubles(cb.Offer.Monthly)
		if err := errors.Join(errAmount, errRate, errMonthly); err != nil {
			return "", application.Decision{}, fmt.Errorf("bad offer: %w", err)
		}
		return cb.ApplicationID, application.Decision{
			Approved:       true,
			Amount:         amount,
			RateBPS:        rate,
			TermMonths:     cb.Offer.PeriodMonths,
			MonthlyPayment: monthly,
		}, nil
	default:
		return "", application.Decision{}, fmt.Errorf("unknown callback result %q", cb.Result)
	}
}
```

- [ ] **Step 9: Запустить — тесты проходят, commit**

Run: `go test -race ./internal/bank/...`
Expected: PASS

```bash
git add internal/bank/westbank
git commit -m "feat(bank): add westbank adapter with callback parsing"
```

- [ ] **Step 10: Подключить банк в бинарниках**

В `cmd/gateway/banks.go` добавить импорт `"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/westbank"` и строку в реестр (коды по алфавиту):
```go
var adapters = map[string]func(baseURL string, c *http.Client) bank.Bank{
	northbank.Code: func(u string, c *http.Client) bank.Bank { return northbank.New(u, c) },
	westbank.Code:  func(u string, c *http.Client) bank.Bank { return westbank.New(u, c) },
}
```

В `cmd/mockbank/main.go` функция `newHandler` теперь использует `ctx` и `log` — заменить её целиком:
```go
// newHandler выбирает диалект заглушки. ctx ограничивает фоновую отправку вебхуков диалекта west.
func newHandler(ctx context.Context, cfg config.Mockbank, log *slog.Logger) (http.Handler, error) {
	opts := mockbank.Options{Latency: cfg.Latency, FailFirst: cfg.FailFirst}
	switch cfg.Dialect {
	case "north":
		return mockbank.NewNorth(opts), nil
	case "west":
		return mockbank.NewWest(ctx, opts, mockbank.CallbackOptions{
			URL:           cfg.CallbackURL,
			Secret:        []byte(cfg.CallbackSecret),
			Delay:         cfg.CallbackDelay,
			Skip:          cfg.CallbackSkip,
			Retries:       3,
			RetryInterval: 2 * time.Second,
			Client:        &http.Client{Timeout: 5 * time.Second},
			Log:           log,
		}), nil
	default:
		return nil, fmt.Errorf("dialect %q is not implemented", cfg.Dialect)
	}
}
```
Если Task 16 уже влита, ветку `case "east":` из неё сохранить.

В `cmd/mockbank/main_test.go` в конец `TestNewHandlerSelectsDialect` добавить:
```go
	west := config.Mockbank{Dialect: "west", CallbackURL: "http://api/v1/webhooks/banks/westbank", CallbackSecret: "s"}
	if h, err := newHandler(t.Context(), west, log); err != nil || h == nil {
		t.Fatalf("west: handler %v, err %v", h, err)
	}
```

- [ ] **Step 11: Добавить банк в compose**

В `compose.yaml` в якорь `x-gateway-env` добавить строки:
```yaml
  BANK_WESTBANK_URL: http://westbank:8090
  BANK_WESTBANK_WEBHOOK_SECRET: local-west-secret
```
и в `services` — сервис (секрет совпадает с `BANK_WESTBANK_WEBHOOK_SECRET`):
```yaml
  westbank:
    <<: *app
    command: ["mockbank"]
    environment:
      MOCKBANK_DIALECT: west
      MOCKBANK_CALLBACK_URL: http://api:8080/v1/webhooks/banks/westbank
      MOCKBANK_CALLBACK_SECRET: local-west-secret
      MOCKBANK_CALLBACK_DELAY: 5s
```

- [ ] **Step 12: Написать e2e-сценарии асинхронного банка**

Файл: `test/e2e/westbank_test.go`
```go
package e2e

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/westbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

var westSecret = []byte("e2e-west-secret")

// westbankMock — асинхронная заглушка; skip — не присылать вебхук вовсе.
func westbankMock(t *testing.T, skip bool) mockBank {
	return mockBank{
		code: westbank.Code,
		mock: func(apiURL string) http.Handler {
			ctx, cancel := context.WithCancel(context.Background())
			t.Cleanup(cancel)
			return mockbank.NewWest(ctx, mockbank.Options{}, mockbank.CallbackOptions{
				URL:           apiURL + "/v1/webhooks/banks/westbank",
				Secret:        westSecret,
				Delay:         50 * time.Millisecond,
				Skip:          skip,
				Retries:       3,
				RetryInterval: 50 * time.Millisecond,
			})
		},
		adapter: func(url string, c *http.Client) bank.Bank { return westbank.New(url, c) },
		secret:  westSecret,
	}
}

func TestWestbankApprovesViaWebhook(t *testing.T) {
	e := start(t, options{banks: []mockBank{westbankMock(t, false)}})
	got := e.submit(nil).bank(t, westbank.Code)
	want := offerView{Amount: 30_000_000, RateBPS: 1890, TermMonths: 24, MonthlyPayment: 1_510_801}
	if got.Status != "approved" || got.Attempts != 1 || got.Offer == nil || *got.Offer != want {
		t.Fatalf("westbank = %+v, want approved via webhook", got)
	}
}

func TestWestbankDeclinesViaWebhook(t *testing.T) {
	e := start(t, options{banks: []mockBank{westbankMock(t, false)}})
	got := e.submit(func(in map[string]any) { in["monthly_income"] = 2_000_000 }).bank(t, westbank.Code)
	if got.Status != "rejected" || got.RejectReason != "insufficient_income" {
		t.Fatalf("westbank = %+v, want rejected with insufficient_income", got)
	}
}

func TestWestbankCallbackTimeout(t *testing.T) {
	e := start(t, options{banks: []mockBank{westbankMock(t, true)}, callbackTimeout: 300 * time.Millisecond})
	got := e.submit(nil).bank(t, westbank.Code)
	if got.Status != "failed" || got.Error != "callback timeout" {
		t.Fatalf("westbank = %+v, want failed with callback timeout", got)
	}
}
```

- [ ] **Step 13: Проверить всё и систему целиком**

Run: `gofmt -w cmd && make lint && make test && make up && make demo; make down`
Expected: всё зелёное; в выводе `make demo` у `westbank` статус `approved` (решение приходит примерно через 5 с).

- [ ] **Step 14: Commit**

```bash
git add cmd compose.yaml test/e2e/westbank_test.go
git commit -m "feat: connect async westbank with webhook scenarios"
```

---

### Task 18: Сквозной тест трёх банков, README и релиз v0.1.0

**Ветка:** `feature/release-docs`, затем `release/0.1.0` · **Этап 3, локально, после merge Task 16 и Task 17** · **Спека:** разделы 1 (критерии готовности), 11, 13.1, 13.5

**Files:**
- Create: `test/e2e/all_banks_test.go`
- Modify: `README.md` (целиком), `docs/dev-log.md`

**Interfaces:**
- Consumes: `northbankMock` (Task 14), `eastbankMock` (Task 16), `westbankMock` (Task 17), `start`, `options`, `(*env).submit`
- Produces: релиз `v0.1.0` в `main`

- [ ] **Step 1: Создать ветку**

```bash
git switch develop && git pull --ff-only && git switch -c feature/release-docs
```

- [ ] **Step 2: Написать сквозной тест трёх банков**

Файл: `test/e2e/all_banks_test.go`
```go
package e2e

import (
	"testing"

	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/eastbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/northbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/bank/westbank"
	"github.com/Vlad-Pisarevskiy/loan-gateway/internal/mockbank"
)

func TestAllBanksDecide(t *testing.T) {
	e := start(t, options{banks: []mockBank{
		northbankMock(mockbank.Options{}),
		eastbankMock(mockbank.Options{FailFirst: 1}, 0),
		westbankMock(t, false),
	}})
	v := e.submit(nil)
	if len(v.Banks) != 3 {
		t.Fatalf("banks = %+v, want 3", v.Banks)
	}
	payments := map[string]int64{northbank.Code: 1_467_460, eastbank.Code: 1_453_175, westbank.Code: 1_510_801}
	for code, payment := range payments {
		got := v.bank(t, code)
		if got.Status != "approved" || got.Offer == nil || got.Offer.MonthlyPayment != payment {
			t.Errorf("%s = %+v, want approved with payment %d", code, got, payment)
		}
	}
}
```

- [ ] **Step 3: Запустить**

Run: `go test -race -count=1 ./test/e2e/`
Expected: PASS. Код уже написан в Tasks 14–17; падение — дефект интеграции, разбирать по `superpowers:systematic-debugging`.

- [ ] **Step 4: Переписать README**

В разделе «Как разрабатывался проект» подставить числа из `docs/dev-log.md`: задачи и PR — по числу строк таблицы (`grep -c '^| 20' docs/dev-log.md`), замечания ревью — сумма столбца, стоимость — сумма облачных сессий.

Файл: `README.md`
````markdown
# Loan Gateway

Сервис-шлюз маркетплейса финансовых продуктов: принимает заявку на кредит наличными, рассылает её в банки-партнёры через адаптеры и собирает их решения.

## Как это работает

- `gateway api` принимает заявку (`POST /v1/applications` с заголовком `Idempotency-Key`) и в одной транзакции создаёт по задаче «отправить в банк» на каждый банк.
- `gateway worker` забирает задачи через `FOR UPDATE SKIP LOCKED`, вызывает адаптеры банков, ретраит временные ошибки с backoff и возвращает в очередь задачи упавших воркеров.
- Асинхронный банк присылает решение вебхуком с HMAC-подписью.
- Клиент опрашивает `GET /v1/applications/{id}`, пока заявка не станет `completed`.

В MVP банки эмулируются заглушкой `mockbank`; у каждого свой протокол:

| Банк | Протокол | Поведение в docker compose |
|---|---|---|
| northbank | синхронный JSON, рубли строкой | отвечает сразу |
| eastbank | синхронный JSON, копейки | отвечает за 1 с, первые 2 запроса — 503 |
| westbank | асинхронный: 202, решение вебхуком | вебхук через 5 с |

## Быстрый старт

Нужны Docker и `jq`.

```bash
make up     # PostgreSQL, миграции, API, воркер и три банка
make demo   # отправить заявку и дождаться решений
make down
```

## Разработка

```bash
make test-short  # юнит-тесты
make test        # все тесты с детектором гонок (нужен Docker)
make lint        # gofmt, go vet, golangci-lint
```

Документы: [спецификация](docs/spec.md) · [план](docs/plan.md) · [журнал разработки](docs/dev-log.md).

## Как разрабатывался проект

Проект сделан агентами Claude Code по процессу «спецификация → план → реализация»:

1. Спецификация и план написаны в диалоге с агентом: требования, варианты архитектуры, дизайн по частям, затем пошаговый план с кодом и тестами.
2. Каждая задача плана — отдельная ветка и PR в `develop` (gitflow). Роль `developer` реализует задачу через TDD, роль `reviewer` проверяет её по спеке и чек-листу — см. `.claude/agents/`.
3. Фундамент (Task 1–15) делался локально и последовательно; адаптеры eastbank и westbank — в двух параллельных облачных сессиях.
4. Каждый PR проходит CI: gofmt, go vet, golangci-lint и все тесты с `-race`.

Итоги по [журналу разработки](docs/dev-log.md): задач и PR — <число строк журнала>, замечаний ревью исправлено — <исправлено> из <всего>, облачные сессии — <сумма> $.
````

- [ ] **Step 5: Строка журнала и commit**

Добавить в `docs/dev-log.md` строку о Task 18, затем:

```bash
git add test/e2e/all_banks_test.go README.md docs/dev-log.md
git commit -m "docs: describe the project and how it was built; add all-banks e2e test"
```

Влить `feature/release-docs` в `develop` по разделу «Процесс выполнения задачи».

- [ ] **Step 6: Выпустить релиз по gitflow**

```bash
git switch develop && git pull --ff-only
git switch -c release/0.1.0
git push -u origin release/0.1.0
gh pr create --base main --head release/0.1.0 --title "Release 0.1.0" --body "MVP: шлюз заявок на кредит, три банка, docker compose, демо."
```

После зелёного CI:

```bash
gh pr merge release/0.1.0 --merge
git switch main && git pull --ff-only
git tag -a v0.1.0 -m "Loan Gateway 0.1.0 (MVP)"
git push origin v0.1.0
gh pr create --base develop --head main --title "Merge release 0.1.0 back into develop" --body "Gitflow: main → develop после релиза."
```

- [ ] **Step 7: Финальная проверка релиза**

Run: `git switch main && make lint && make test && make up && make demo; make down`
Expected: всё зелёное; `make demo` показывает три банка в статусе `approved`. Критерии готовности MVP из раздела 1 спеки выполнены.
