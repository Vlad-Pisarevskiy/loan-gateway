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
    TAGS=$(jq -rn --arg id "$ID" '{"application.id": $id} | tojson | @uri')
    echo
    echo "Дашборд: ${GRAFANA:-http://localhost:3000}/d/loan-gateway"
    echo "Трейс:   ${JAEGER:-http://localhost:16686}/search?service=gateway-api&operation=POST%20%2Fv1%2Fapplications&lookback=1h&tags=$TAGS"
    exit 0
  fi
  sleep 1
done
echo "Заявка не завершилась за 60 с:" >&2
jq . <<<"$RESP" >&2
exit 1
