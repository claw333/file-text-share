#!/bin/sh
set -eu

CDPATH=''
export CDPATH
project_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

if ! command -v docker >/dev/null 2>&1; then
    echo "错误：未找到 Docker。请先安装 Docker Engine 与 Compose 插件。" >&2
    exit 1
fi

if [ ! -f .env ]; then
    echo "错误：缺少 .env。请先复制 .env.example 并填写域名与邮箱。" >&2
    exit 1
fi

set -a
# shellcheck disable=SC1091
. ./.env
set +a

case "${PUBLIC_HOST:-}" in
    ""|share.example.com|*/*|*:*|*" "*)
        echo "错误：请在 .env 中填写有效的单个公网域名或 IPv4 地址。" >&2
        exit 1
        ;;
esac

case "${LETSENCRYPT_EMAIL:-}" in
    ""|admin@example.com) email_args="--register-unsafely-without-email" ;;
    *@*.*) email_args="--email $LETSENCRYPT_EMAIL" ;;
    *)
        echo "错误：请在 .env 中填写有效的 Let's Encrypt 联系邮箱。" >&2
        exit 1
        ;;
esac

staging_arg=""
if [ "${LETSENCRYPT_STAGING:-false}" = "true" ]; then
    staging_arg="--staging"
fi

identifier_args="--domain $PUBLIC_HOST"
case "$PUBLIC_HOST" in
    *[!0-9.]*|'') ;;
    *) identifier_args="--ip-address $PUBLIC_HOST --preferred-profile shortlived" ;;
esac

cleanup() {
    docker compose --profile bootstrap stop nginx-bootstrap >/dev/null 2>&1 || true
}
trap cleanup EXIT INT TERM

echo "[1/5] 构建并启动应用服务"
docker compose --profile bootstrap stop nginx nginx-bootstrap >/dev/null 2>&1 || true
docker compose up -d --build app

echo "[2/5] 启动仅用于 ACME HTTP-01 的 80 端口服务"
docker compose --profile bootstrap up -d nginx-bootstrap

echo "[3/5] 向 Let's Encrypt 申请证书"
# The optional argument strings are intentionally word-split.
# shellcheck disable=SC2086
docker compose run --rm --no-deps certbot certonly \
    --webroot \
    --webroot-path /var/www/certbot \
    --cert-name "$PUBLIC_HOST" \
    --agree-tos \
    --no-eff-email \
    --non-interactive \
    $email_args \
    $identifier_args \
    $staging_arg

echo "[4/5] 切换到正式 HTTPS Nginx"
cleanup
docker compose up -d nginx

echo "[5/5] 校验服务状态"
docker compose exec -T nginx nginx -t
docker compose ps

echo "完成：https://$PUBLIC_HOST"
