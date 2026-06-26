#!/bin/sh
set -eu

CDPATH=''
export CDPATH
repo_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)

assert_contains() {
    file="$1"
    expected="$2"
    if ! grep -Fq "$expected" "$file"; then
        echo "missing expected text in $file: $expected" >&2
        exit 1
    fi
}

app_template="$repo_dir/deploy/nginx/app.conf.template"
bootstrap_template="$repo_dir/deploy/nginx/bootstrap.conf.template"
compose_file="$repo_dir/compose.yaml"
init_script="$repo_dir/deploy/init-letsencrypt.sh"

assert_contains "$compose_file" "DOODLE_HOST: \${DOODLE_HOST:?Set DOODLE_HOST in .env}"
assert_contains "$compose_file" "NGINX_ENVSUBST_FILTER: ^(PUBLIC_HOST|DOODLE_HOST)$"
assert_contains "$compose_file" "\${DOODLE_STATIC_ROOT:-./doodle-static}:/usr/share/nginx/doodle:ro"

assert_contains "$app_template" "server_name \${DOODLE_HOST};"
assert_contains "$app_template" "ssl_certificate /etc/letsencrypt/live/\${DOODLE_HOST}/fullchain.pem;"
assert_contains "$app_template" "root /usr/share/nginx/doodle;"
assert_contains "$app_template" "location ^~ /api/ {"
assert_contains "$app_template" "proxy_pass http://app:8080;"

assert_contains "$bootstrap_template" "server_name \${DOODLE_HOST};"
assert_contains "$bootstrap_template" "location ^~ /.well-known/acme-challenge/ {"
assert_contains "$bootstrap_template" "return 308 https://\${DOODLE_HOST}\$request_uri;"

assert_contains "$init_script" 'issue_certificate "$PUBLIC_HOST"'
assert_contains "$init_script" 'issue_certificate "$DOODLE_HOST"'
