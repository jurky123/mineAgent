# MineAgent

Minecraft 服务器的 AI 聊天助手：玩家在游戏聊天里就能提问，它了解服务器的实时情况，也能帮玩家做传送、给物品、执行命令这类操作（需要管理员批准）。

同一后端还接了 QQ 官方机器人：私聊、群里 @它都能聊，能查服务器状态，能写代码跑代码（仅管理员），还能替你向游戏内管理员申请操作 MC。

还可以直接开一个**网页入口**：浏览器里聊天、发文件/图片、收文件/图片，按名字隔离会话，页面可以嵌进别的网页。

## 能做什么

- **聊天问答**：像和群友聊天一样提问，回答会发在服务器聊天里，所有人都能看到
- **服务器状态**：TPS、内存占用、在线人数、服务器版本，卡不卡一问便知
- **在线玩家**：谁在线、各自在哪个世界、延迟多少
- **玩家信息**：某位玩家的坐标、血量、饥饿值、游戏模式
- **世界信息**：现在是白天还是黑夜、什么天气；还有哪些世界、装了哪些插件
- **搬运操作**：把玩家传送到另一个玩家身边、给玩家物品、执行服务器命令
- **记得住上下文**：最近聊过什么它都知道，可以追问；聊得多了会自动压缩记忆，不会忘事
- **多人共用一个助手**：MC 全服玩家共享同一份聊天记忆，回答公开发布；
  QQ 侧按私聊每人、群聊每群隔离记忆，互不串话

## 怎么用

聊天框直接发 `@agent 问题`，或者用命令 `/agent 问题`（Tab 有补全，别名 `/ai`）：

```text
@agent 在线有谁
@agent 服务器卡不卡
@agent Steve 在哪个世界
@agent 现在几点了？下雨吗
@agent 把服务器时间设为白天
@agent 给我 1 个钻石
```

## QQ 用法

- 私聊机器人直接说话就行，不用 @；群里 @机器人再说话
- 查服务器：`在线有谁`、`服务器卡不卡`、`有哪些世界`、`装了什么插件`
- 绑定 MC 身份后才能申请 MC 操作：先发 `绑定 <MC玩家名>`（如 `绑定 Steve`），
  之后 `把我传送到 Alex 身边` 这类请求会转到游戏内管理员审批；
  `解绑` 解除绑定
- 写代码/跑代码（仅管理员）：`在 workspace 写个 hello.py 并跑一下`。
  所有读写和执行都被限制在 `workspace/` 目录内，有超时、输出上限和全程审计。
  执行分三层：危险命令（rm/sudo/ssh/docker 等）直接拒绝；
  装依赖/下载（`$VENV_BIN/pip install` 装进 workspace/.venv、
  curl/wget 从公开 http(s) 下载到 workspace 内）先过静态约束再送 LLM 语义审查，
  审查不通过或审查器不可用则拒绝；普通跑代码命令直接执行

## 高权限操作与审批

传送、给物品、执行命令属于高权限操作，处理流程：

1. MC 侧：助手会先检查**请求者本人**有没有相应权限，没有权限直接拒绝，不会打扰管理员
2. QQ 侧：跳过本人权限检查（QQ 身份不是 MC 玩家），直接转游戏内审批；
   每个 QQ 用户同时只能挂 2 个待审批（`qq.maxPendingPerUser`），防刷审批
3. 有权限/转审批后，向管理员广播一条消息：请求内容 + **[批准]** **[拒绝]** 按钮，点一下就能处理
4. 批准后：MC 侧以请求者本人身份执行（权限由 LuckPerms 决定）；
   QQ 侧以控制台身份执行，且禁掉 `stop/op/ban/luckperms` 等高危命令
5. 所有请求与审批都会记录在案（谁请求、谁批准、执行结果）

## 权限节点

| 权限 | 作用 | 默认 |
|---|---|---|
| `mineagent.approve` | 批准/拒绝高权限操作 | op |
| `mineagent.teleport` | 发起传送请求 | op |
| `mineagent.give` | 发起给物品请求 | op |

命令类操作按 `minecraft.command.<命令名>` 或对应插件自身的权限节点校验。

## 管理命令

| 命令 | 作用 |
|---|---|
| `/agent <问题>`（别名 `/ai`） | 提问，等价于聊天 `@agent` |
| `/mineagent status` | 查看助手连接状态 |
| `/mineagent reconnect` | 手动重连后端 |
| `/mineagent approve\|deny <审批ID>` | 批准/拒绝操作（Tab 可补全待审批 ID，别名 `/ma`） |

## 安装与运行

- 服务端：Paper 26.2 插件 `MineAgent.jar`，放入 `plugins/` 即可
- 后端：同机运行 `mineagent` 服务（默认 `127.0.0.1:8765`），负责对话、审批与记忆存储
- 模型：需要配置一个 OpenAI 兼容的模型接口（如 opencode-go），在 `config.json` 中填写
- 日常运维：`systemctl status|restart mineagent`，日志 `journalctl -u mineagent -f`

## QQ 接入（官方 Bot）

1. 到 [QQ 开放平台](https://q.qq.com) 注册机器人，拿到 `AppID` / `AppSecret`；
   在管理端订阅 `C2C_MESSAGE_CREATE`（单聊）和 `GROUP_AT_MESSAGE_CREATE`（群@）事件
2. `config.json` 里填 `qq.appId` / `qq.appSecret`（或环境变量
   `MINEAGENT_QQ_APPID` / `MINEAGENT_QQ_SECRET`，Secret 建议走环境变量），
   重启 `mineagent`。不填就是禁用 QQ，不影响 MC
3. 看日志确认 `qq channel enabled` + `qq gateway ready`
4. 给机器人发私聊，看日志里的 openid，把自己的 `union_openid`（优先，没有就用
   `user_openid`）填进 `qq.adminOpenIds`，重启——之后你才能用 workspace 写代码/跑代码
5. 正式环境记得在开放平台配 IP 白名单（本机公网 IP），否则连不上 OpenAPI；
   沙箱环境不用配，先拉沙箱群/沙箱号联调

配置项见 `config.example.json`：`qq.apiBase`（默认正式环境）、
`qq.minIntervalMs`（同会话回复最小间隔，防刷屏）、
`qq.maxPendingPerUser`（单用户待审批上限）、`workspace.*`（沙箱根目录与限制）。

## 企业微信接入（自建应用，个人可注册）

个人微信没有官方 bot、Hook 个人号有封号风险，所以微信侧只走
**企业微信自建应用**：个人可注册（无需企业认证），微信扫码关注"微信插件"后
可以直接在个人微信里收发消息，无封号风险。

1. 到 [企业微信](https://work.weixin.qq.com/) 注册（个人选"企业"类型，企业信息可不认证），
   管理员用真实微信扫码
2. 管理后台 →「我的企业」记企业 ID（`corpId`）
3. 「应用管理」→ 创建自建应用 → 记 `AgentId`、点"查看"拿 `Secret`
4. 应用详情 →「接收消息」→「设置 API 接收」：
   - URL 填 `http://<本机公网IP>:80/wecom`（微信只允许 80/443 端口），
     本机公网 IP `43.160.211.42`，记得腾讯云控制台放行 TCP 80；
   - 随机获取 `Token` 和 `EncodingAESKey`（保存时会先请求你的 URL 验证，
     所以要先填好配置把 `mineagent` 跑起来再点保存）
5. `config.json` 填 `wecom.corpId` / `agentId` / `secret` / `token` / `encodingAesKey`，
   重启 `mineagent`；日志出现 `wecom channel enabled` + `wecom callback listening`
6. 应用详情 →「企业可信 IP」把本机公网 IP 配进去（否则发消息会报错）
7. 自己先发一条消息，日志里 `wecom trigger ... author=<userid>` 拿到 userid，
   填进 `wecom.adminUserIds` 重启（之后才能用 workspace）
8. 「我的企业」→「微信插件」→ 设置 Logo 并分享二维码，个人微信扫码关注后，
   在微信里就能直接和应用对话；单聊/应用群聊都支持

配置项：`wecom.port`（回调端口，默认 80）、`wecom.minIntervalMs`、
`wecom.maxPendingPerUser`、`wecom.adminUserIds`。企微回复走主动消息接口
（应用消息有频控：单成员 30 条/分钟，`minIntervalMs` 默认 1.5s 防抖），
不受公众号"48 小时客服消息窗口"限制。

## 网页入口（web）

浏览器直接聊，支持收发**文件/图片**：拖拽、粘贴或点 ＋ 上传文件，
agent 用 `web_file` 把 workspace 里的文件/图片发回来（图片内联显示，其它给下载链接）。
会话按名字隔离（`web:c2c:<名字>`），同一账号多个标签页共享消息（SSE 实时推送）。

1. `config.json` 里 `web.listen` 默认 `0.0.0.0:8766`（空字符串=禁用网页入口）；
   公网访问要在腾讯云控制台放行 TCP 8766（ufw 未启用）
2. 浏览器打开 `http://<公网IP>:8766/`，输入名字进入（**暂无密码**，
   所以强烈建议把 `web.users` 配成允许的名字白名单）
3. 想用 workspace 写代码/跑代码，把名字填进 `web.adminUsers`（`/myid` 可看自己身份）

接口一览（后续嵌入其它网页可用）：

| 接口 | 说明 |
|---|---|
| `POST /api/login` `{"name":"..."}` | 换登录令牌（`users` 白名单非空时校验） |
| `GET /api/history?after=<id>` | 历史消息（含附件引用） |
| `GET /api/events?token=` | SSE 实时推送 |
| `POST /api/upload?name=<文件名>` | 上传单个文件，返回引用 |
| `POST /api/send` `{text, files}` | 发消息给 agent |
| `GET /api/msgfile?m=<msgId>&i=<序号>` | 下载消息附件（只能取自己会话的） |

嵌入说明：页面允许 iframe（`frame-ancestors *`），API 支持跨域带
`Authorization: Bearer <token>`（无 Cookie），`web.allowedOrigins` 可收紧来源。
上传文件存在 `workspace/web-files/<名字>/` 下，单文件默认 20MB（`web.maxUploadMB`），
登录令牌存 `data/webui/tokens.json`（重启不掉线）。
注意：账号只有名字没有密码，等于"知道名字就能进"，别把管理员名字设成别人能猜到的。

## 微信的其它路线（备查）

- **个人微信 ClawBot（官方 iLink 通道）**：代码已实现（`internal/wechat/`，
  `mineagent --wechat-login` 扫码登录后启用，`wechat` 配置段），但一个微信号只能绑一个
  bot 后端，本号已被其它服务占用，暂未启用。
- **公众号（订阅号/服务号）**：注册免费但只能单聊（无群聊）；回复受"48 小时内
  用户互动才能发客服消息"限制，且需要 80/443 回调。适合对外做服务，不适合自己玩。
- **个人号 Hook（WeChatFerry/wxauto）**：能进真实微信群，但必须挂 Windows 微信
  客户端+小号，封号率高（社区统计 WCF 用户几乎无一幸免），不做。
- **iPad/Android 协议**：能当真实好友+进群，但属灰产、要小号+付费跟版本，
  风控触发概率高（社区实测：每天交互 >500 次账号寿命通常 <72 小时），不做。
