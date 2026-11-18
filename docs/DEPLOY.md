# 构建、自建中继与验收

## 架构

```
B 的临时 Chrome ──socks5──▶ B: 127.0.0.1:随机端口 (tunnel.Consumer)
      │  TLS 1.3（密钥来自邀请码中的 secret，中继拿不到）
      ▼
中继：temproom-server 或你的 Cloudflare Worker（/ws/relay/*，只转发密文）
      ▼
A: tunnel.Provider ──▶ 只连 A 选定的 127.0.0.1:代理端口 ──▶ A 的代理节点 ──▶ 网站
```

- **系统影响**：不需要 root 或 TUN，也不改 B 的系统网络，B 的其他应用不受影响。
- **Chrome 只能经隧道出网**：启动参数是 `--proxy-server`、`--host-resolver-rules`（DNS 在远端解析）和 WebRTC 禁止直连 UDP。隧道断开时页面直接失败，不会回落到直连（fail-closed）。
- **邀请码**：格式为 `房间码-secret[@中继地址]`。
  - 房间码交给中继做匹配；secret 只在 A、B 之间复制。
  - A 的 TLS 公钥由 secret 派生，B 据此校验 A。B 再用 secret 对 TLS 会话做 HMAC 证明，A 据此校验 B。
  - 只拿到房间码或令牌的第三方，用不了 A 的代理。
  - `@中继地址` 只在房间建在“非本构建默认中继”上时出现，B 据此自动寻址。
- **令牌**：中继按角色发放令牌。只有 A 能批准；查询状态、关闭房间、建立中继连接都需要令牌。
- **租约**：任一方 45 秒不轮询，中继就关闭房间并切断连接（应对崩溃、强退、断网）。客户端 60 秒联系不上中继，也会在本地撤销。
- **有效期**：未批准的房间 30 分钟后过期；批准后可用 2 小时。

## 构建

```bash
scripts/build.sh
# 产物：bin/temproom-server-linux-{amd64,arm64}、OneExit.app（Intel + Apple Silicon）、OneExit-mac.zip

# 可选：内置一个默认中继（公共服务器）
TEMPROOM_DEFAULT_RELAY=https://relay.example.com scripts/build.sh
```

- 开源构建**没有内置默认中继**。用户必须在 Relay 里填写自己的 Cloudflare Worker，或者在运行时设置环境变量 `TEMPROOM_SERVER_URL`。
- `frontend/dist` 是前端源码。构建脚本会把它同步到 `cmd/temproom-app/dist`（桌面应用实际嵌入的目录），不要直接改后者。
- 需要 Go 1.26+、Xcode 命令行工具；运行时需要 Google Chrome。

## CI/CD & Automated Releases

仓库（[`edison-land/oneexit`](https://github.com/edison-land/oneexit)）内置三条 GitHub Actions 工作流：

- **`.github/workflows/ci.yml`（Push / PR 自动验证）**：每次推送到 `main` 或提交 Pull Request 时，在 macOS 环境校验 `frontend/dist` 与 `cmd/temproom-app/dist` 同步一致性、`worker/src/index.js` 与 `frontend/dist/app.js` 语法、`go vet ./...`、`go test -race -count=1 ./...`，以及 Linux 服务端（`amd64`/`arm64`）和 macOS 桌面端的编译。
- **`.github/workflows/release.yml`（标签自动发版）**：推送版本标签即可自动构建并发布 GitHub Release（也支持 `workflow_dispatch` 手动触发干跑构建）：
  ```bash
  git tag vX.Y.Z && git push origin vX.Y.Z
  ```
  工作流会强制清空 `TEMPROOM_DEFAULT_RELAY` 与 `TEMPROOM_SERVER_URL`（避免混入私有中继地址），运行 `scripts/build.sh` 产出通用架构 `OneExit-mac.zip`、`temproom-server-linux-amd64`、`temproom-server-linux-arm64` 与 `SHA256SUMS.txt`，并自动创建/更新 Release。
- **`.github/workflows/pages.yml`（官网自动部署至 Cloudflare Pages）**：在 `main` 分支 `landing/**` 更新、GitHub Release 发布/编辑或手动触发时运行。因 `*.zip` 被 `.gitignore` 忽略，部署前会自动从最新 GitHub Release 下载 `OneExit-mac.zip` 放入 `landing/`，再通过 Wrangler 发布到 Cloudflare Pages 项目 `oneexit`（保证官网下载直链可用）。
  - **可选 Secrets**：在 GitHub 仓库 **Settings → Secrets and variables → Actions** 中配置 `CLOUDFLARE_API_TOKEN` 和 `CLOUDFLARE_ACCOUNT_ID` 后自动生效；未配置时会输出 Step Summary 指引并以成功状态跳过（不会导致 CI 变红）。

## 中继方式一：Cloudflare Worker（推荐个人使用）

见 [`worker/README.md`](../worker/README.md)。部署后在 OneExit 中操作：Share Network → Relay → My Cloudflare Worker，填入地址并回车，显示绿点 Connected 后才会保存。

- 设置保存在 `~/Library/Application Support/TempRoom/config.json`，字段为 `relay_mode` 和 `custom_worker_url`。
- 每个会话固定使用建房时的中继。中途改设置只影响下一个房间。
- 中国大陆通常无法访问 `*.workers.dev`，需要绑定自定义域名。

## 中继方式二：自建 temproom-server（Linux）

1. 上传 `bin/temproom-server-linux-amd64`（ARM 机器用 `-arm64`），以 systemd 等方式运行。监听端口由环境变量 `PORT` 决定，默认 8080。
2. 前面放 nginx（建议配 HTTPS）。`/ws` 前缀（包含 `/ws/relay/*`）必须做 WebSocket 升级，并放宽超时：
   ```nginx
   location /ws {
       proxy_pass http://127.0.0.1:8080;
       proxy_http_version 1.1;
       proxy_set_header Upgrade $http_upgrade;
       proxy_set_header Connection "upgrade";
       proxy_read_timeout 3600s;
       proxy_send_timeout 3600s;
       proxy_buffering off;
   }
   location / { proxy_pass http://127.0.0.1:8080; }
   ```
3. 检查：`curl https://relay.example.com/health`，返回内容应包含 `"protocol":2`。
4. 客户端设置：构建时用 `TEMPROOM_DEFAULT_RELAY`，或运行时用 `TEMPROOM_SERVER_URL`。

注意：
- 房间只存在内存里，重启服务会结束所有房间。
- 所有浏览流量都经过服务器，速度取决于服务器带宽。
- 用户在界面里填写的自定义地址必须是 https；只有内置默认中继和本机回环地址允许 http。

## 自测

```bash
go test -race ./...                                                      # 单元 + 本地端到端
TEMPROOM_LIVE_PROXY=127.0.0.1:7890 go test -tags live -run Live -v ./internal/app
TEMPROOM_LIVE_SERVER=https://relay.example.com TEMPROOM_LIVE_PROXY=127.0.0.1:7890 \
  go test -tags live -run Live -v ./internal/app                         # 经你的中继
(cd worker && npx wrangler dev) &                                         # 本地 Worker
TEMPROOM_TEST_WORKER=http://127.0.0.1:8787 go test -race -run PrivateRelay ./internal/app
```

live 测试使用真实代理和真实 Chrome（headless，生产参数），验证两件事：经隧道访问时看到的是 A 的出口；关闭隧道后 Chrome 无法加载页面。

## 双 Mac 验收

前置条件：

- A、B 在不同网络。
- A 的代理可用。规则模式下部分网站会走直连，先约定好预期结果。
- 记录三个 IP：A 直连 IP、A 经代理的出口 IP、B 直连 IP。

步骤：

1. 两台 Mac 安装同一个构建，中继 `/health` 返回 protocol 2。
2. A 选择代理 → Create Room → Copy invite，把邀请码发给 B。
3. B 粘贴完整邀请码 → Join；A 弹出 Join Request → Approve。
4. B 显示 `Connected via host`，Browser Exit 显示 A 的代理出口并标注 `(verified)`。
5. B 点 Launch Chrome，检查以下几点：
   - api.ipify.org 显示 A 的代理出口。
   - browserleaks.com/dns 不出现 B 的运营商 DNS。
   - browserleaks.com/webrtc 不出现 B 的公网 IP。
   - B 的普通浏览器仍是 B 自己的 IP。
6. A 关闭代理：B 的临时 Chrome 加载失败，不能出现任何其他 IP；B 显示 Blocked。A 恢复代理后，B 自动回到 verified。
7. A 点 End Session：B 在数秒内回到 “Session Ended”，临时 Chrome 被关闭，`ls $TMPDIR/temproom-chrome-*` 为空。再由 B 发起 End，同样验证一遍。
8. A 强退（`kill -9`）：B 的临时 Chrome 立即无法加载新页面，60 秒内 B 显示会话结束。B 强退后重新打开 OneExit：遗留的 Chrome 和临时目录被清理。
9. 第三台设备只拿到房间码（不含 secret）时无法加入；房间已有 B 时，其他人加入被拒绝。
