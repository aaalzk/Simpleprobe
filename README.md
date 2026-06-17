# 简易探针 (Simpleprobe)

类似 ServerStatus 的轻量级 Linux 服务器探针系统，专为跨国网络不稳定场景设计。

## 特性

- **Agent 零依赖**：单个静态编译的 Go 二进制文件，无需安装任何运行时或依赖
- **Push 模式**：Agent 主动推送数据到 Server，Server 绝不反连 Agent（不执行命令）
- **Cloudflare 友好**：Server 部署在 Cloudflare DNS 代理后面，解决跨国网络不稳定
- **实时告警**：支持 Gotify 推送，覆盖离线/恢复/CPU 异常/流量异常
- **历史趋势**：带 Chart.js 图表的历史数据仪表盘
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
wget https://github.com/aaalzk/Simpleprobe/releases/latest/download/simpleprobe-server-linux-amd64.tar.gz
tar xzf simpleprobe-server-linux-amd64.tar.gz

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

history_retention_hours: 72
```

### 2. 部署 Agent

```bash
# 下载 agent 二进制
wget https://github.com/aaalzk/Simpleprobe/releases/latest/download/simpleprobe-agent-linux-amd64.tar.gz
tar xzf simpleprobe-agent-linux-amd64.tar.gz

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

## 反向代理

Server 默认监听 `:8080`，可以通过 Nginx 或 Caddy 反向代理来提供 HTTPS、访问控制和静态资源缓存。

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

    # 可选：basic auth 保护 Dashboard
    # auth_basic "Simpleprobe";
    # auth_basic_user_file /etc/nginx/.htpasswd;

    # Dashboard 和 API
    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;

        # 长轮询支持（Dashboard 刷新间隔 10s）
        proxy_read_timeout 30s;
    }

    # Agent 上报接口需要更大的 body 和超时
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

启用站点并获取证书（假设用 certbot）：

```bash
sudo ln -s /etc/nginx/sites-available/probe /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx
sudo certbot --nginx -d probe.your-domain.com
```

### Caddy 反向代理

Caddyfile 只需一行，自动申请和续期 TLS 证书：

```caddy
# /etc/caddy/Caddyfile
probe.your-domain.com {
    reverse_proxy 127.0.0.1:8080
}
```

带 basic auth 保护 Dashboard：

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
User=www-data
ExecStart=/usr/local/bin/simpleprobe-server -c /etc/probe/server.yml
Restart=always
RestartSec=5

NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
ReadWritePaths=/var/lib/probe

[Install]
WantedBy=multi-user.target
```

```bash
sudo mkdir -p /var/lib/probe
sudo chown www-data:www-data /var/lib/probe
# 在 server.yml 中设置 db_path: "/var/lib/probe/probe.db"
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

| 类型 | 触发条件 | 冷却 |
|------|---------|------|
| `offline` | 连续 N 秒无上报 | 可配置 |
| `online` | 重新上线时 | 同 offline |
| `cpu` | CPU > 阈值 | 可配置 |
| `traffic_rx` | 入站流量 > 阈值 | 可配置 |
| `traffic_tx` | 出站流量 > 阈值 | 可配置 |

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

- Agent 与 Server 之间通过 Bearer Token 认证
- 建议在 Cloudflare 上配置 WAF 规则限制 `/api/report` 的访问频率
- 建议为 Dashboard 添加 Cloudflare Access 或 nginx basic auth

## License

MIT