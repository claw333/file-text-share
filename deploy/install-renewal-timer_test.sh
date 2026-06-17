#!/bin/sh
set -eu

CDPATH=''
export CDPATH
repo_dir=$(cd -- "$(dirname -- "$0")/.." && pwd)
tmp_dir=$(mktemp -d)
fakebin="$tmp_dir/bin"
systemd_dir="$tmp_dir/systemd"
systemctl_log="$tmp_dir/systemctl.log"

cleanup() {
    rm -rf "$tmp_dir"
}
trap cleanup EXIT INT TERM

mkdir -p "$fakebin" "$systemd_dir"
: > "$systemctl_log"

cat > "$fakebin/systemctl" <<'SCRIPT'
#!/bin/sh
set -eu
printf '%s\n' "$*" >> "$SYSTEMCTL_LOG"
SCRIPT
chmod +x "$fakebin/systemctl"

cat > "$fakebin/sudo" <<'SCRIPT'
#!/bin/sh
set -eu

case "$1" in
    install)
        shift
        if [ "$1" != "-m" ]; then
            echo "fake sudo only supports install -m" >&2
            exit 1
        fi
        mode="$2"
        src="$3"
        dest="$4"
        case "$dest" in
            /etc/systemd/system/*)
                install -m "$mode" "$src" "$SYSTEMD_TEST_DIR/$(basename "$dest")"
                ;;
            *)
                echo "unexpected install destination: $dest" >&2
                exit 1
                ;;
        esac
        ;;
    systemctl)
        shift
        systemctl "$@"
        ;;
    *)
        echo "unexpected sudo command: $*" >&2
        exit 1
        ;;
esac
SCRIPT
chmod +x "$fakebin/sudo"

assert_contains() {
    file="$1"
    expected="$2"
    if ! grep -Fqx "$expected" "$file"; then
        echo "missing expected line in $file: $expected" >&2
        echo "--- $file ---" >&2
        cat "$file" >&2
        exit 1
    fi
}

assert_not_contains() {
    file="$1"
    unexpected="$2"
    if grep -Fq "$unexpected" "$file"; then
        echo "unexpected text in $file: $unexpected" >&2
        echo "--- $file ---" >&2
        cat "$file" >&2
        exit 1
    fi
}

(
    cd "$repo_dir"
    PATH="$fakebin:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    SYSTEMD_TEST_DIR="$systemd_dir" \
    SERVICE_USER=deploy \
    SERVICE_GROUP=deploy \
    sh deploy/install-renewal-timer.sh >/dev/null
)

service_file="$systemd_dir/file-text-share-cert-renew.service"
timer_file="$systemd_dir/file-text-share-cert-renew.timer"

test -f "$service_file"
test -f "$timer_file"

assert_contains "$service_file" "User=deploy"
assert_contains "$service_file" "Group=deploy"
assert_contains "$service_file" "WorkingDirectory=$repo_dir"
assert_contains "$service_file" "ExecStart=$repo_dir/deploy/renew-cert.sh"
assert_contains "$service_file" "NoNewPrivileges=true"
assert_not_contains "$service_file" "@APP_"
assert_contains "$systemctl_log" "daemon-reload"
assert_contains "$systemctl_log" "enable --now file-text-share-cert-renew.timer"

if (
    cd "$repo_dir"
    PATH="$fakebin:$PATH" \
    SYSTEMCTL_LOG="$systemctl_log" \
    SYSTEMD_TEST_DIR="$systemd_dir" \
    SERVICE_USER=root \
    SERVICE_GROUP=root \
    sh deploy/install-renewal-timer.sh >/dev/null 2>&1
); then
    echo "installer accepted root as service user" >&2
    exit 1
fi
