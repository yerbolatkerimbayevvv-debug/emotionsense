#!/usr/bin/env bash
# Демо-сценарий для защиты: регистрация → логин → загрузка фото → опрос статуса → статистика.
# Использование: ./scripts/demo.sh path/to/face.jpg [http://localhost:8080]
set -euo pipefail

IMG="${1:-}"
BASE="${2:-http://localhost:8080}/api/v1"

if [[ -z "$IMG" || ! -f "$IMG" ]]; then
  echo "usage: $0 path/to/face.jpg [base_url]" >&2
  exit 1
fi
command -v jq >/dev/null || { echo "jq is required (brew install jq / apt install jq)" >&2; exit 1; }

EMAIL="demo+$(date +%s)@example.com"
PASS="demo-password-123"

echo "==> Register $EMAIL"
REG=$(curl -sS -X POST "$BASE/auth/register" -H 'Content-Type: application/json' \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}")
echo "$REG" | jq -c '.user'
TOKEN=$(echo "$REG" | jq -r '.tokens.access_token')

echo "==> Upload image"
ACCEPTED=$(curl -sS -X POST "$BASE/analyses" -H "Authorization: Bearer $TOKEN" -F "image=@$IMG")
echo "$ACCEPTED" | jq -c .
ID=$(echo "$ACCEPTED" | jq -r '.id')

echo "==> Poll status"
for i in $(seq 1 60); do
  RES=$(curl -sS "$BASE/analyses/$ID" -H "Authorization: Bearer $TOKEN")
  STATUS=$(echo "$RES" | jq -r '.status')
  printf "   [%2d] %s\n" "$i" "$STATUS"
  if [[ "$STATUS" == "done" || "$STATUS" == "failed" ]]; then
    echo "$RES" | jq .
    break
  fi
  sleep 1
done

echo "==> My stats (7 days)"
curl -sS "$BASE/stats/me?days=7" -H "Authorization: Bearer $TOKEN" | jq .

echo "==> History"
curl -sS "$BASE/analyses?limit=5" -H "Authorization: Bearer $TOKEN" | jq -c '.items[] | {id, status, dominant}'
