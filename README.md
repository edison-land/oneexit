<div align="center">

# OneExit（一窗）

**Zero System Intrusion · Zero Login · End-to-End Encrypted · One Disposable Chrome**

Share your Mac's network exit with one person, for one session, inside one isolated Chrome window.

[Live Demo & Download](https://oneexit.pages.dev/) · [中文官网](https://oneexit.pages.dev/zh/) · [Release v2.1.0](https://github.com/edison-land/oneexit/releases/tag/v2.1.0) · [1-Click Cloudflare Worker](https://deploy.workers.cloudflare.com/?url=https://github.com/edison-land/oneexit/tree/main/worker)

[![GitHub stars](https://img.shields.io/github/stars/edison-land/oneexit?style=flat&color=f97316)](https://github.com/edison-land/oneexit/stargazers)
[![License: MIT](https://img.shields.io/badge/license-MIT-3b82f6?style=flat)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8?style=flat&logo=go&logoColor=white)](go.mod)
[![Wails](https://img.shields.io/badge/Desktop-Wails%20v3-dc2626?style=flat)](cmd/temproom-app)
[![Relay](https://img.shields.io/badge/Relay-Cloudflare%20Tunnel%20%2F%20Workers-f38020?style=flat&logo=cloudflare&logoColor=white)](worker)
[![Crypto](https://img.shields.io/badge/E2EE-TLS%201.3%20%C2%B7%20Ed25519%20%C2%B7%20HKDF-10b981?style=flat)](internal/tunnel)
[![Platform](https://img.shields.io/badge/macOS-11%2B%20(arm64%20%2F%20x86__64)-111318?style=flat&logo=apple&logoColor=white)](https://github.com/edison-land/oneexit/releases/tag/v2.1.0)

</div>

OneExit 让你把本机代理（Clash / mihomo 等的 SOCKS5/mixed 端口）的出口，临时借给另一台 Mac 上的伙伴使用。伙伴只能在 OneExit 打开的临时 Chrome 里用；全程端到端加密，你随时可以一键收回。

```
伙伴 B 的临时 Chrome ─▶ B 本机隧道端口 ═══ TLS 1.3 端到端 ═══▶ 中继 ═══▶ A 的 OneExit ─▶ A 的代理 ─▶ 网站
                                         (中继只看到密文)
```

> 状态：**早期版本（v2.1）**。核心链路已有自动化测试，并用真实代理和真实 Chrome 做过本机实测；跨城双 Mac 的系统验收仍在进行中。请先小范围试用。

## 特性

- **只开放一个口子**：A 只把流量转发到自己选定的本地代理端口，本机其他服务一律不可达。
- **只影响一个浏览器**：B 不改系统网络，也不需要管理员权限；只有 OneExit 启动的隔离 Chrome 走隧道。
- **Fail-closed**：隧道或 A 的代理一断，B 的临时 Chrome 就无法上网，不会悄悄回落到 B 自己的网络。DNS 在远端解析，WebRTC 禁止直连 UDP。
- **端到端加密**：邀请码里带着一段 secret，由它派生 TLS 1.3 密钥和身份校验。中继只看到密文，只拿到房间码的人也加入不了。
- **需要批准**：B 加入后，A 要点 Approve 才会开始共享。每个房间只允许一个伙伴。
- **用完即焚**：任一方结束、断线 45 秒，或超出有效期，都会撤销访问，关闭临时 Chrome 并删除它的配置目录。
- **三种中继模式（默认免登录即开即用）**：
  - **Instant Tunnel（默认）**：客户端自动在本地拉起内存 Hub 并创建一次性 `*.trycloudflare.com` 临时隧道，无需注册登录，会话结束即刻销毁。
  - **My Worker（可选）**：自己一键部署到 **Cloudflare Worker**（固定自定义域名，只在内存中运行，不存储、不记日志）。
  - 自建的 **temproom-server**。
  - 伙伴 B 不需要任何账户或配置：邀请码里已经带着中继地址。

## 快速开始

### 1. 直接使用（零配置，免登录）

下载 `OneExit-mac.zip` 解压打开，保持默认的 **Instant Tunnel** 即可直接建房；首次建房若本机未装 `cloudflared`，客户端会自动下载到应用缓存目录。

> **可选进阶（中国大陆直连更稳）**：如果你希望绑定自己的域名长期使用，可一键部署到自己的 Cloudflare Worker 并在客户端切换到 `My Worker`：
>
> [![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/edison-land/oneexit/tree/main/worker)
>
> 说明见 [`worker/README.md`](worker/README.md)。自建服务器见 [`docs/DEPLOY.md`](docs/DEPLOY.md)。

### 2. 构建

需要 macOS 11+、Go 1.26+、Xcode 命令行工具，运行时需要 Google Chrome。

```bash
git clone https://github.com/edison-land/oneexit.git
cd oneexit
scripts/build.sh            # 产出 OneExit.app 和 OneExit-mac.zip（Intel + Apple Silicon）
```

应用只做了 ad-hoc 签名。首次打开时右键 → 打开，或执行 `xattr -dr com.apple.quarantine OneExit.app`。

### 3. 使用

| 房主 A（有代理的一方） | 伙伴 B |
|---|---|
| Share Network → 选择本地代理端口 | |
| Relay 默认选中 `Instant Tunnel`（免登录即用；也可切到 `My Worker`） | |
| Create Room → Copy invite，发给 B | |
| | Join Session → 粘贴完整邀请码 → Join |
| 弹出 Join Request → Approve | |
| | 等到 `Connected via host`，Browser Exit 显示 `(verified)` |
| | Launch Chrome，在这个临时窗口里上网 |
| End Session（B 也可以结束） | 临时 Chrome 自动关闭并清除 |

B 平时用的浏览器和其他应用**不会**经过 A。这是有意的设计。

## 中继能看到什么

| | 公共/自建 temproom-server | 你的 Cloudflare Worker |
|---|---|---|
| 网页内容 | 看不到（端到端 TLS） | 看不到（端到端 TLS） |
| 双方 IP、连接时间、流量大小 | 能看到 | Cloudflare 能看到 |
| 房间状态 | 只在内存中 | 只在内存中（每房间一个 Durable Object，不调用存储接口） |
| 日志 | 取决于服务器配置 | Worker 不打印日志，observability 关闭 |

## 技术栈

| 层级 | 技术选型 | 说明 |
|---|---|---|
| **桌面客户端** | Go 1.26 + Wails v3 + 原生 Web (HTML/CSS/JS) | macOS 11+ Universal（Apple Silicon + Intel），内置中英双语切换 |
| **端到端加密** | TLS 1.3 + Ed25519 + HKDF-SHA256 + RFC 5705 | 从邀请码派生密钥与主机证书公钥固定，中继全程只看到密文 |
| **中继与穿透** | Cloudflare Quick Tunnel (`trycloudflare.com`) / Cloudflare Workers (Durable Objects) | 默认免登录即开即用；可选一键部署私有 Worker（纯内存、零存储、零日志） |
| **浏览器隔离** | Google Chrome 独立 `--user-data-dir` + 远端 DNS + WebRTC UDP 禁用 | 不改系统代理、不装 TUN 虚拟网卡，结束即 `pkill` 并擦除临时目录 |

## 项目结构

| 路径 | 作用 |
|---|---|
| `cmd/temproom-app` | macOS 桌面应用（Wails 3），内嵌前端和本地 API |
| `cmd/temproom-server` | 中继/信令服务器（Go） |
| `cmd/temproom-cli` | 代理探测等诊断工具 |
| `worker/` | Cloudflare Worker 版中继（协议与 temproom-server 相同） |
| `internal/tunnel` | 邀请码、密钥派生、TLS 公钥固定、Provider（A）与 Consumer（B） |
| `internal/server` | 房间、令牌、租约、`/ws/relay/*` 中继 |
| `internal/app` | 会话编排、网络校验、Chrome 门控、撤销 |
| `internal/browser` | 隔离 Chrome 的启动与清理 |
| `internal/config` | 本地设置（`~/Library/Application Support/OneExit/config.json`） |
| `frontend/dist` | 桌面界面源码（构建时同步到 `cmd/temproom-app/dist`） |
| `landing/` | 静态介绍页 |

## 开发与测试

```bash
go test -race ./...                                                    # 单元 + 本地端到端（含 A/B、撤销、错误密钥）
(cd worker && npx wrangler dev) &                                       # 本地运行 Worker
TEMPROOM_TEST_WORKER=http://127.0.0.1:8787 go test -race -run PrivateRelay ./internal/app
TEMPROOM_LIVE_PROXY=127.0.0.1:7890 go test -tags live -run Live -v ./internal/app   # 真实代理 + 真实 Chrome
go test -tags uidev -run UIDev -timeout 0 ./cmd/temproom-app            # 在 :18081/:18082 起两个实例调界面
```

运行时环境变量：

- `TEMPROOM_SERVER_URL`：默认中继地址。
- `TEMPROOM_CONFIG_DIR`：设置目录。

构建时环境变量：

- `TEMPROOM_DEFAULT_RELAY`：内置的默认中继地址。

## 已知限制 / 待支持

| 项 | 现状 |
|---|---|
| 平台 | 只支持 macOS 11+。Windows、Linux 客户端尚未支持。 |
| 浏览器 | 只支持 Google Chrome。Edge、Brave、Firefox、Safari 尚未支持。 |
| 共享范围 | 只有临时 Chrome 走隧道。整机或其他应用（全局模式）不支持，这是有意的设计。 |
| 代理类型 | A 端只支持 SOCKS5 / mixed 端口，纯 HTTP 代理端口不支持。 |
| UDP / QUIC | 不支持，只走 TCP。WebRTC 音视频只能用 TCP，通话质量可能下降。 |
| 伙伴数量 | 每个房间只允许一个伙伴。 |
| 直连 | 所有流量都经中继转发，暂无 P2P 打洞直连，速度受中继带宽限制。 |
| 默认中继 | 开源构建不内置任何共享公共服务器；默认每次会话按需拉起一次性 `*.trycloudflare.com` 临时隧道（也可切换自部署 Worker）。 |
| 中继服务器 | 状态只在内存中，重启会结束所有房间；没有限速、配额和监控。 |
| 信令 | temproom-server 示例使用 HTTP。数据面已端到端加密，但令牌可能在链路上被嗅探，最坏情况是被关房或拒绝服务。建议配置 HTTPS。 |
| Worker | 只在本地 workerd（`wrangler dev`）验证过，尚未在 Cloudflare 线上大规模验证。没有背压，大流量下载可能让 Durable Object 内存积压。免费额度有限。 |
| 签名 | 只有 ad-hoc 签名，没有 Developer ID 签名和公证。 |
| 界面语言 | 支持中英双语（按系统语言自动识别，顶部可一键切换 `中文 / EN`）。 |
| 兼容性 | 带 `@中继` 的邀请码需要 v2.1+；旧版本会提示“邀请码不完整”。 |

## Future work

- [ ] 跨城、跨运营商的双 Mac 系统验收，并公开验收记录（IP、DNS、WebRTC 泄漏检测）。
- [ ] Worker 在 Cloudflare 线上的实测：国内自定义域名的可达性、延迟和吞吐，以及免费额度的消耗模型。
- [ ] 中继背压和流量统计：A 端显示累计流量；中继按房间限速和计量。
- [ ] B 端只在 A 的中继在线时才校验网络，并带重试，减少偶发的校验失败。
- [ ] 多路复用：单条 WebSocket 承载多条流，减少握手和 Worker 请求数。
- [ ] P2P 直连（NAT 打洞），失败时再回落到中继。
- [ ] UDP 支持（SOCKS5 UDP ASSOCIATE），用于 QUIC 和 WebRTC。
- [ ] 更多浏览器（Chromium 系优先）以及 Windows、Linux 客户端。
- [ ] 中继服务器支持 HTTPS 部署模板、持久化或优雅重启，以及 Prometheus 指标。
- [ ] Worker 部署方式二：在客户端输入一个只有 `Workers Scripts: Edit` 权限的 API Token，自动部署（Token 只存在 macOS 钥匙串）。
- [ ] Developer ID 签名、公证和自动更新。
- [ ] 中文界面。

欢迎提 Issue 和 PR。动手改较大功能前，建议先开 Issue 讨论。

## 负责任地使用

OneExit 是帮信任的人临时借用你网络出口的工具。共享出口意味着伙伴的访问会以你的 IP 出现。请只分享给你信任的人，并遵守当地法律以及你的网络或代理服务商的条款。

## License

[MIT](LICENSE)
