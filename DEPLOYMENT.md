# 轻递从零部署手册

本文档给 AI agent 或维护者使用，目标是在没有任何聊天上下文的情况下，把本项目从本地代码目录部署到一台 Linux VPS，并完成域名 HTTPS、禁止直接 IP 访问、自动续期、防火墙和后续更新发布。

本文档使用以下占位变量。执行前先在本地 shell 设置为真实值，不要把真实值提交进仓库：

```bash
export SSH_TARGET=deploy@example-vps
export SSH_PORT=22
export PUBLIC_HOST=share.example.com
export SERVER_IP=<SERVER_IP>
export PROJECT_DIR=/opt/file-text-share
```

如果部署到其他服务器或域名，把下文变量替换成实际值。

## 0. 安全边界

- 不要把密码、Cloudflare token、证书私钥、`.env` 实际内容粘贴进代码、日志或回复。
- `.env` 只保存在服务器项目目录，权限设置为 `600`。
- 数据库、上传文件、证书都在 Docker volume 中，更新代码时不要删除 volume。
- 禁止执行 `docker compose down -v`，除非用户明确要求清空所有数据。
- 生产入口只允许 HTTPS 443。80 端口只用于 HTTP 到 HTTPS 跳转和 Let's Encrypt HTTP-01 验证。
- 直接用 IP 或非配置域名访问时，Nginx 应断开 HTTP 连接，并在 HTTPS 阶段拒绝未知 SNI。

## 1. 本地准备

在本地项目根目录执行：

```bash
cd /path/to/file-text-share
git status --short --branch
git log -1 --oneline --decorate
go test ./...
```

如果本地没有 Go 环境，至少确认代码提交和文件状态；但正式部署前应尽量让 `go test ./...` 通过。

记录当前提交号，用作镜像标签：

```bash
APP_IMAGE_TAG="$(date +%Y%m%d)-$(git rev-parse --short HEAD)"
echo "$APP_IMAGE_TAG"
```

## 2. 服务器前提检查

以下命令以 `$SSH_TARGET` 为 SSH 目标。若没有该变量，替换成 `user@host` 或配置 SSH。

```bash
ssh "$SSH_TARGET" 'set -e
hostname
date -u
cat /etc/os-release | sed -n "1,6p"
command -v docker
docker version --format "{{.Server.Version}}"
docker compose version
ss -lntp | sed -n "1,80p"
'
```

要求：

- 64 位 Linux VPS。
- Docker Engine 和 `docker compose` 插件可用。
- 域名 A/AAAA 已解析到服务器公网 IPv4/IPv6。
- 服务器上 80 和 443 未被其他服务占用。
- SSH 当前连接端口必须确认清楚，启用 UFW 前必须先放行该端口。

域名解析验证：

```bash
dig +short A "$PUBLIC_HOST"
dig +short AAAA "$PUBLIC_HOST"
ssh "$SSH_TARGET" 'curl -4 -fsS https://ifconfig.me; echo; curl -6 -fsS https://ifconfig.me || true; echo'
```

Cloudflare DNS 使用 HTTP-01 时不需要 API key。建议初次签发证书时将记录设为 DNS only，避免 Cloudflare 的跳转、安全规则或缓存影响 `/.well-known/acme-challenge/`。

## 3. 创建服务器目录并同步代码

第一次部署：

```bash
ssh "$SSH_TARGET" "sudo mkdir -p '$PROJECT_DIR' && sudo chown \"\\$USER\":\"\\$USER\" '$PROJECT_DIR'"
```

从本地同步代码到服务器，保留服务器侧 `.env`、运行数据和 Git 元数据：

```bash
rsync -az --delete \
  --exclude ".git/" \
  --exclude ".env" \
  --exclude "data/" \
  ./ "$SSH_TARGET:$PROJECT_DIR/"
```

说明：

- `.env` 不同步，避免覆盖生产配置。
- `data/` 不同步，避免本地调试数据库污染生产。
- Docker volume 才是生产数据的主要持久化位置。

## 4. 服务器 `.env`

在服务器创建 `.env`：

```bash
ssh "$SSH_TARGET" "cd '$PROJECT_DIR'
if [ ! -f .env ]; then
  cp .env.example .env
fi
chmod 600 .env
"
```

编辑 `.env`，生产域名示例：

```dotenv
PUBLIC_HOST=share.example.com
LETSENCRYPT_EMAIL=admin@example.com
LETSENCRYPT_STAGING=false
COMPOSE_PROJECT_NAME=file-text-share
APP_IMAGE_TAG=YYYYMMDD-gitsha
```

不要在 `.env` 写管理员密码。管理员密码只通过一次性环境变量传给命令。

如果由 agent 自动更新镜像标签，只修改 `APP_IMAGE_TAG`：

```bash
APP_IMAGE_TAG="$(date +%Y%m%d)-$(git rev-parse --short HEAD)"
ssh "$SSH_TARGET" "cd '$PROJECT_DIR'
if grep -q '^APP_IMAGE_TAG=' .env; then
  sed -i 's/^APP_IMAGE_TAG=.*/APP_IMAGE_TAG=$APP_IMAGE_TAG/' .env
else
  printf '\nAPP_IMAGE_TAG=$APP_IMAGE_TAG\n' >> .env
fi
chmod 600 .env
"
```

## 5. 创建或修改管理员账号

本项目支持 `admin` 管理员账号和多个普通用户，无注册页面。先构建 app 镜像，再设置固定管理员账号 `admin` 的密码。

```bash
ssh -t "$SSH_TARGET" "cd '$PROJECT_DIR'
docker compose build app
printf 'APP_ADMIN_PASSWORD: '
stty -echo
read APP_ADMIN_PASSWORD
stty echo
printf '\n'
export APP_ADMIN_PASSWORD
docker compose run --rm --no-deps -e APP_ADMIN_PASSWORD app user set-admin-password
unset APP_ADMIN_PASSWORD
"
```

密码规则：

- 长度 12 到 128 位。
- 必须包含大写字母、小写字母、数字和特殊字符。
- 使用 Argon2id 存储哈希。

普通用户建议登录管理员后台创建，也可用服务器命令创建：

```bash
ssh -t "$SSH_TARGET" "cd '$PROJECT_DIR'
printf 'APP_ADMIN_PASSWORD: '
stty -echo
read APP_ADMIN_PASSWORD
stty echo
printf '\n'
export APP_ADMIN_PASSWORD
docker compose run --rm --no-deps -e APP_ADMIN_PASSWORD app user set-password '<USERNAME>'
unset APP_ADMIN_PASSWORD
"
```

`admin` 是保留用户名，普通用户不能使用。

## 6. 首次签发 HTTPS 证书并上线

确认 `.env` 中 `LETSENCRYPT_STAGING=false` 后运行：

```bash
ssh "$SSH_TARGET" "cd '$PROJECT_DIR' && ./deploy/init-letsencrypt.sh"
```

脚本会做这些事：

1. 构建并启动 `app`。
2. 启动只监听 80 的 `nginx-bootstrap`。
3. 使用 Certbot HTTP-01 从 Let's Encrypt 签发证书。
4. 停止 bootstrap，启动正式 `nginx`。
5. 执行 `nginx -t` 并输出 `docker compose ps`。

不使用自签名证书。

如果证书签发失败：

- 确认 DNS 已解析到该 VPS。
- 确认 80 端口公网可达。
- 确认 Cloudflare 没有强制 HTTPS、访问规则、WAF 或缓存阻断 ACME challenge。
- 查看 Certbot 输出，不要打印私钥。

## 7. 启用自动续期

在服务器项目目录执行：

```bash
ssh "$SSH_TARGET" "cd '$PROJECT_DIR' && ./deploy/install-renewal-timer.sh"
```

该脚本会安装：

- `/etc/systemd/system/file-text-share-cert-renew.service`
- `/etc/systemd/system/file-text-share-cert-renew.timer`

续期 service 以部署用户运行，不以 root 运行。该用户必须能执行 `docker compose`。

验证：

```bash
ssh "$SSH_TARGET" "set -e
systemctl is-enabled file-text-share-cert-renew.timer
systemctl is-active file-text-share-cert-renew.timer
systemctl list-timers file-text-share-cert-renew.timer --no-pager
cd '$PROJECT_DIR'
docker compose run --rm --no-deps certbot renew --dry-run --no-random-sleep-on-renew
"
```

续期成功后，`deploy/renew-cert.sh` 会 reload Nginx。

## 8. 收紧防火墙

启用 UFW 前先确认 SSH 端口。当前示例使用 `$SSH_PORT/tcp`。

```bash
ssh "$SSH_TARGET" "set -e
ufw status verbose || true
ufw limit '$SSH_PORT'/tcp comment 'SSH rate limit'
ufw allow 80/tcp comment 'HTTP redirect and ACME'
ufw allow 443/tcp comment 'HTTPS'
ufw default deny incoming
ufw default allow outgoing
ufw --force enable
ufw status verbose
"
```

若服务器 SSH 使用 22 端口，把 `SSH_PORT` 设置为 `22`。不要在没有确认 SSH 端口时启用 UFW。

Docker 会发布 80/443 到宿主机；本项目没有发布 8080，app 只在 Docker 内部网络对 Nginx 可见。

## 9. 验证上线结果

从本地或任意公网环境验证域名 HTTPS：

```bash
curl --noproxy "*" -fsS -o /dev/null -D - "https://$PUBLIC_HOST/" | sed -n "1,20p"
```

期望看到：

- `HTTP/2 200`
- `content-security-policy`
- `strict-transport-security`
- `x-content-type-options: nosniff`
- `x-frame-options: DENY`

验证 HTTP 只跳转到固定 HTTPS 域名：

```bash
curl --noproxy "*" -sS -o /dev/null -D - "http://$PUBLIC_HOST/" | sed -n "1,10p"
```

期望：

- `HTTP/1.1 308 Permanent Redirect`
- `Location: https://$PUBLIC_HOST/`

验证直接 IP 访问被拒绝：

```bash
curl --noproxy "*" -sS --max-time 5 -o /dev/null -w "ip_http_code=%{http_code} exit=%{exitcode}\n" "http://$SERVER_IP/"
curl --noproxy "*" -k -sS --max-time 5 -o /dev/null -w "ip_https_code=%{http_code} exit=%{exitcode}\n" "https://$SERVER_IP/"
```

期望：

- HTTP：空响应或连接关闭，`http_code=000`。
- HTTPS：TLS 握手失败，常见错误为 `tlsv1 unrecognized name`，`http_code=000`。

验证容器健康：

```bash
ssh "$SSH_TARGET" "set -e
cd '$PROJECT_DIR'
docker compose ps
docker compose exec -T app wget -q -O - http://127.0.0.1:8080/healthz
echo
docker compose exec -T nginx nginx -t
docker inspect -f '{{.Config.Image}}' file-text-share-app-1
"
```

注意：不要从宿主机访问 `127.0.0.1:8080` 作为健康判断。8080 没有发布到宿主机，宿主机 curl 失败是预期行为。

## 10. 后续更新发布

每次本地有新提交后：

```bash
cd /path/to/file-text-share
git status --short --branch
git log -1 --oneline --decorate
go test ./...

APP_IMAGE_TAG="$(date +%Y%m%d)-$(git rev-parse --short HEAD)"
rsync -az --delete \
  --exclude ".git/" \
  --exclude ".env" \
  --exclude "data/" \
  ./ "$SSH_TARGET:$PROJECT_DIR/"

ssh "$SSH_TARGET" "set -e
cd '$PROJECT_DIR'
if grep -q '^APP_IMAGE_TAG=' .env; then
  sed -i 's/^APP_IMAGE_TAG=.*/APP_IMAGE_TAG=$APP_IMAGE_TAG/' .env
else
  printf '\nAPP_IMAGE_TAG=$APP_IMAGE_TAG\n' >> .env
fi
chmod 600 .env
docker compose build app
docker compose up -d app nginx
docker compose ps
docker inspect -f '{{.Config.Image}}' file-text-share-app-1
"
```

更新后再执行第 9 节的验证命令。

## 11. 数据和证书备份

必须备份以下 Docker volume：

- `file-text-share_app-data`：SQLite 数据库和上传文件。
- `file-text-share_letsencrypt`：Let's Encrypt 证书、账号和续期配置。
- `file-text-share_certbot-webroot`：HTTP-01 webroot。

查看 volume：

```bash
ssh "$SSH_TARGET" 'docker volume ls | grep file-text-share'
```

示例备份命令，输出到服务器 `/opt/backups`：

```bash
ssh "$SSH_TARGET" 'set -e
mkdir -p /opt/backups
for v in file-text-share_app-data file-text-share_letsencrypt file-text-share_certbot-webroot; do
  docker run --rm -v "$v:/data:ro" -v /opt/backups:/backup alpine:3.24 \
    tar czf "/backup/$v-$(date +%Y%m%d%H%M%S).tgz" -C /data .
done
ls -lh /opt/backups | tail
'
```

恢复前必须停止服务，并确认目标 volume 名称与 `COMPOSE_PROJECT_NAME` 一致。

## 12. 常见故障处理

### 证书签发失败

检查：

```bash
ssh "$SSH_TARGET" "cd '$PROJECT_DIR'
docker compose --profile bootstrap ps
docker compose --profile bootstrap logs --tail=100 nginx-bootstrap
docker compose run --rm --no-deps certbot certificates || true
"
```

处理方向：

- DNS A/AAAA 是否已经生效。
- 80 端口是否被防火墙或云厂商安全组拦截。
- Cloudflare 是否设置了会影响 ACME path 的跳转或规则。
- `.env` 的 `PUBLIC_HOST` 是否只包含域名，不含协议和路径。

### Nginx 启动失败

检查：

```bash
ssh "$SSH_TARGET" "cd '$PROJECT_DIR'
docker compose run --rm --no-deps nginx nginx -t
docker compose logs --tail=100 nginx
"
```

常见原因：

- 证书还没有签发，`/etc/letsencrypt/live/$PUBLIC_HOST` 不存在。
- `.env` 中 `PUBLIC_HOST` 写错。
- 80/443 被其他进程占用。

### App 不健康

检查：

```bash
ssh "$SSH_TARGET" "cd '$PROJECT_DIR'
docker compose ps
docker compose logs --tail=100 app
docker compose exec -T app wget -q -O - http://127.0.0.1:8080/healthz || true
"
```

常见原因：

- `app-data` volume 权限异常。
- SQLite 文件损坏或磁盘空间不足。
- 新代码构建成功但启动参数或迁移逻辑出错。

### 登录失败

重设管理员密码：

```bash
ssh -t "$SSH_TARGET" "cd '$PROJECT_DIR'
printf 'APP_ADMIN_PASSWORD: '
stty -echo
read APP_ADMIN_PASSWORD
stty echo
printf '\n'
export APP_ADMIN_PASSWORD
docker compose run --rm --no-deps -e APP_ADMIN_PASSWORD app user set-admin-password
unset APP_ADMIN_PASSWORD
"
```

如果要重设普通用户密码，可在管理员后台操作，也可用 `app user set-password '<USERNAME>'`。

不要把密码写进 `.env`。

## 13. 部署完成标准

部署完成必须满足：

- `docker compose ps` 中 `app` 和 `nginx` 均为 healthy。
- `https://$PUBLIC_HOST/` 返回 `HTTP/2 200`。
- `http://$PUBLIC_HOST/` 返回 `308` 并跳转到 `https://$PUBLIC_HOST/`。
- 直接访问服务器 IP 的 HTTP/HTTPS 不返回业务页面。
- `docker compose exec -T nginx nginx -t` 成功。
- `docker compose exec -T app wget -q -O - http://127.0.0.1:8080/healthz` 返回 `ok`。
- `file-text-share-cert-renew.timer` 为 enabled 且 active。
- Certbot renew dry run 成功。
- UFW active，入站只放行 SSH、80、443。
