-- +goose Up
-- Контекст трейса заявки в формате W3C traceparent (раздел 14.4 спеки).
ALTER TABLE bank_requests ADD COLUMN trace_parent text;

-- +goose Down
ALTER TABLE bank_requests DROP COLUMN trace_parent;
