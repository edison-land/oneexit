# OneExit Private Relay（Cloudflare Worker）

把 OneExit 的中继换成**你自己 Cloudflare 账户里的 Worker**。你的伙伴（加入方）不需要 Cloudflare 账户，也不用做任何设置：邀请码里已经带着你的 Worker 地址。

[![Deploy to Cloudflare](https://deploy.workers.cloudflare.com/button)](https://deploy.workers.cloudflare.com/?url=https://github.com/edison-land/oneexit/tree/main/worker)

## 30 秒配置

```
① 部署                 ② 复制地址                         ③ 填进 OneExit
Deploy 按钮 / wrangler ─▶ https://oneexit-relay.<你>.workers.dev ─▶ Share Network → Relay
                                                                    ○ Default Relay
                                                                    ● My Cloudflare Worker
                                                                    [ https://…workers.dev ] ⏎
                                                                    ● Connected · 120ms
④ Create Room → Copy invite      →  邀请码形如  ABCD-EF23-XXXXXXXXXXXXXXXXXXXXXXXXXX@oneexit-relay.<你>.workers.dev
⑤ 伙伴粘贴整段邀请码 → Join       →  自动连到你的 Worker，无需任何配置
```

### 方式 A：一键部署（推荐）
点上面的按钮，在 Cloudflare 官方页面登录并确认。OneExit 不会接触你的 Cloudflare 账户、密码或 API Token。

### 方式 B：命令行
```bash
cd worker
npx wrangler login     # 在浏览器里登录 Cloudflare 官方页面
npx wrangler deploy    # 输出 https://oneexit-relay.<子域>.workers.dev
curl https://oneexit-relay.<子域>.workers.dev/health
# {"status":"ok","worker":true,"protocol":2,...}
```

## 中国大陆使用：务必绑定自定义域名

`*.workers.dev` 在中国大陆通常无法直接访问。加入方（B）通常没有代理，连不上就无法加入。解决办法如下：

1. 把一个自己的域名接入 Cloudflare。
2. 打开 Workers → `oneexit-relay` → Settings → Domains & Routes → Add → Custom Domain，例如填 `relay.example.com`。
3. 在 OneExit 里填写 `https://relay.example.com`。

即使绑定了自定义域名，Cloudflare 免费套餐在国内的速度和稳定性也因运营商而异。请先让 B 在浏览器里打开 `https://你的域名/health`，确认能看到 `"status":"ok"`。

## 它保证什么，不保证什么

| 项 | 说明 |
|---|---|
| 浏览内容 | A、B 之间是 TLS 1.3 端到端加密，密钥来自邀请码里的 secret，Worker 拿不到，只转发密文。 |
| 存储 | 不使用 KV、D1、R2，也不调用 Durable Object 的存储接口。每个房间是一个 Durable Object，状态只在内存里。（免费套餐要求声明为 SQLite 类型的 Durable Object，但这里从不写入。） |
| 日志 | 不调用 `console.log`，`wrangler.toml` 关闭了 observability。Cloudflare 平台自身仍会有请求计数等运行指标。 |
| 销毁 | 任一方离开、30 分钟未批准、批准后 2 小时到期，或 45 秒没有心跳，都会关闭所有连接并从内存中删除房间。Durable Object 被回收时，房间也随之消失。 |
| 配对 | 每个房间只允许一个房主和一个加入者，第三人加入返回 409。只有房主能批准；所有连接都需要角色令牌。 |
| Cloudflare 能看到 | 双方的 IP、连接时间和流量大小，看不到网页内容。 |

## 免费套餐额度（个人使用通常够用）

- Workers：每天 10 万次请求。
- Durable Objects：每天 10 万次请求（WebSocket 消息按 20:1 计），以及 13,000 GB·s 的运行时长。
- 所有浏览流量都经过 Worker，看视频会较快消耗额度。超出后当天中继不可用（如果你的构建内置了默认中继，可以切回 Default Relay）。

## 协议

与 `cmd/temproom-server` 完全相同（`protocol: 2`）：

- REST：`/api/v1/rooms/{create,join,approve,leave,status}`
- 提示通道：`/ws`
- 数据中继：`/ws/relay/{provider,connect,accept}`

OneExit 客户端不区分公共中继和 Worker。

本地调试：`npx wrangler dev`（http://127.0.0.1:8787），然后在项目根目录运行：
```bash
TEMPROOM_TEST_WORKER=http://127.0.0.1:8787 go test -race -run PrivateRelay ./internal/app
TEMPROOM_LIVE_SERVER=http://127.0.0.1:8787 TEMPROOM_LIVE_PROXY=127.0.0.1:7890 go test -tags live -run Live ./internal/app
```
