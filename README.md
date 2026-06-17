# SysProbe — 轻量级 Linux 服务器探针

类似 ServerStatus 的服务器监控系统，专为跨国网络不稳定场景设计。

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
wget https://github.com/xxx/probe/releases/latest/download/probe-server-linux-amd64.tar.gz
tar xzf probe-server-linux-amd64.tar.gz

# 编辑配置
cp server.yml.example server.yml
vim server.yml

# 启动
./probe-server -c server.yml
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
wget https://github.com/xxx/probe/releases/latest/download/probe-agent-linux-amd64.tar.gz
tar xzf probe-agent-linux-amd64.tar.gz

# 编辑配置
cp agent.yml.example agent.yml
vim agent.yml

# 启动（推荐使用 systemd 管理）
./probe-agent -c agent.yml
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

## 使用 systemd 管理 Agent

```ini
# /etc/systemd/system/probe-agent.service
[Unit]
Description=SysProbe Agent
After=network.target

[Service]
Type=simple
ExecStart=/usr/local/bin/probe-agent -c /etc/probe/agent.yml
Restart=always
RestartSec=10

[Install]
WantedBy=multi-user.target
```

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now probe-agent
```

## 从源码编译

```bash
# 需要 Go 1.21+
git clone https://github.com/xxx/probe.git
cd probe

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