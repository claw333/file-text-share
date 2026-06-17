#!/bin/sh
set -eu

CDPATH=''
export CDPATH
project_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_dir"

if [ ! -f .env ]; then
    echo "错误：缺少 $project_dir/.env" >&2
    exit 1
fi

docker compose run --rm --no-deps certbot renew --quiet --no-random-sleep-on-renew
docker compose exec -T nginx nginx -s reload
