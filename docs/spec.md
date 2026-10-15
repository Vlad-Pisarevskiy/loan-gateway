# Loan Gateway — спецификация MVP

Дата: 2026-10-13, обновлена 2026-10-15 · Статус: v0.1.0 реализована, раздел 14 на ревью ·
Релизы: v0.1.0 (MVP), v0.2.0 (наблюдаемость, раздел 14)

## 1. Контекст и цель

Маркетплейс финансовых продуктов работает со множеством банков-партнёров. У каждого
банка свой протокол, свои форматы данных и свои сбои, а заявка клиента должна дойти до
каждого банка, и клиент должен увидеть сводный результат.

Loan Gateway — сервис-шлюз: принимает заявку на кредит наличными, рассылает её в
банки-партнёры через адаптеры, доводит каждую отправку до результата (ретраи,
асинхронные ответы, идемпотентность) и отдаёт клиенту сводку решений.

**Цели продукта**
- Единая точка приёма заявок: клиентское приложение не знает о протоколах банков.
- Заявка не теряется и не дублируется в банке при сбоях сети, банка или самого шлюза.
- Подключение нового банка — новый адаптер и запись в реестре, без изменений API,
  хранилища и воркера.

**Ограничения**
- Go 1.26, PostgreSQL 18, Docker Compose. Без брокеров сообщений.
- В MVP банки-партнёры эмулируются заглушками (northbank, eastbank, westbank) с
  контрактами из раздела 6.3; подключение реальных банков — после MVP.
- Разработка ведётся агентами Claude Code (раздел 13); бюджет на облачные сессии — $100.

**Критерии готовности MVP**
1. `docker compose up` поднимает всю систему; `make demo` отправляет заявку и печатает
   решения трёх банков.
2. `go test -race ./...`, `go vet`, `golangci-lint run` — зелёные локально и в CI.
3. В репозитории есть эта спека, план, `CLAUDE.md`, `.claude/agents/`, история PR,
   журнал разработки и раздел README «Как разрабатывался проект».

## 2. Объём

**Входит:** приём заявки на кредит наличными, асинхронная рассылка в 3 банка через
адаптеры, ретраи с backoff, асинхронные ответы банков через вебхук с HMAC-подписью,
идемпотентность (клиент → шлюз и шлюз → банк), чтение результата, заглушки банков,
тесты трёх уровней, CI, docker compose, демо-скрипт.

**Входит в v0.2.0:** метрики Prometheus, трейсинг OpenTelemetry, дашборд Grafana и
алерты (раздел 14).

**Не входит** (кандидаты в задачи после MVP):
- подключение реальных банков-партнёров;
- уведомление клиента вебхуком (в MVP клиент опрашивает `GET`);
- фильтрация банков по критериям до отправки;
- другие продукты (кредитная карта и т. д.);
- аутентификация клиентов API, шифрование ПДн в БД;
- список и поиск заявок;
- срок жизни ключей идемпотентности (в MVP хранятся бессрочно).

## 3. Архитектура

Модульный монолит. Один бинарник `gateway` с подкомандами:
- `gateway api` — HTTP API;
- `gateway worker` — обработка очереди запросов в банки и чистильщик;
- `gateway migrate` — применение миграций БД.

Отдельный бинарник `mockbank` — заглушка банка; один бинарник изображает любой из трёх
банков в зависимости от `MOCKBANK_DIALECT`.

Поток данных: клиент → `api` (заявка + задачи в одной транзакции) → PostgreSQL ←
`worker` (забирает задачи) → банки → решение пишется в PostgreSQL; асинхронный банк
присылает решение вебхуком в `api`.

```
cmd/gateway/              main, разбор подкоманд
cmd/mockbank/             main заглушки
internal/config/          чтение env (только stdlib)
internal/application/     доменные типы, валидация, хеш запроса, сервис Create/Get
internal/bank/            интерфейсы Bank и CallbackHandler, классы ошибок, реестр, конвертация сумм
internal/bank/northbank/  адаптер northbank
internal/bank/eastbank/   адаптер eastbank
internal/bank/westbank/   адаптер westbank
internal/dispatch/        воркер, backoff, чистильщик
internal/store/postgres/  репозитории, миграции (embed + goose)
internal/httpapi/         роутинг, хендлеры, problem+json, проверка HMAC
internal/mockbank/        логика заглушки: правила решений, диалекты, отправка вебхука
internal/telemetry/       метрики, трейсинг, обёртка slog (v0.2.0, раздел 14)
test/e2e/                 end-to-end тесты
scripts/demo.sh           демо через curl + jq
deploy/observability/     конфиги Prometheus, алертов и Grafana (v0.2.0)
```

**Зависимости:** `github.com/jackc/pgx/v5`, `github.com/pressly/goose/v3`,
`github.com/google/uuid`, `github.com/testcontainers/testcontainers-go` (модуль postgres).
С v0.2.0 — `github.com/prometheus/client_golang` и `go.opentelemetry.io/otel` (API, SDK,
экспортёр OTLP/HTTP), раздел 14.
HTTP — стандартный `net/http` (роутинг по шаблонам Go 1.22+), логи — `log/slog`.
Новые зависимости — только при явной необходимости.

## 4. Домен

### 4.1 Деньги и ставки

Все денежные значения внутри системы и в публичном API — **целые числа в копейках**
(`int64`). Ставка — целое число в **базисных пунктах** (`1590` = 15,90 % годовых).
Конвертация в форматы банков (рубли и проценты строкой с 0–2 знаками после точки,
например `"300000.00"`, `"15.9"`) — только в адаптерах, без `float`. Хелперы
конвертации (`internal/bank/money.go`) используют и адаптеры, и заглушка.

### 4.2 Заявка

| Поле | Тип | Правило валидации |
|---|---|---|
| `full_name` | string | 1–200 символов (руны) после `TrimSpace`, без управляющих символов (категория Unicode Cc) |
| `birth_date` | string `YYYY-MM-DD` | возраст на дату приёма (UTC) — полных 18–75 лет |
| `phone` | string | `^\+7\d{10}$` |
| `monthly_income` | int64, копейки | > 0 |
| `employment_type` | string | одно из `employed`, `self_employed`, `entrepreneur`, `pensioner` |
| `amount` | int64, копейки | 3 000 000 – 500 000 000 (30 000 – 5 000 000 ₽) |
| `term_months` | int | 6–60 |

Неизвестные поля в JSON — ошибка валидации (`DisallowUnknownFields`).

### 4.3 Решение банка

- **approved:** `amount` (копейки), `rate_bps`, `term_months`, `monthly_payment` (копейки);
- **rejected:** `reject_reason` — код причины, как его вернул банк.

### 4.4 Статусы запроса в банк (`bank_requests.status`)

```
queued → sending → approved | rejected                  (синхронный ответ)
                 → waiting_callback → approved | rejected
                                    → failed             (вебхук не пришёл к дедлайну)
                 → queued                                (временная ошибка, попытки остались)
                 → failed                                (попытки исчерпаны или постоянная ошибка)
```

| Статус | Смысл |
|---|---|
| `queued` | ждёт отправки (`next_attempt_at`) |
| `sending` | взят воркером, действует аренда `locked_until` |
| `waiting_callback` | банк принял асинхронно, ждём вебхук до `callback_deadline` |
| `approved`, `rejected`, `failed` | финальные |

### 4.5 Статус заявки

Не хранится, вычисляется при чтении: `completed`, если все её `bank_requests` в
финальных статусах, иначе `processing`.

## 5. Публичный API

Формат — JSON. Ошибки — `application/problem+json` (RFC 9457):

```json
{
  "type": "about:blank",
  "title": "Validation failed",
  "status": 400,
  "detail": "request body has invalid fields",
  "errors": [{"field": "amount", "message": "must be between 3000000 and 500000000"}]
}
```

Поле `errors` присутствует только у ошибок валидации.

### 5.1 `POST /v1/applications`

Заголовки: `Idempotency-Key` (обязателен, 1–128 печатных ASCII-символов),
`Content-Type: application/json`. Тело — поля из 4.2, не более 1 MiB.

```json
{
  "full_name": "Иванов Иван Иванович",
  "birth_date": "1990-05-17",
  "phone": "+79991234567",
  "monthly_income": 12000000,
  "employment_type": "employed",
  "amount": 30000000,
  "term_months": 24
}
```

| Ответ | Когда |
|---|---|
| `202 {"id": "<uuid>", "status": "processing"}` | заявка создана |
| `202` с тем же `id` и текущим статусом | повтор с тем же ключом и тем же телом |
| `400` | нет ключа, битый JSON, ошибки валидации |
| `409` | ключ уже использован с другим телом |
| `413` | тело больше 1 MiB |

**Идемпотентность.** `request_hash` = SHA-256 от канонического JSON: тело декодируется
в структуру и заново кодируется `encoding/json` (порядок полей и пробелы не влияют).
Создание — `INSERT … ON CONFLICT (idempotency_key) DO NOTHING`; если вставки не было —
`SELECT` существующей записи и сравнение хешей. Ключ глобальный (аутентификации
клиентов в MVP нет).

В той же транзакции создаётся по одному `bank_request` (`queued`,
`next_attempt_at = now()`) на каждый включённый банк.

### 5.2 `GET /v1/applications/{id}`

`200`. Пример показывает все варианты результата и не соответствует анкете из 5.1:

```json
{
  "id": "0199d5a2-…",
  "status": "completed",
  "created_at": "2026-10-13T12:00:00Z",
  "amount": 30000000,
  "term_months": 24,
  "banks": [
    {"bank_code": "northbank", "status": "approved", "attempts": 1,
     "offer": {"amount": 30000000, "rate_bps": 1590, "term_months": 24, "monthly_payment": 1467460}},
    {"bank_code": "eastbank", "status": "rejected", "attempts": 3,
     "reject_reason": "insufficient_income"},
    {"bank_code": "westbank", "status": "failed", "attempts": 1,
     "error": "callback timeout"}
  ]
}
```

`offer` — только у `approved`, `reject_reason` — только у `rejected`, `error`
(`last_error`) — только у `failed`. Банки отсортированы по `bank_code`. ПДн в ответе нет.
`400` — `id` не UUID, `404` — заявка не найдена.

### 5.3 `POST /v1/webhooks/banks/{code}`

Принимает асинхронные решения банков. Формат тела — диалект банка (разбирает адаптер).

Заголовки:
- `X-Webhook-Timestamp` — unix-время в секундах;
- `X-Webhook-Signature` — `sha256=<hex>`, где hex = HMAC-SHA256(секрет банка,
  `<timestamp> + "." + <сырое тело>`).

Обработка:
1. Банк с таким `code` не включён или не реализует `CallbackHandler` → `404`.
2. Нет заголовков, подпись не совпала (`hmac.Equal`) или `|now − timestamp| > WEBHOOK_MAX_SKEW`
   → `401`.
3. Адаптер не разобрал тело → `400`.
4. `UPDATE … WHERE bank_code = $1 AND external_id = $2 AND status = 'waiting_callback'`
   с решением → `200`.
5. Ноль обновлённых строк: запрос существует (повтор или опоздание после `failed`) →
   `200`, ничего не меняем, warning в лог; не существует → `404`.

### 5.4 Служебные

- `GET /healthz` — `200`, если процесс жив.
- `GET /readyz` — `200`, если `ping` БД успешен, иначе `503`.
- `GET /metrics` — метрики Prometheus; отдаётся не на `HTTP_ADDR`, а на отдельном
  адресе `METRICS_ADDR` у `api` и у `worker` (v0.2.0, раздел 14).

## 6. Интеграция с банками

### 6.1 Интерфейсы (`internal/bank`)

```go
type SubmitRequest struct {
    RequestID   uuid.UUID               // bank_requests.id — ключ идемпотентности для банка
    Application application.Application
}

type SubmitResult struct {
    Decision   *application.Decision // != nil — синхронное решение
    ExternalID string                // id заявки в банке; обязателен, если Decision == nil
}

type Bank interface {
    Code() string
    Submit(ctx context.Context, req SubmitRequest) (SubmitResult, error)
}

// Реализуют только асинхронные банки.
type CallbackHandler interface {
    ParseCallback(body []byte) (externalID string, d application.Decision, err error)
}
```

Реестр строится из конфигурации: банк включён, если задан `BANK_<CODE>_URL`. Если
адаптер реализует `CallbackHandler`, а `BANK_<CODE>_WEBHOOK_SECRET` не задан — ошибка
старта. Если ни один банк не включён — ошибка старта (`api` и `worker`).

### 6.2 Классы ошибок

```go
var (
    ErrTemporary = errors.New("temporary bank error") // ретраим
    ErrPermanent = errors.New("permanent bank error") // сразу failed
)
```

Адаптеры оборачивают ошибки: `fmt.Errorf("%w: …", bank.ErrPermanent)`.
- **Временные:** таймаут, сетевая ошибка, `5xx`, `429`.
- **Постоянные:** прочие `4xx`, ответ, который не удалось разобрать.
- Ошибка без класса считается временной.
- Отказ банка — не ошибка, а `Decision` с `rejected`.

### 6.3 Диалекты банков

Все заглушки хранят состояние в памяти. Повтор с тем же ключом идемпотентности
возвращает сохранённый ответ (для westbank — тот же `application_id`, второй вебхук
не планируется). Сохраняется только успешный ответ; искусственные `503` от
`MOCKBANK_FAIL_FIRST` не сохраняются. Возраст считается на дату запроса (UTC).

**northbank — синхронный, рубли и проценты строками, camelCase**
- `POST /api/v1/loan-requests`, ключ идемпотентности — заголовок `X-Request-Id`.
- Запрос:
  ```json
  {"client": {"name": "…", "birthDate": "1990-05-17", "phone": "+79991234567"},
   "income": "120000.00", "employment": "EMPLOYED",
   "loan": {"sum": "300000.00", "months": 24}}
  ```
  `employment` — значение `employment_type` в верхнем регистре.
- Ответ `200`:
  `{"decision": "APPROVED", "offer": {"sum": "300000.00", "ratePercent": "15.90", "months": 24, "payment": "14674.60"}}`
  или `{"decision": "DECLINED", "reason": "insufficient_income"}`.

**eastbank — синхронный, копейки и базисные пункты, snake_case; в compose медленный и нестабильный**
- `POST /v2/applications`, ключ идемпотентности — заголовок `Idempotency-Key`.
- Запрос:
  ```json
  {"applicant_name": "…", "date_of_birth": "1990-05-17", "phone_number": "+79991234567",
   "income_kopecks": 12000000, "employment_kind": "employed",
   "requested_kopecks": 30000000, "term": 24}
  ```
- Ответ `200`:
  `{"status": "approved", "approved_kopecks": 30000000, "rate_bps": 1490, "term": 24, "monthly_payment_kopecks": 1453175}`
  или `{"status": "rejected", "reject_code": "employment_not_accepted"}`.

**westbank — асинхронный, решение вебхуком**
- `POST /loans/apply`, ключ идемпотентности — заголовок `X-Idempotency-Key`.
- Запрос:
  ```json
  {"fio": "…", "dob": "1990-05-17", "tel": "+79991234567", "salary": "120000.00",
   "job": "employed", "amount": "300000.00", "period_months": 24}
  ```
- Ответ `202`: `{"application_id": "<id банка>"}`.
- Через `MOCKBANK_CALLBACK_DELAY` заглушка отправляет `POST` на `MOCKBANK_CALLBACK_URL`
  с подписью по 5.3:
  `{"application_id": "…", "result": "approved", "offer": {"amount": "300000.00", "rate": "18.90", "period_months": 24, "monthly": "15108.01"}}`
  или `{"application_id": "…", "result": "declined", "reason": "debt_burden"}`.
- При не-`2xx` ответе заглушка повторяет вебхук до 3 раз с интервалом 2 с. Это
  закрывает гонку, когда вебхук приходит раньше, чем воркер сохранил `external_id`.

Примеры одобрений посчитаны по 6.4 для анкеты из 5.1 и годятся как эталон в тестах.

### 6.4 Правила решений заглушек

Решение детерминировано анкетой. Проверки идут по порядку, первая сработавшая даёт
отказ с указанным кодом:

| Проверка (код отказа) | northbank | eastbank | westbank |
|---|---|---|---|
| возраст, полных лет (`age_out_of_range`) | 21–70 | 18–65 | 18–75 |
| не принимаемая занятость (`employment_not_accepted`) | — | `pensioner` | — |
| минимальный доход, ₽ (`insufficient_income`) | 30 000 | 50 000 | 25 000 |
| максимальная сумма, ₽ (`amount_exceeds_limit`) | 3 000 000 | 5 000 000 | 2 000 000 |
| платёж > 50 % дохода (`debt_burden`) | да | да | да |
| ставка при одобрении | 15,90 % | 14,90 % | 18,90 % |

Одобряется запрошенная сумма на запрошенный срок. Платёж — аннуитет:
`P = S · i / (1 − (1 + i)^(−n))`, `i = ставка / 12`, округление до копейки
(половина — вверх). Расчёт в заглушке допускает `float64`.

### 6.5 Параметры `mockbank` (env)

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `MOCKBANK_ADDR` | `:8090` | адрес HTTP |
| `MOCKBANK_DIALECT` | — (обяз.) | `north` \| `east` \| `west` |
| `MOCKBANK_LATENCY` | `0s` | задержка перед каждым ответом |
| `MOCKBANK_FAIL_FIRST` | `0` | первые N запросов с каждым ключом идемпотентности → `503` |
| `MOCKBANK_CALLBACK_URL` | — | (west) куда слать вебхук |
| `MOCKBANK_CALLBACK_SECRET` | — | (west) секрет HMAC |
| `MOCKBANK_CALLBACK_DELAY` | `5s` | (west) задержка вебхука |
| `MOCKBANK_CALLBACK_SKIP` | `false` | (west) не слать вебхук вовсе |

Логика заглушки живёт в `internal/mockbank` как библиотека (`http.Handler` на диалект),
`cmd/mockbank` — тонкая обёртка. Это позволяет поднимать заглушки в e2e-тестах через
`httptest`.

## 7. Хранилище

```sql
CREATE TABLE applications (
    id              uuid PRIMARY KEY,
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
    id                uuid PRIMARY KEY,
    application_id    uuid        NOT NULL REFERENCES applications (id),
    bank_code         text        NOT NULL,
    status            text        NOT NULL CHECK (status IN
                          ('queued','sending','waiting_callback','approved','rejected','failed')),
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
```

Идентификаторы — UUIDv7, генерируются в Go. Миграции — SQL-файлы goose, встроены через
`embed`; `gateway migrate` применяет их, `api` и `worker` миграции не запускают.
Каждое обновление `bank_requests` выставляет `updated_at = now()`.

Миграция v0.2.0 добавляет колонку `bank_requests.trace_parent text` (nullable) — контекст
трейса заявки в формате W3C `traceparent` (раздел 14.4).

## 8. Воркер

**Цикл.** Раз в `WORKER_POLL_INTERVAL` воркер забирает до `min(WORKER_BATCH_SIZE,
свободные слоты)` задач (слотов — `WORKER_CONCURRENCY`) и обрабатывает каждую в
отдельной горутине.

**Захват:**

```sql
UPDATE bank_requests
SET status = 'sending', attempts = attempts + 1,
    locked_until = now() + $lease, updated_at = now()
WHERE id IN (
    SELECT id FROM bank_requests
    WHERE status = 'queued' AND next_attempt_at <= now()
    ORDER BY next_attempt_at
    LIMIT $n
    FOR UPDATE SKIP LOCKED
)
RETURNING …;
```

**Обработка одной задачи:**
1. Найти адаптер по `bank_code`. Нет адаптера → `failed`, `last_error = "unknown bank"`.
2. Вызвать `Submit` с контекстом с таймаутом `BANK_<CODE>_TIMEOUT`.
3. Записать результат. Каждый `UPDATE` содержит защиту владения
   `WHERE id = $1 AND status = 'sending' AND attempts = $claimedAttempts`; ноль строк —
   задачу уже перехватили, результат отбрасывается с warning в лог.
   - решение → `approved` / `rejected` с полями решения;
   - `ExternalID` → `waiting_callback`, `external_id`, `callback_deadline = now() + CALLBACK_TIMEOUT`;
   - временная ошибка и `attempts < RETRY_MAX_ATTEMPTS` → `queued`,
     `next_attempt_at = now() + backoff(attempts)`, `last_error`;
   - временная ошибка и попытки исчерпаны, или постоянная ошибка → `failed`, `last_error`.
   Во всех случаях `locked_until = NULL`.

**Backoff (full jitter):** `d = min(RETRY_MAX_DELAY, RETRY_BASE_DELAY · 2^(attempts−1))`,
фактическая пауза — случайная в `[0, d]`.

**Чистильщик** (раз в `SWEEP_INTERVAL`, в каждом экземпляре воркера; запросы
идемпотентны):
- `waiting_callback` и `callback_deadline < now()` → `failed`, `last_error = "callback timeout"`;
- `sending` и `locked_until < now()`: если `attempts < RETRY_MAX_ATTEMPTS` → `queued`,
  `next_attempt_at = now()`; иначе → `failed`, `last_error = "lease expired"`.

Повторная отправка после падения воркера безопасна: банк получает тот же ключ
идемпотентности (`bank_requests.id`).

**Остановка по SIGTERM/SIGINT:** перестать забирать задачи, дождаться текущих не дольше
`SHUTDOWN_TIMEOUT`, затем отменить их контексты. Отменённый вызов банка записывается
как временная ошибка по правилам шага 3: запись результата от отмены не зависит, и
задача не ждёт истечения аренды. Задачи, результат которых записать не удалось,
вернёт чистильщик по истечении аренды. `api` останавливается через
`http.Server.Shutdown` с тем же таймаутом.

## 9. Конфигурация (`gateway`, env)

| Переменная | По умолчанию | Смысл |
|---|---|---|
| `DATABASE_URL` | — (обяз.) | строка подключения PostgreSQL |
| `HTTP_ADDR` | `:8080` | адрес API |
| `LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `SHUTDOWN_TIMEOUT` | `20s` | корректная остановка |
| `WORKER_POLL_INTERVAL` | `500ms` | период опроса очереди |
| `WORKER_BATCH_SIZE` | `10` | максимум задач за один захват |
| `WORKER_CONCURRENCY` | `10` | одновременных вызовов банков |
| `WORKER_LEASE` | `30s` | аренда задачи |
| `RETRY_MAX_ATTEMPTS` | `5` | всего попыток |
| `RETRY_BASE_DELAY` | `1s` | база backoff |
| `RETRY_MAX_DELAY` | `1m` | потолок backoff |
| `CALLBACK_TIMEOUT` | `10m` | ожидание вебхука |
| `SWEEP_INTERVAL` | `5s` | период чистильщика |
| `WEBHOOK_MAX_SKEW` | `5m` | допустимое расхождение `X-Webhook-Timestamp` |
| `BANK_<CODE>_URL` | — | базовый URL банка; банк включён, если задан |
| `BANK_<CODE>_TIMEOUT` | `5s` | таймаут вызова банка |
| `BANK_<CODE>_WEBHOOK_SECRET` | — | секрет HMAC (обязателен для асинхронных банков) |
| `METRICS_ADDR` | `:9100` | адрес `GET /metrics` у `api` и `worker` (v0.2.0) |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | — | адрес приёма трейсов OTLP/HTTP; не задан — трейсинг выключен (v0.2.0) |
| `OTEL_SERVICE_NAME` | `gateway-api` / `gateway-worker` | имя сервиса в трейсах (v0.2.0) |
| `OTEL_TRACES_SAMPLER` | `parentbased_always_on` | семплирование трейсов (v0.2.0) |

`<CODE>` — `NORTHBANK`, `EASTBANK`, `WESTBANK`. Ошибка старта, если
`WORKER_LEASE` ≤ максимального `BANK_<CODE>_TIMEOUT`.

Переменные `OTEL_*` читает OpenTelemetry SDK, а не `internal/config`; остальные
стандартные переменные `OTEL_*` тоже действуют.

## 10. Логи и безопасность

- `log/slog`, JSON в stdout. Поля по контексту: `application_id`, `bank_request_id`,
  `bank_code`, `attempt`.
- ФИО, телефон, дату рождения и тела запросов к банкам в логи не пишем.
- С v0.2.0 записи с контекстом содержат `trace_id` и `span_id` (раздел 14.5); в метках
  метрик и атрибутах спанов ПДн нет (14.2, 14.4).
- Вебхуки защищены HMAC и окном времени (5.3); сравнение подписи — `hmac.Equal`.
- Секреты — только из env, в репозитории только значения для локального compose.

## 11. Тестирование

| Уровень | Что проверяет |
|---|---|
| **Юнит** (табличные тесты) | валидация заявки; инвариантность `request_hash` к порядку полей и пробелам; границы backoff; конвертация «300000.00» ↔ копейки и «15.90» ↔ 1590 bps; маппинг ответов и кодов статуса в решения и классы ошибок для каждого адаптера (через `httptest`); проверка HMAC (верная, чужой секрет, изменённое тело, просроченный timestamp); правила решений и аннуитет заглушки |
| **Интеграционные** (PostgreSQL в `testcontainers-go`) | два параллельных захвата не пересекаются; N параллельных `POST` с одним ключом создают одну заявку; защита владения отбрасывает результат после перехвата; правила чистильщика; обработка вебхука (дубль, опоздание, неизвестный `external_id`) |
| **E2E** (`test/e2e`: API и воркер в процессе, заглушки через `httptest`, PostgreSQL в контейнере, короткие таймауты) | все три банка дают решение, заявка `completed`; ретраи (`FAIL_FIRST=2` → `approved`, `attempts = 3`); исчерпание попыток → `failed`; таймаут банка → ретрай; асинхронное решение через вебхук; вебхук не пришёл → `failed` (`callback timeout`); повтор `POST` → тот же `id` и один набор `bank_requests`; тот же ключ с другим телом → `409` |

- Интеграционные и e2e-тесты пропускаются при `go test -short` (`testing.Short()`).
- Полный прогон — `go test -race ./...`, нужен Docker.
- Тесты наблюдаемости (v0.2.0) — раздел 14.6.

## 12. Запуск и демо

**docker compose:**
- `postgres` (`postgres:18`);
- `migrate` (`gateway migrate`, однократно);
- `api` и `worker` (стартуют после успешного `migrate`);
- `northbank` (по умолчанию);
- `eastbank` (`MOCKBANK_LATENCY=1s`, `MOCKBANK_FAIL_FIRST=2`);
- `westbank` (`MOCKBANK_CALLBACK_URL=http://api:8080/v1/webhooks/banks/westbank`,
  `MOCKBANK_CALLBACK_DELAY=5s`); `MOCKBANK_CALLBACK_SECRET` совпадает с
  `BANK_WESTBANK_WEBHOOK_SECRET` у `api`;
- с v0.2.0: `prometheus` (порт 9090; опрашивает `api:9100` и `worker:9100` раз в 5 с,
  правила алертов), `jaeger` (Jaeger v2 all-in-one: UI на порту 16686, приём OTLP/HTTP
  на 4318 только внутри сети compose), `grafana` (порт 3000; источники данных и дашборд
  подключаются provisioning-ом, анонимный вход — только для локального стенда). У `api`
  и `worker` заданы `OTEL_EXPORTER_OTLP_ENDPOINT=http://jaeger:4318` и
  `OTEL_SERVICE_NAME`.

Образ — многостадийный `Dockerfile` (сборка на `golang:1.26`, финальный образ —
`gcr.io/distroless/static`), оба бинарника в одном образе.

**Makefile:** `test` (все тесты с `-race`), `test-short`, `lint`, `up`, `down`, `demo`.

**`scripts/demo.sh`:** отправляет заявку с `Idempotency-Key`, раз в секунду опрашивает
`GET` до `completed` (не дольше 60 с), печатает результат через `jq`. С v0.2.0 затем
печатает ссылки на дашборд Grafana и на поиск трейса заявки в Jaeger по тегу
`application.id`.

## 13. Процесс разработки

### 13.1 Git — gitflow

- `main` — только релизы; каждый merge в `main` помечается тегом `vX.Y.Z`.
- `develop` — интеграционная ветка; от неё создаются все feature-ветки.
- `feature/<slug>` — одна задача плана = одна ветка = один PR в `develop`. Агенты
  (локальные и облачные) работают только в feature-ветках.
- `release/X.Y.Z` — от `develop`; PR в `main`, тег, merge обратно в `develop`.
- `hotfix/X.Y.Z` — от `main` при необходимости, вливается в `main` и `develop`.
- `main` и `develop` защищены: только через PR и только с зелёным CI (настраивается
  в GitHub вручную).
- Коммиты — Conventional Commits на английском (`feat:`, `fix:`, `test:`, `docs:`,
  `chore:`, `ci:`).
- MVP выходит как `release/0.1.0` → тег `v0.1.0`; наблюдаемость — `release/0.2.0` →
  `v0.2.0`.

### 13.2 CI

`.github/workflows/ci.yml`, запускается на `pull_request` в `develop`/`main` и на `push`
в `develop`/`main`:
- `gofmt -l` пуст, `go vet ./...`, `golangci-lint run`;
- `go test -race ./...` (Docker на `ubuntu-latest` есть, testcontainers работают).

### 13.3 Агенты

- **`CLAUDE.md`** — команды сборки и тестов, карта пакетов (раздел 3), соглашения
  (деньги в копейках, ПДн не логируем, сначала тест, классы ошибок банков), правила
  gitflow для агентов, критерий готовности задачи: тесты с `-race` и линтер зелёные.
- **`.claude/agents/developer.md`** (модель Sonnet, все инструменты): берёт одну задачу
  плана, работает через TDD в feature-ветке, перед завершением запускает `make test`
  и `make lint`. Спеку не меняет; при неоднозначности останавливается и спрашивает.
- **`.claude/agents/reviewer.md`** (модель Opus, инструменты Read, Grep, Glob, Bash):
  код не правит. Сверяет изменения со спекой и чек-листом (идемпотентность, гонки и
  защита владения, классификация ошибок, ПДн в логах, покрытие тестами) и возвращает
  список замечаний с уровнем `blocker` / `major` / `minor`.

### 13.4 Этапы

1. **Локально.** Гигиена репозитория (`.gitignore`, модуль
   `github.com/Vlad-Pisarevskiy/loan-gateway`, `CLAUDE.md`, роли агентов, CI, Makefile),
   затем последовательно: хранилище и миграции → домен → API → воркер и чистильщик →
   инфраструктура вебхуков (тестируется на фейковом `CallbackHandler`) → заглушка банков
   (правила решений, диалект `north`); конфигурация из env.
2. **Облако.** Адаптер northbank, бинарники, e2e на northbank, compose и демо —
   параллельно там, где позволяют зависимости; затем две независимые сессии:
   - eastbank: адаптер, диалект `east`, сервис в compose, e2e-сценарии ретраев и таймаута;
   - westbank: адаптер с `CallbackHandler`, диалект `west` с отправкой вебхука, сервис в
     compose, e2e-сценарии асинхронного решения и `callback timeout`.

   Обе задачи трогают `compose.yaml`, реестр банков и выбор диалекта в заглушке;
   конфликты в этих местах разрешаются при merge второго PR.
3. **Релиз.** Раздел README «Как разрабатывался проект», `release/0.1.0` → `v0.1.0`.
4. **v0.2.0 — наблюдаемость** (раздел 14). Пакет `internal/telemetry` и хранилище —
   локально; `httpapi`, `dispatch` и инфраструктура compose — три параллельные облачные
   сессии; сборка в `cmd/gateway`, e2e и релиз `release/0.2.0` → `v0.2.0` — локально.
5. **Дальше** — задачи из «Не входит» (раздел 2), пока позволяет бюджет.

### 13.5 Журнал разработки

`docs/dev-log.md` — таблица, строка на каждую завершённую задачу:
дата · задача · где (локально / облако) · PR · замечаний ревью (исправлено / всего) ·
стоимость сессии. Из журнала берутся цифры для README.

## 14. Наблюдаемость (v0.2.0)

Метрики Prometheus, трейсинг OpenTelemetry, дашборд Grafana и алерты. По метрикам видно
состояние шлюза и каждого банка, по трейсу — путь одной заявки через API, очередь,
воркер, банк и вебхук.

### 14.1 Стек

- **Метрики** — `github.com/prometheus/client_golang`. `api` и `worker` отдают
  `GET /metrics` на отдельном адресе `METRICS_ADDR` (по умолчанию `:9100`), не на
  публичном порту API; `migrate` метрик не отдаёт.
- **Трейсы** — OpenTelemetry SDK (`go.opentelemetry.io/otel`, SDK, экспортёр OTLP/HTTP),
  отправка напрямую в Jaeger v2, без OpenTelemetry Collector. Пропагатор — W3C Trace
  Context.
- **Хранение и UI** — Prometheus, Jaeger и Grafana в compose (раздел 12).
- **Пакет `internal/telemetry`** — описания метрик, настройка трейсинга, обёртка `slog`,
  преобразование контекста трейса в строку `traceparent` и обратно. `httpapi` и
  `dispatch` получают метрики и трейсер параметрами; `nil` вместо метрик — запись
  выключена (так работают тесты, которым метрики не нужны).

### 14.2 Метрики

Префикс — `loangw_`. Метки — только из ограниченных наборов значений (шаблон маршрута,
код банка, исход); идентификаторов и ПДн в метках нет. Гистограммы — стандартные бакеты
`client_golang` (`prometheus.DefBuckets`, от 5 мс до 10 с).

| Метрика | Тип | Метки | Процесс | Когда меняется |
|---|---|---|---|---|
| `loangw_http_requests_total` | counter | `route`, `method`, `code` | api | каждый ответ API; `route` — шаблон из таблицы маршрутов (`/v1/applications/{id}`), для неизвестных путей — `other` |
| `loangw_http_request_duration_seconds` | histogram | `route`, `method` | api | то же |
| `loangw_applications_total` | counter | `result` = `created` \| `replayed` \| `conflict` | api | создание заявки; повтор с тем же ключом и телом; тот же ключ с другим телом (`409`) |
| `loangw_webhooks_total` | counter | `bank`, `result` = `applied` \| `duplicate` \| `unknown` \| `unauthorized` \| `bad_body` \| `unknown_bank` \| `error` | api | каждый вебхук (5.3): решение применено; запрос не ждёт вебхук; неизвестный `external_id`; подпись не прошла (`401`); тело не прочитано или не разобрано; банк не принимает вебхуки (`bank="other"`); внутренняя ошибка (`500`) |
| `loangw_bank_calls_total` | counter | `bank`, `outcome` = `decision` \| `accepted` \| `retry` \| `failed` | worker | каждая обработанная задача (исход из раздела 8, шаг 3) |
| `loangw_bank_call_duration_seconds` | histogram | `bank`, `outcome` | worker | длительность вызова `Submit`, без записи результата в БД |
| `loangw_bank_decisions_total` | counter | `bank`, `status` = `approved` \| `rejected`, `via` = `sync` \| `webhook` | worker, api | решение записано: синхронное — воркером, из вебхука — `api` |
| `loangw_worker_tasks_in_flight` | gauge | — | worker | число вызовов банков, идущих прямо сейчас |
| `loangw_queue_errors_total` | counter | `op` = `claim` \| `complete` \| `sweep` \| `stats` | worker | ошибка обращения к очереди |
| `loangw_lease_lost_total` | counter | — | worker | `Complete` сообщил, что аренду перехватили |
| `loangw_sweeper_actions_total` | counter | `action` = `callback_timeout` \| `requeued` \| `lease_expired` | worker | по итогам прохода чистильщика |
| `loangw_bank_requests` | gauge | `bank`, `status` = `queued` \| `sending` \| `waiting_callback` | worker | читается из БД при каждом опросе Prometheus |
| `loangw_queue_oldest_age_seconds` | gauge | — | worker | `now() − min(next_attempt_at)` среди `queued` с `next_attempt_at <= now()`; `0`, если таких нет |

Gauge из БД воркер считает при опросе одним запросом по незавершённым статусам
(частичные индексы раздела 7) с тем же таймаутом, что и остальные обращения воркера к
БД. При ошибке эти gauge не отдаются, а растёт `loangw_queue_errors_total{op="stats"}`.
При нескольких экземплярах воркера их значения совпадают — в дашборде и алертах берётся
`max`. Метрики рантайма Go и процесса (`go_*`, `process_*`) — стандартные коллекторы
`client_golang`.

### 14.3 Алерты и дашборд

Правила лежат в `deploy/observability/alerts.yml` и вычисляются Prometheus. Alertmanager
не входит: срабатывания видны в UI Prometheus и в Grafana.

| Алерт | Условие | Сколько держится |
|---|---|---|
| `TargetDown` | `up{job=~"api\|worker"} == 0` | 1 мин |
| `BankErrorRateHigh` | по банку: доля `outcome` = `retry` или `failed` в `rate(loangw_bank_calls_total[5m])` больше 0,2 | 5 мин |
| `QueueBacklog` | `max(loangw_queue_oldest_age_seconds) > 60` | 2 мин |
| `CallbackTimeouts` | `increase(loangw_sweeper_actions_total{action="callback_timeout"}[10m]) > 0` | сразу |

eastbank в compose намеренно нестабилен (`MOCKBANK_FAIL_FIRST=2`: две попытки из трёх —
`retry`), поэтому после демо `BankErrorRateHigh` по eastbank может ненадолго сработать —
это ожидаемо.

Дашборд «Loan Gateway» — JSON в `deploy/observability/grafana/`, подключается
provisioning-ом вместе с источниками данных Prometheus и Jaeger. Панели: нагрузка и
ошибки API по `route` и `code`; задержка вызовов банков p50/p95/p99 по банкам; исходы
вызовов по банкам; решения по банкам; очередь по статусам и возраст самой старой задачи;
вызовы в работе; действия чистильщика и результаты вебхуков.

### 14.4 Трейсинг

**Включение.** Трейсинг включён, если задан `OTEL_EXPORTER_OTLP_ENDPOINT` (или
`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT`); иначе работает no-op трейсер и ничего не
отправляется. Переменные `OTEL_*` читает SDK (раздел 9). Спаны отправляются пакетами; при
остановке процесс досылает их в пределах `SHUTDOWN_TIMEOUT`. Ошибки экспорта на обработку
заявок не влияют.

**Спаны:**

| Спан | Вид | Процесс | Родитель | Атрибуты |
|---|---|---|---|---|
| `<METHOD> <route>`, например `POST /v1/applications` | server | api | входящий `traceparent` клиента, если есть; иначе корень | `http.request.method`, `http.route`, `http.response.status_code`; `application.id`, когда он известен |
| `bank_request.process` | consumer | worker | контекст из `bank_requests.trace_parent`; пустой или невалидный — корень | `application.id`, `bank_request.id`, `bank.code`, `attempt`, `outcome` |
| `bank.submit` | client | worker | `bank_request.process` | `bank.code`, `outcome`; при ошибке `error.type` = `temporary` \| `permanent` |
| `queue.complete` | internal | worker | `bank_request.process` | `result` = `saved` \| `lease_lost` \| `error` |
| `bank_request.callback` | internal | api | контекст из `bank_requests.trace_parent` | `bank_request.id`, `bank.code`, `decision.status`; ссылка (span link) на серверный спан вебхука |

**Передача через очередь.** При создании заявки (5.1) в той же транзакции в
`trace_parent` каждого `bank_request` записывается контекст текущего серверного спана в
формате W3C `traceparent`. Каждая попытка воркера — отдельный `bank_request.process` в
трейсе заявки, поэтому ретраи видны как последовательность попыток под одним `POST`.

**Вебхук.** Банк контекст трейса не присылает, поэтому серверный спан вебхука начинает
свой трейс. Если решение применено (5.3, шаг 4) и у запроса есть `trace_parent`, `api`
создаёт `bank_request.callback` в трейсе заявки со ссылкой на спан вебхука; для этого
применение вебхука возвращает `id` и `trace_parent` запроса. Для повторов, опозданий и
ошибок спан в трейсе заявки не создаётся.

**Ошибки.** Спан получает статус `Error` с текстом ошибки при ответе API `5xx`, при ошибке
`Submit` и при ошибке записи результата. Тексты ошибок по разделу 10 не содержат ПДн.

**Не трейсим:** вызовы банков не получают заголовок `traceparent` — это внешние партнёры;
заглушки `mockbank`; захват задач и чистильщик — пакетные операции, их покрывают метрики;
`/healthz` и `/readyz` учитываются в метриках, но спанов не создают; `/metrics` не
инструментируется.

**ПДн.** В атрибуты спанов попадают только идентификаторы, коды и статусы из таблицы
выше; ФИО, телефон, дата рождения, суммы анкеты и тела запросов — никогда.

### 14.5 Логи

Обёртка над `slog.Handler` добавляет в запись `trace_id` и `span_id`, если в контексте
записи есть валидный спан. Записи, связанные с запросом API и с задачей воркера, пишутся с
контекстом (`InfoContext` и т. п.). По `trace_id` из лога трейс находится в Jaeger.

### 14.6 Тестирование

| Уровень | Что проверяет |
|---|---|
| **Юнит** | метрики через `prometheus/testutil`: каждый исход воркера увеличивает свой счётчик; `route` — шаблон маршрута, неизвестный путь → `other`; вебхук неизвестного банка → `bank="other"`. Трейсы через `tracetest.SpanRecorder`: родитель `bank_request.process` — сохранённый `traceparent`, невалидный `traceparent` → корневой спан. Контекст ↔ `traceparent` без потерь. Обёртка `slog` добавляет `trace_id` |
| **Интеграционные** | миграция добавляет колонку; `Create` записывает `trace_parent`, `Claim` и применение вебхука возвращают его; запрос gauge-метрик очереди: числа по банкам и статусам, возраст самой старой готовой задачи, `0` при пустой очереди |
| **E2E** | заявка в три банка: все её спаны в одном трейсе (`POST`, попытки каждого банка, `bank_request.callback` westbank со ссылкой на спан вебхука); ни один атрибут ни одного спана не содержит ФИО, телефон или дату рождения из анкеты; `/metrics` у `api` и `worker` отдаёт серии из 14.2 |

Ручная проверка перед релизом: `make up && make demo`; в Prometheus обе цели `up`,
дашборд в Grafana показывает данные, трейс заявки открывается в Jaeger по ссылке из демо.

### 14.7 Критерии готовности v0.2.0

1. `make up` поднимает систему вместе с Prometheus, Jaeger и Grafana; `make demo`
   печатает решения трёх банков и ссылки на дашборд и трейс заявки; трейс содержит
   попытки всех трёх банков и вебхук westbank.
2. Тесты из 14.6 и все прежние, `go vet`, `golangci-lint` — зелёные локально и в CI.
3. Спека, план и журнал обновлены; README описывает наблюдаемость.
