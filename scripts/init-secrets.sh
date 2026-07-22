#!/bin/sh
set -eu

umask 077

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
env_file=${ENV_FILE:-"$repo_dir/.env"}
example_file=${ENV_EXAMPLE_FILE:-"$repo_dir/.env.example"}

if ! command -v openssl >/dev/null 2>&1; then
    echo "openssl is required to generate Gateway secrets" >&2
    exit 1
fi

if [ ! -f "$env_file" ]; then
    if [ ! -f "$example_file" ]; then
        echo "environment template not found: $example_file" >&2
        exit 1
    fi
    cp "$example_file" "$env_file"
fi

set_value_if_empty() {
    key=$1
    value=$(openssl rand -base64 32 | tr -d '\n')
    tmp_file=$(mktemp "${env_file}.tmp.XXXXXX")
    awk -v key="$key" -v value="$value" '
        BEGIN { found = 0 }
        index($0, key "=") == 1 {
            found = 1
            if ($0 == key "=") {
                print key "=" value
            } else {
                print
            }
            next
        }
        { print }
        END {
            if (!found) {
                print key "=" value
            }
        }
    ' "$env_file" >"$tmp_file"
    mv "$tmp_file" "$env_file"
}

set_value_if_empty DOCUMENT_SERVER_JWT_SECRET
set_value_if_empty GATEWAY_ADMIN_SESSION_SECRET
set_value_if_empty GATEWAY_CALLBACK_CAPABILITY_SECRET
set_value_if_empty WEBHOOK_SECRET_ENCRYPTION_KEY
chmod 600 "$env_file"

echo "Gateway secrets are initialized in $env_file (existing values preserved)."
