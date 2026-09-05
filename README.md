# 简易探针 (Simpleprobe)

类似 ServerStatus 的轻量级 Linux 服务器探针系统，专为跨国网络不稳定场景设计。

## 特性

- **Agent 零依赖**：单个静态编译的 Go 二进制文件，无需安装任何运行时或依赖
- **Push 模式**：Agent 主动推送数据到 Server，Server 绝不反连 Agent（不执行命令）
- **Cloudflare 友好**：Server 部署在 Cloudflare DNS 代理后面，解决跨国网络不稳定
- **实时告警**：支持 Gotify 推送，覆盖离线/恢复/CPU 异常/流量异常，CPU 告警包含 Top 3 进程详情
- **凝视模式**：用户浏览 Dashboard 时自动加速更新频率（Agent 30s→10s，Web 10s→5s），离开后自动恢复
- **历史趋势**：带 Chart.js 图表的历史数据仪表盘
- **站点可用性监控**：Server 端周期性探测任意 HTTP/HTTPS 站点（状态码/延迟），Dashboard 展示当前状态与 24 小时可用率，故障/恢复触发 Gotify 告警
- **纯 Go SQLite**：使用 modernc.org/sqlite，无需 CGO，跨平台编译无忧

## 架构

```
Agent (Linux) ──HTTPS POST──> Cloudflare DNS ──> Server (Go) + SQLite
                                                    │
                                                    ├── Dashboard (Web)
                                                    └── Alerter → Gotify
```

## 快速开始

### 1. 部署 Server

```bash
# 下载 server 二进制
wget https://github.com/aaalzk/Simpleprobe/releases/latest/download/simpleprobe-server_linux_amd64.tar.gz
tar xzf simpleprobe-server_linux_amd64.tar.gz

# 编辑配置
cp server.yml.example server.yml
vim server.yml

# 启动
./simpleprobe-server -c server.yml
```

Server 配置示例 (`server.yml`):

```yaml
listen: ":8080"
db_path: "./probe.db"
token: "your-secure-token"

gotify:
  url: "https://gotify.example.com"
  token: "your-gotify-app-token"

alerts:
  offline_seconds: 90
  cpu_threshold: 90
  traffic_rx_mbps: 800
  traffic_tx_mbps: 800
  cooldown_seconds: 300
  tolerance:                     # 连续触发次数才发出告警（各类型独立）
    offline: 3
    cpu: 3
    traffic_rx: 3
    traffic_tx: 3
    site_down: 3
    site_up: 3

gaze:
  enabled: true
  agent_interval: 10
  web_interval: 5
  timeout: 30

history_retention_hours: 72

# 站点可用性监控（可选）：Server 周期性探测这些站点
sites:
  - name: "example"
    url: "https://example.com"
    # interval: 60   # 探测间隔秒，默认 60
    # timeout: 10    # 请求超时秒，默认 10
  - name: "github"
    url: "https://github.com"
```

### 2. 部署 Agent

```bash
# 下载 agent 二进制
wget https://github.com/aaalzk/Simpleprobe/releases/latest/download/simpleprobe-agent_linux_amd64.tar.gz
tar xzf simpleprobe-agent_linux_amd64.tar.gz

# 编辑配置
cp agent.yml.example agent.yml
vim agent.yml

# 启动（推荐使用 systemd 管理）
./simpleprobe-agent -c agent.yml
```

Agent 配置示例 (`agent.yml`):

```yaml
server_url: "https://probe.your-domain.com"
name: "my-server"
token: "your-secure-token"
interval: 30
```

### 3. Cloudflare 配置

将 Server 域名在 Cloudflare DNS 中开启代理（橙色云图标），确保 SSL/TLS 模式为 "Full" 或 "Full (strict)"。

### 4. 访问 Dashboard

浏览器打开 `https://probe.your-domain.com` 即可查看监控面板。

### 升级

使用 systemd 管理时，下载升级脚本后直接运行：

```bash
# 升级 Server 到最新版
wget -O upgrade-server.sh https://raw.githubusercontent.com/aaalzk/Simpleprobe/main/scripts/upgrade-server.sh
sudo sh upgrade-server.sh

# 升级 Agent 到最新版
wget -O upgrade-agent.sh https://raw.githubusercontent.com/aaalzk/Simpleprobe/main/scripts/upgrade-agent.sh
sudo sh upgrade-agent.sh

# 升级到指定版本
sudo sh upgrade-agent.sh v1.1.0
```

## 反向代理

Server 默认监听 `:8080`，可以通过 Nginx 或 Caddy 反向代理来提供 HTTPS 和静态资源缓存。

> **注意**：Server 已内置 Bearer Token 鉴权、速率限制和暴力破解检测，反向代理层无需重复这些安全措施。Nginx/Caddy 只需负责 TLS 终结和反向代理即可。

### 前置准备：让 Server 仅监听本地

如果机器上已有 Nginx/Caddy 处理 HTTPS，建议让 Server 只监听 127.0.0.1，避免端口直接暴露：

```yaml
# server.yml
listen: "127.0.0.1:8080"
```

### Nginx 反向代理

```nginx
# /etc/nginx/sites-available/probe
server {
    listen 80;
    server_name probe.your-domain.com;

    # Cloudflare：让 Nginx 识别真实用户 IP（而非 Cloudflare 出口 IP）
    # CF-Connecting-IP 由 Cloudflare 加密设置，此处声明信任该头
    set_real_ip_from 0.0.0.0/0;
    real_ip_header CF-Connecting-IP;

    # 可选：basic auth 作为额外保护层
    # auth_basic "Simpleprobe";
    # auth_basic_user_file /etc/nginx/.htpasswd;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 长轮询支持（Dashboard 刷新间隔 10s）
        proxy_read_timeout 30s;
    }

    # Agent 上报接口：更大的 body 和超时
    location /api/report {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 15s;
        client_max_body_size 64k;
    }
}
```

启用站点并获取证书：

```bash
sudo ln -s /etc/nginx/sites-available/probe /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d probe.your-domain.com
```

> **关于 IP 识别**：配置 `real_ip_header CF-Connecting-IP` 后，Nginx 的 `$remote_addr` 和传给后端的 `X-Forwarded-For` 都会使用 Cloudflare 提供的真实用户 IP。Server 的速率限制和暴力破解检测因此能正确识别真实 IP，不会误封 Cloudflare 出口 IP。
>
> **关于速率限制**：Server 已内置认证失败速率限制（同 IP 60s 内失败 10 次封禁 5 分钟）和暴力破解告警。如需在 Nginx 层额外限制请求频率（例如防止 CC 攻击），可添加：
>
> ```nginx
> # 全局请求速率限制（可选，Server 已有内置限制）
> limit_req_zone $binary_remote_addr zone=api:10m rate=5r/s;
>
> location /api/ {
>     limit_req zone=api burst=3 nodelay;
>     proxy_pass http://127.0.0.1:8080;
> }
> ```

### Caddy 反向代理

Caddyfile 只需一行，自动申请和续期 TLS 证书：

```caddy
# /etc/caddy/Caddyfile
probe.your-domain.com {
    reverse_proxy 127.0.0.1:8080
}
```

带 basic auth 保护 Dashboard（可选，Server 已有 Token 鉴权）：

```caddy
probe.your-domain.com {
    reverse_proxy 127.0.0.1:8080
    basicauth {
        admin $2a$14$P7L5V8...  # 用 caddy hash-password 生成
    }
}
```

```bash
sudo systemctl reload caddy
```

> **注意**：反向代理配置完成后，Dashboard 仍然通过 Cloudflare DNS 访问。Cloudflare 处理跨国流量加速，Nginx/Caddy 处理本地反向代理和 TLS。

## 使用 systemd 管理 Server

```ini
# /etc/systemd/system/simpleprobe-server.service
[Unit]
Description=Simpleprobe Server
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/simpleprobe-server -c /etc/probe/server.yml
Restart=always
RestartSec=5

# 以下为可选的安全加固，如遇到 203/EXEC 错误可注释掉
# NoNewPrivileges=yes
# ProtectSystem=strict
# ProtectHome=yes
# ReadWritePaths=/var/lib/probe

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now simpleprobe-server
```

## 使用 systemd 管理 Agent

```ini
# /etc/systemd/system/simpleprobe-agent.service
[Unit]
Description=Simpleprobe Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/simpleprobe-agent -c /etc/probe/agent.yml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now simpleprobe-agent
```

## 从源码编译

```bash
# 需要 Go 1.21+
git clone https://github.com/aaalzk/Simpleprobe.git
cd Simpleprobe

# 编译所有平台
make build

# 或单独编译
make build-agent
make build-server
```

## 告警类型

| 类型 | 触发条件 | 冷却 | 容忍度 |
|------|---------|------|--------|
| `offline` | 连续 N 秒无上报 | 可配置 | 默认 3 次 |
| `online` | 重新上线时 | 同 offline | 默认 3 次 |
| `cpu` | CPU > 阈值 | 可配置 | 默认 3 次 |
| `traffic_rx` | 入站流量 > 阈值 | 可配置 | 默认 3 次 |
| `traffic_tx` | 出站流量 > 阈值 | 可配置 | 默认 3 次 |
| `site_down` | 站点探测失败（网络错误/超时/HTTP ≥ 400） | 同 offline | 默认 3 次 |
| `site_up` | 站点恢复可用时 | 同 site_down | 默认 3 次 |

### 告警容忍度

每种告警类型独立配置容忍度（连续触发次数）。例如默认容忍度为 3 时，服务器需要连续 3 次检测都超过 CPU 阈值才会发出告警，避免因瞬间抖动触发误报。当条件恢复正常时，计数器自动重置。

```yaml
alerts:
  tolerance:
    offline: 3       # 离线告警：连续 3 次检测离线才告警
    cpu: 3           # CPU 告警：连续 3 次超阈值才告警
    traffic_rx: 3    # 入站流量告警
    traffic_tx: 3    # 出站流量告警
    site_down: 3     # 站点不可用告警
    site_up: 3       # 站点恢复告警
```

### 站点可用性监控

Server 会按 `sites` 配置对每个站点发起 HTTP(S) GET 请求（等价于
`curl -L -o /dev/null -s -w "%{http_code}" https://example.com`），跟随重定向后：

- 最终状态码 `200–399` → `up`（可用）
- 网络错误、超时、或最终状态码 `≥ 400` → `down`（不可用）
- 站点从 `up` 变 `down` 触发 `site_down` 告警，恢复触发 `site_up` 告警

Dashboard 的「站点可用性」区块展示每个站点的当前状态与 24 小时可用率，探测状态列显示可读信息（如 "正常 — 23ms"、"HTTP 502"、"连接超时"、"DNS 失败" 等）；点击站点可查看 24 小时逐小时可用率图表。

相关 API：

| 端点 | 说明 |
|------|------|
| `GET /api/sites` | 所有配置站点的当前状态 + 24h 可用率统计 |
| `GET /api/sites/{name}/history?hours=24` | 单个站点的探测历史（状态码/延迟/错误） |

## 采集指标

| 指标 | 来源 |
|------|------|
| CPU 使用率 | `/proc/stat` |
| 内存使用 | `/proc/meminfo` |
| 磁盘使用 | `statfs(/)` |
| 网络流量 | `/proc/net/dev` |
| 系统负载 | `/proc/loadavg` |
| 运行时长 | `/proc/uptime` |
| TCP 连接数 | `/proc/net/tcp*` |
| 进程数 | `/proc` 目录 |
| 系统信息 | `/etc/os-release`, `/proc/version` |

## 安全性

### ⚠️ 重要：必须修改默认 Token

**默认 token 为 `change-me`，程序会拒绝启动！** 部署前必须生成一个高强度随机 token。

生成随机 token 的方法：

```bash
# Linux/macOS
openssl rand -hex 32
# 或
cat /dev/urandom | tr -dc 'a-zA-Z0-9' | head -c 32
```

将生成的 token 填入 `server.yml` 和 `agent.yml`，两端必须一致。

### 安全特性

- **全端点鉴权**：所有 API 端点（`/api/report`、`/api/servers`、`/api/history`、`/api/alerts`、`/api/sites`）均需要 Bearer Token 认证
- **恒定时间比较**：Token 比较使用 `crypto/subtle.ConstantTimeCompare`，防止时序攻击
- **速率限制**：同一 IP 在 60 秒内认证失败超过 10 次将被临时封禁 5 分钟
- **暴力破解告警**：检测到暴力破解尝试时，会通过 Gotify 推送安全告警通知
- **认证失败日志**：每次认证失败都会记录 IP 和 User-Agent，便于审计
- **Token 强度校验**：启动时强制要求 token 长度 >= 16 字符，拒绝使用默认 token

### 端口扫描防护

Server 已内置速率限制和暴力破解检测，无需额外配置。如需在反向代理层增加防护：

- 建议在 Cloudflare 上配置 WAF 规则限制 `/api/report` 的访问频率
- 建议为 Dashboard 添加 Cloudflare Access 或 nginx basic auth（作为第二层保护）

## License

MIT