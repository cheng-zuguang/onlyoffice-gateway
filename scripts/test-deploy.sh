#!/bin/bash
# =============================================================================
# Gateway 部署验证脚本
# 用法: bash scripts/test-deploy.sh <HOST> [ADMIN_PASSWORD]
# =============================================================================
set -e

HOST="${1:-localhost}"
PASS="${2:-admin123}"
GATEWAY="http://$HOST:18080"
ADMIN_UI="http://$HOST:18081"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[0;33m'
NC='\033[0m'

pass()  { echo -e "${GREEN}✓${NC} $1"; }
fail()  { echo -e "${RED}✗${NC} $1 — $2"; exit 1; }
warn()  { echo -e "${YELLOW}~${NC} $1 — $2"; }

echo "============================================"
echo " Gateway 部署验证  →  $HOST"
echo "============================================"
echo ""

echo "── Gateway API ($GATEWAY) ──"

echo -n "  Health check ... "
curl -sf "$GATEWAY/api/v1/health" > /dev/null && pass "OK" || fail "FAIL" "Gateway 未响应"

echo -n "  Document Server ... "
DS=$(curl -sf "$GATEWAY/api/v1/health/ds") || true
echo "$DS" | grep -q '"document_server_ok":true' && pass "OK" || warn "不可达" "检查 DS 容器"

echo -n "  Admin login ... "
LOGIN=$(curl -sf -X POST "$GATEWAY/admin/api/login" \
  -H 'Content-Type: application/json' \
  -d "{\"username\":\"admin\",\"password\":\"$PASS\"}")
TOKEN=$(echo "$LOGIN" | python3 -c "import sys,json; print(json.load(sys.stdin).get('token',''))" 2>/dev/null)
[ -n "$TOKEN" ] && pass "OK" || fail "FAIL" "密码错误？"

echo -n "  Service CRUD ... "
curl -sf -X POST "$GATEWAY/admin/api/services" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"id":"_test_","public_key":"pk","allowed_webhook_domains":["l"]}' > /dev/null
curl -sf -X PUT "$GATEWAY/admin/api/services/_test_" \
  -H 'Content-Type: application/json' \
  -H "Authorization: Bearer $TOKEN" \
  -d '{"id":"_test_","public_key":"pk2","allowed_webhook_domains":["e"]}' > /dev/null
curl -sf -X DELETE "$GATEWAY/admin/api/services/_test_" \
  -H "Authorization: Bearer $TOKEN" > /dev/null
pass "OK (create / update / delete)"

echo ""
echo "── Admin UI ($ADMIN_UI) ──"
echo -n "  SPA reachable ... "
curl -sf "$ADMIN_UI/admin/login" > /dev/null 2>&1 && pass "OK" || warn "不可达" "检查 admin-ui 容器"

echo ""
echo "============================================"
echo -e " ${GREEN}All checks passed${NC}"
echo ""
echo "  管理端 →  $ADMIN_UI/admin/login"
echo "  网关   →  $GATEWAY"
echo "============================================"
