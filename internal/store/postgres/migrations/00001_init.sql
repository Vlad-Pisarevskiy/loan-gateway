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
