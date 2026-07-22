#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

cat >"$tmp_dir/.env.example" <<'EOF'
DOCUMENT_SERVER_JWT_SECRET=
GATEWAY_ADMIN_SESSION_SECRET=
GATEWAY_CALLBACK_CAPABILITY_SECRET=
WEBHOOK_SECRET_ENCRYPTION_KEY=
EOF

cat >"$tmp_dir/.env" <<'EOF'
DOCUMENT_SERVER_JWT_SECRET=keep-this-existing-value
GATEWAY_ADMIN_SESSION_SECRET=
GATEWAY_CALLBACK_CAPABILITY_SECRET=
WEBHOOK_SECRET_ENCRYPTION_KEY=
EOF

ENV_FILE="$tmp_dir/.env" ENV_EXAMPLE_FILE="$tmp_dir/.env.example" \
    "$repo_dir/scripts/init-secrets.sh"

grep -qx 'DOCUMENT_SERVER_JWT_SECRET=keep-this-existing-value' "$tmp_dir/.env"
for name in GATEWAY_ADMIN_SESSION_SECRET GATEWAY_CALLBACK_CAPABILITY_SECRET WEBHOOK_SECRET_ENCRYPTION_KEY; do
    value=$(sed -n "s/^${name}=//p" "$tmp_dir/.env")
    test -n "$value"
done

mode=$(stat -f '%Lp' "$tmp_dir/.env" 2>/dev/null || stat -c '%a' "$tmp_dir/.env")
test "$mode" = "600"
