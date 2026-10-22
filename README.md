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
    online: 3
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
# 可选：联系方式，会附加到 User-Agent（见下文 Cloudflare 配置）
contact: "https://your-domain.com/agent"
```

`contact` 是可选的联系方式（URL 或邮箱），会拼进 Agent 的 User-Agent：

```
simpleprobe-agent/1.5.0 (https://your-domain.com/agent)
```

可识别、可联系的客户端比匿名客户端宽松得多——这既是 Cloudflare Bot 管理规则的通行做法，也方便你在 Cloudflare 日志里一眼认出哪些流量是自己的探针。

### 3. Cloudflare 配置

将 Server 域名在 Cloudflare DNS 中开启代理（橙色云图标），确保 SSL/TLS 模式为 "Full" 或 "Full (strict)"。

#### 3.1 源站只放行 Cloudflare（强烈建议）

用安全组/防火墙把源站的 80/443 只放行给 Cloudflare 官方 IP 段（[官方列表](https://www.cloudflare.com/ips/)），或者干脆改用 Cloudflare Tunnel。否则攻击者可以直连源站，绕过 Cloudflare 的全部防护，并伪造 `CF-Connecting-IP` / `X-Forwarded-For` 头——Server 的限速与暴力破解检测会因此被绕过。

#### 3.2 给 Agent 上报接口放行安全产品

探针是机器客户端，User-Agent 形如 `simpleprobe-agent/1.5.0 (+https://your-domain.com/agent)`，很容易被 Bot 类规则判成机器人。建议在 Cloudflare 控制台为 `/api/report` 建一条 **Skip** 规则：

| 条件 | 取值 |
|------|------|
| `http.request.uri.path` | `starts_with` `/api/report` |
| `http.user_agent` | `contains` `simpleprobe-agent` |
| 动作 | Skip：安全产品 / 缓存 / 限速 / WAF 托管规则 |

另外几条也要注意：

- **Cache Rule**：`/api/*` 一律 bypass cache。Server 已自带 `Cache-Control: no-store`（双保险）；否则一旦开启 Cache Everything，Dashboard 会读到 120 分钟前的旧数据（Cloudflare 对 200 响应的默认 Edge TTL 就是 120 分钟）。
- **Rate Limiting**：若给 `/api/report` 配了限速，计数窗口设为 **≥ 10 秒**。1 秒窗口会误杀正常上报，触发 Cloudflare 错误 1015。
- **不要对 `/api/report` 开启 API Shield schema validation**：会直接返回 1020，探针全部失败。
- **Under Attack Mode（我在受攻击模式）必须排除 `/api/report`**：挑战页需要浏览器执行 JavaScript，探针过不去，会全线掉线。

Agent 侧已有配套处理：识别 Cloudflare 拦截并把 `cf-ray` 打进日志、失败后退避重试。

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
    # set_real_ip_from 建议只填 Cloudflare 官方 IP 段（https://www.cloudflare.com/ips/）；
    # 填 0.0.0.0/0 等于信任任何来源的该头，仅在源站已限制为 Cloudflare 直连时使用
    set_real_ip_from 0.0.0.0/0;
    real_ip_header CF-Connecting-IP;

    # 可选：basic auth 作为额外保护层
    # auth_basic "Simpleprobe";
    # auth_basic_user_file /etc/nginx/.htpasswd;

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        # 覆盖而不是追加：$proxy_add_x_forwarded_for 会保留客户端自带的
        # X-Forwarded-For，可被伪造来绕过 Server 的限速与暴力破解检测
        proxy_set_header X-Forwarded-For $remote_addr;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 长轮询支持（Dashboard 刷新间隔 15s）
        proxy_read_timeout 30s;
    }

    # Agent 上报接口：更大的 body 和超时
    location /api/report {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $remote_addr;
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

## 实时推送（SSE）

Dashboard 默认通过 `GET /api/stream` 接收实时推送：Server 在收到 Agent 上报的那一刻就把最新状态推给所有打开的面板，客户端不再轮询 `/api/servers`（原来 5s 一次，每标签页约 1.7 万次/天）。

- 推送失败（网络中断、反代不支持流式）时前端会自动退回 30s 轮询，并在 30s 后重连，功能不受影响。
- Server 每 15 秒发一次心跳注释（`: ping`）保活。Cloudflare 到源站的读超时是 125 秒，没有心跳长连接会被 524 掐断。
- 这条长连接本身也算「有人在看面板」的信号（gaze），行为与原来的轮询一致。
- 鉴权方式不变：仍然用 `Authorization: Bearer <token>`。浏览器原生 `EventSource` 不支持自定义请求头，所以前端改用 `fetch` + `ReadableStream` 自行解析 SSE。

反向代理需要为这个路径关掉缓冲与压缩：

```nginx
# SSE：必须关闭缓冲，否则事件会被攒着一起下发
location /api/stream {
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $remote_addr;
    proxy_set_header X-Forwarded-Proto $scheme;

    proxy_buffering off;
    proxy_cache off;
    gzip off;
    proxy_read_timeout 3600s;
    proxy_send_timeout 3600s;
}
```

Caddy 默认就是流式透传，无需额外配置；如果前面还有一层 Nginx，记得同样关掉 `proxy_buffering`。

> Cloudflare 侧不需要额外规则：SSE 走的就是普通的 `/api/*` 路径，注意不要给它建「缓存全部内容」的 Cache Rule（Server 已自带 `Cache-Control: no-cache`）。

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
| `offline` | 连续 N 次扫描（每 15s 一次）仍无上报 | 可配置 | 默认 3 次 |
| `online` | 恢复后连续 N 次上报都在线，且该次离线告警已发出 | 同 offline | 默认 3 次 |
| `cpu` | CPU > 阈值 | 可配置 | 默认 3 次 |
| `traffic_rx` | 入站流量 > 阈值 | 可配置 | 默认 3 次 |
| `traffic_tx` | 出站流量 > 阈值 | 可配置 | 默认 3 次 |
| `site_down` | 连续 N 次探测失败（网络错误/超时/HTTP ≥ 400） | 同 offline | 默认 3 次 |
| `site_up` | 连续 N 次探测成功，且该次不可用告警已发出 | 同 site_down | 默认 3 次 |
| `link_failure` | 绝大多数探针同时失联（≥ `link_failure_ratio`），疑似上报链路故障 | 同 offline | 触发即发一次 |
| `link_recovery` | 上报链路恢复 | 同 offline | 触发即发一次 |

### 告警容忍度

每种告警类型独立配置容忍度（连续触发次数）。例如默认容忍度为 3 时，服务器需要连续 3 次检测都超过 CPU 阈值才会发出告警，避免因瞬间抖动触发误报。当条件恢复正常时，计数器自动重置。

“连续 N 次检测”按各自的采样节奏计数：`offline` / `online` 依据每次上报与每 15s 一次的离线扫描，`cpu` / `traffic_*` 依据每次上报，`site_down` / `site_up` 依据每次站点探测（默认 60s）。一次故障只告警一次；只有状态真正恢复、计数器归零后再次满足条件，才会重新告警。

### 恢复通知的前置条件

`online` / `site_up` 只在**对应的故障告警确实已经发出**时才会通知。Server 在内存里维护一个「尚未恢复的告警集合」（`map[主体+类型]struct{}`，见 `internal/server/alerter.go` 的 `Alerter.activeAlerts`）：只有真正推送出去的 `offline` / `site_down` 才会写入集合，恢复确认后从集合中移除，恢复通知才会发出。

于是「掉线一次又上线一次」这种没达到容忍度、从未发过故障通知的抖动，不会再收到莫名其妙的「已恢复」；一次已通知的故障也只会对应一条恢复通知。集合只存在于内存中（进程重启后为空），重启后没有需要收尾的故障——这与启动时清空连续计数器是同一个道理。

```yaml
alerts:
  tolerance:
    offline: 3       # 离线告警：连续 3 次扫描仍无上报才告警
    online: 3        # 恢复告警：恢复后连续 3 次上报在线才告警
    cpu: 3           # CPU 告警：连续 3 次超阈值才告警
    traffic_rx: 3    # 入站流量告警
    traffic_tx: 3    # 出站流量告警
    site_down: 3     # 站点不可用告警
    site_up: 3       # 站点恢复告警
```

```yaml
alerts:
  # 失联比例超过该值（且已知机器 ≥ 2 台）时，判定为「上报链路故障」
  # （Cloudflare 抖动、网络中断），此时改发一条 link_failure 总告警，
  # 不再逐台发离线告警，避免告警风暴。默认 0.8。
  link_failure_ratio: 0.8
```

### 站点可用性监控

Server 会按 `sites` 配置对每个站点发起 HTTP(S) GET 请求（等价于
`curl -L -o /dev/null -s -w "%{http_code}" https://example.com`），跟随重定向后：

- 最终状态码 `200–399` → `up`（可用）
- 网络错误、超时、或最终状态码 `≥ 400` → `down`（不可用）
- 被 Cloudflare 挑战（`403/503/429` 且带 `cf-mitigated` 或挑战页特征）→ `challenged`（**不算不可用**）：挑战说明的是「我们被拦了」，不是「站点挂了」，因此不触发 `site_down` 告警，也不计入 24 小时可用率
- 站点从 `up` 变 `down` 连续 N 次后触发 `site_down` 告警，恢复后再连续 N 次成功才触发 `site_up` 告警（且仅当该次不可用告警已发出）

Dashboard 的「站点可用性」区块展示每个站点的当前状态与 24 小时可用率，探测状态列显示可读信息（如 "正常 — 23ms"、"HTTP 502"、"连接超时"、"DNS 失败" 等）；点击站点可查看 24 小时逐小时可用率图表。

相关 API：

| 端点 | 说明 |
|------|------|
| `GET /api/sites` | 所有配置站点的当前状态 + 24h 可用率统计 |
| `GET /api/sites/{name}/history?hours=24` | 单个站点的探测历史（状态码/延迟/错误） |

## 历史数据与降采样

监控数据分两层存放，避免 SQLite 随运行时间膨胀：

| 层 | 精度 | 保留时长 | 用途 |
|---|---|---|---|
| `reports` | 原始采样（10–30s 一条） | 2 小时 | 短窗口图表 |
| `reports_1m` | 1 分钟汇总（每分钟最后一个采样值 + 样本数） | `history_retention_hours`（默认 72h） | 长窗口图表 |

`GET /api/history/{name}?hours=N` 按窗口自动选层与精度：

- `N ≤ 2`：原始采样
- `2 < N ≤ 24`：1 分钟汇总
- `N > 24`：5 分钟汇总

于是 24 小时查询从「8640 个点、约 1.9MB」变成「1440 个点、约 300KB」，响应还会在源站 gzip，传输量进一步下降。

上报按 Agent 时间戳去重（`reports` 表上的 `(server_name, agent_ts)` 唯一索引）：网络抖动导致的重试不会在图表上留下重复点。

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

- 建议在 Cloudflare 上配置 WAF 规则限制 `/api/report` 的访问频率（计数窗口 ≥ 10 秒）
- 建议为 Dashboard 添加 Cloudflare Access 或 nginx basic auth（作为第二层保护）
- 探针上报路径请加 Skip 规则，避免被 Bot/WAF 误伤（见上文 3.2 节）

### Agent 侧的网络健壮性

- **失败退避**：上报失败后按 base → 2×base → 4×base 指数退避（上限 5 分钟），并叠加 ±20% 抖动。固定频率的机器式重试正是 Bot 规则和限速最容易盯上的形态。
- **可诊断的错误信息**：遇到 Cloudflare 拦截时，Agent 会区分 1015 / 1020 / 挑战页 / 520 / 522 / 524，并把 `cf-ray` 打进日志——拿这个 ID 去 Cloudflare 的 Security Events 里就能查到是哪条规则拦的。
- **连接复用**：HTTP/2 + keep-alive，上报不再每次重做 TLS 握手。
- **gaze_interval 可配**：面板有人观看时 Agent 会自动提速上报，设成 `0` 即关闭（默认 10 秒；`interval` 仍是常规间隔）。

## License

MIT