#!/bin/sh
set -eu

CDPATH=''
export CDPATH
project_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
service_file=/etc/systemd/system/file-text-share-cert-renew.service
timer_file=/etc/systemd/system/file-text-share-cert-renew.timer
service_user=${SERVICE_USER:-${SUDO_USER:-$(id -un)}}
service_group=${SERVICE_GROUP:-}

if [ "$service_user" = "root" ]; then
    echo "错误：证书续期 service 不能以 root 运行。请以部署用户执行本脚本，或设置 SERVICE_USER/SERVICE_GROUP。" >&2
    exit 1
fi

if [ -z "$service_group" ]; then
    service_group=$(id -gn "$service_user")
fi

if ! command -v systemctl >/dev/null 2>&1; then
    echo "错误：服务器未使用 systemd，请改用 cron 每 12 小时运行 deploy/renew-cert.sh。" >&2
    exit 1
fi

tmp_service=$(mktemp)
tmp_timer=$(mktemp)
trap 'rm -f "$tmp_service" "$tmp_timer"' EXIT INT TERM

escape_sed_replacement() {
    printf '%s' "$1" | sed 's/[&|]/\\&/g'
}

app_dir_replacement=$(escape_sed_replacement "$project_dir")
app_user_replacement=$(escape_sed_replacement "$service_user")
app_group_replacement=$(escape_sed_replacement "$service_group")

sed \
    -e "s|@APP_DIR@|$app_dir_replacement|g" \
    -e "s|@APP_USER@|$app_user_replacement|g" \
    -e "s|@APP_GROUP@|$app_group_replacement|g" \
    deploy/systemd/file-text-share-cert-renew.service > "$tmp_service"
cp deploy/systemd/file-text-share-cert-renew.timer "$tmp_timer"

sudo install -m 0644 "$tmp_service" "$service_file"
sudo install -m 0644 "$tmp_timer" "$timer_file"
sudo systemctl daemon-reload
sudo systemctl enable --now file-text-share-cert-renew.timer
sudo systemctl list-timers file-text-share-cert-renew.timer --no-pager
