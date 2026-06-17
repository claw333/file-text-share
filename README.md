# 轻递

<div align="center">

**跨设备传文字，传文件，轻一点。**

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?style=flat-square&logo=go&logoColor=white)](go.mod)
[![SQLite](https://img.shields.io/badge/SQLite-WAL-003B57?style=flat-square&logo=sqlite&logoColor=white)](database.go)
[![Docker Compose](https://img.shields.io/badge/Docker%20Compose-ready-2496ED?style=flat-square&logo=docker&logoColor=white)](compose.yaml)
[![HTTPS](https://img.shields.io/badge/HTTPS-Let%27s%20Encrypt-003A70?style=flat-square&logo=letsencrypt&logoColor=white)](DEPLOYMENT.md)
[![Auth](https://img.shields.io/badge/Auth-single%20account-7C3AED?style=flat-square)](security.go)
[![Password](https://img.shields.io/badge/Password-Argon2id-4B5563?style=flat-square)](security.go)

![CSRF](https://img.shields.io/badge/CSRF-protected-brightgreen?style=flat-square)
![Upload](https://img.shields.io/badge/File%20upload-1GB-blue?style=flat-square)
![Text retention](https://img.shields.io/badge/Text%20retention-30%20days-f59e0b?style=flat-square)
![File retention](https://img.shields.io/badge/File%20retention-7%20days-f97316?style=flat-square)
![Telemetry](https://img.shields.io/badge/Telemetry-none-111827?style=flat-square)

</div>

私人跨设备文本与文件共享工具。包含 Go 后端、SQLite、响应式网页，以及 Docker Compose、Nginx 和 Let's Encrypt 生产部署配置。

## 已实现

- 单账号登录，无公开注册入口
- 12–128 位复杂密码与 Argon2id 哈希
- 服务端会话、CSRF 校验和登录限速
- 多条文本共享、复制状态和全部使用历史
- 最大 1 GB 的流式文件上传与下载
- 一次性下载票据，避免大文件进入浏览器 JS 内存
- 文件下载状态和全部下载历史
- 文本 30 天、文件 7 天后自动清理
- 精确到秒的时间和简化设备信息
- SQLite WAL 与服务器本地文件存储

## 本地启动

要求 Go 1.26 或更高版本。

首次创建账号时，在终端安全输入密码：

```bash
cd /path/to/file-text-share
read -s APP_ADMIN_PASSWORD
export APP_ADMIN_PASSWORD
go run . user set-password demo
unset APP_ADMIN_PASSWORD
```

密码必须至少 12 位，并包含大写字母、小写字母、数字和特殊字符。

本地 HTTP 调试需要临时关闭 Cookie 的 `Secure` 属性：

```bash
APP_COOKIE_SECURE=false go run . serve
```

访问 `http://127.0.0.1:8080/`。生产环境必须保持默认值 `APP_COOKIE_SECURE=true`，并仅通过 HTTPS 访问。

## 配置

| 环境变量 | 默认值 | 说明 |
| --- | --- | --- |
| `APP_ADDR` | `127.0.0.1:8080` | 后端监听地址 |
| `APP_DB_PATH` | 用户缓存目录下的 `file-text-share/share.db` | SQLite 数据库位置 |
| `APP_UPLOAD_DIR` | 用户缓存目录下的 `file-text-share/uploads` | 上传文件目录 |
| `APP_COOKIE_SECURE` | `true` | 是否仅通过 HTTPS 发送会话 Cookie |
| `APP_ADMIN_PASSWORD` | 无 | 管理员命令临时读取的密码，不应写入配置文件 |

## 测试

```bash
go test ./...
go vet ./...
node --check app.js
```

## 生产部署

完整的从零部署、域名绑定、HTTPS、UFW、禁止直接 IP 访问、自动续期和更新发布流程见 [DEPLOYMENT.md](DEPLOYMENT.md)。下面保留简版说明。

### 1. 服务器前提

- 64 位 Linux VPS，建议至少 1 GB 内存和足够的持久化磁盘。
- 已安装 Docker Engine 及 `docker compose` 插件。
- 域名的 A/AAAA 记录已经指向服务器公网地址。
- 防火墙允许公网 TCP 80 和 443；应用的 8080 端口不对公网发布。
- 80 端口只处理 Let's Encrypt HTTP-01 校验，其余合法域名请求永久跳转到固定的 HTTPS 域名。
- 直接通过服务器 IP 或其他 Host 访问时，Nginx 会直接断开连接，不提供业务响应。

### 2. 配置域名

```bash
cp .env.example .env
chmod 600 .env
```

编辑 `.env`，至少替换公开访问地址：

```dotenv
PUBLIC_HOST=share.example.com
LETSENCRYPT_EMAIL=admin@example.com
LETSENCRYPT_STAGING=false
COMPOSE_PROJECT_NAME=file-text-share
```

如果没有域名，也可直接填写服务器的独立公网 IPv4 地址：

```dotenv
PUBLIC_HOST=<SERVER_IPV4>
LETSENCRYPT_EMAIL=
```

Let's Encrypt 的 IP 证书有效期约 6 天，Certbot 会保存短期证书配置并由本项目的定时器持续检查续期。推荐填写真实邮箱；留空时会注册无邮箱 ACME 账号。

先以 Let's Encrypt 测试环境验证部署时，使用独立项目名：

```dotenv
LETSENCRYPT_STAGING=true
COMPOSE_PROJECT_NAME=file-text-share-staging
```

测试证书不受浏览器信任。测试结束后运行 `docker compose down`，再将 `.env` 改为 `LETSENCRYPT_STAGING=false` 和 `COMPOSE_PROJECT_NAME=file-text-share`，从而使用全新的正式证书卷。

### 3. 创建正式账号

密码只通过临时环境变量传给管理命令，不写入 `.env`：

```bash
docker compose build app
read -s APP_ADMIN_PASSWORD
export APP_ADMIN_PASSWORD
docker compose run --rm --no-deps -e APP_ADMIN_PASSWORD app user set-password share-admin
unset APP_ADMIN_PASSWORD
```

首次运行时该命令会创建 `app-data` 持久化卷；以后重复执行可修改密码。

### 4. 首次签发证书并上线

```bash
./deploy/init-letsencrypt.sh
```

脚本执行顺序：

1. 构建并启动应用容器。
2. 启动只监听 80 的 ACME 引导容器。
3. 通过 HTTP-01 获取 Let's Encrypt 证书。
4. 停止引导容器，启动正式 Nginx。
5. 校验 Nginx 配置和容器状态。

该流程不会创建或使用自签名证书。

### 5. 启用自动续期

在使用 systemd 的服务器上运行：

```bash
./deploy/install-renewal-timer.sh
```

安装脚本会把 systemd unit 写入 `/etc/systemd/system`，但续期 service 会以运行安装脚本的非 root 部署用户执行；该用户需要能够运行 `docker compose`。如果你用 `sudo ./deploy/install-renewal-timer.sh` 运行，脚本会使用 `SUDO_USER` 作为 service 用户；如需显式指定：

```bash
SERVICE_USER=deploy SERVICE_GROUP=deploy ./deploy/install-renewal-timer.sh
```

定时器每天 03:17 和 15:17 检查证书，加入最多 30 分钟随机延迟；证书续期检查完成后会热重载 Nginx。

检查状态：

```bash
systemctl status file-text-share-cert-renew.timer
systemctl list-timers file-text-share-cert-renew.timer
```

手动测试续期：

```bash
docker compose run --rm --no-deps certbot renew --dry-run --no-random-sleep-on-renew
```

### 6. 更新与运维

更新代码并重新部署应用：

```bash
docker compose up -d --build app
docker compose ps
```

查看不包含正文和文件内容的运行日志：

```bash
docker compose logs --tail=100 app nginx
```

备份时必须同时保留以下三个 Docker 卷：

- `file-text-share_app-data`：SQLite 数据库及上传文件。
- `file-text-share_letsencrypt`：证书和续期配置。
- `file-text-share_certbot-webroot`：ACME HTTP-01 校验目录。

停止服务但保留数据：

```bash
docker compose down
```

不要执行 `docker compose down -v`，该命令会删除数据库、上传文件和证书。

## 数据安全

- 本地默认数据库和上传目录位于用户缓存目录；如果显式配置到项目内，相关目录不应加入版本控制或交给外部 agent。
- 日志不记录密码、会话令牌、共享文本正文或文件内容。
- 文件使用随机磁盘名保存于网页静态目录之外。
- 生产环境必须使用默认的安全 Cookie、Nginx HTTPS 入口及防火墙规则。

## Star History

> 发布到 GitHub 后，把下面的 `OWNER/file-text-share` 替换为实际仓库地址。

<a href="https://star-history.com/#OWNER/file-text-share&Date">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=OWNER/file-text-share&type=Date&theme=dark" />
    <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=OWNER/file-text-share&type=Date" />
    <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=OWNER/file-text-share&type=Date" />
  </picture>
</a>
