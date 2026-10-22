# MineAgent

Minecraft 服务器的 AI 聊天助手：玩家在游戏聊天里就能提问，它了解服务器的实时情况，也能帮玩家做传送、给物品、执行命令这类操作（需要管理员批准）。

同一后端还接了 QQ 官方机器人：私聊、群里 @它都能聊，能查服务器状态，能写代码跑代码（仅管理员），还能替你向游戏内管理员申请操作 MC。

还可以直接开一个**网页入口**：浏览器里聊天、发文件/图片、收文件/图片，按名字隔离会话，页面可以嵌进别的网页。

> **具身接入 V1**：通过独立 Node/Mineflayer 玩家执行 13 个 `body_*` 工具，网页管理员控制，门户 `/body` 提供状态、审批和急停。配置默认关闭；2026-10-20 已按服主授权启动生产试运行（无区域限制，首次连接仍暂停）。
> **持续任务**：身体任务默认12轮、总时长不限，显式maxSteps=0不限轮数，限速时等待配额；`body_status` 独立诊断控制通道，停止/断线/撤权取消旧任务。普通聊天预算不变，详见 body/README.md。
> **建造权限**：Paper 0.3.2 支持 AI 专用 LuckPerms 建造组，以及出生点四周各 512 格、全高度的方块破坏/放置与 WorldEdit 实际写入保护；保护区允许移动和观察，按目标方块拦截写入；须先启用保护再授权批量编辑，详见 body/README.md。
> 使用与验证：[body/README.md](body/README.md)；架构与实施记录：[MINE_BODY_DESIGN.md](MINE_BODY_DESIGN.md)（§18）。
> **下一版导航设计**：[Navigation V2](MINE_BODY_NAVIGATION_DESIGN.md)：目标候选与分段寻路、路线修复，以及受控创造飞行/游泳/精确移动；已交付首轮目标区域解析和显式水面游泳，其余仍为分阶段设计。
> **出城建造实测**：[农场与木屋验收及后续建议](BODY_FARM_FOLLOWUP_2026-10-22.md)：真实非 OP 身体完成 72 株灌溉农场和带床木屋，记录水边、梯子、木门修复及测试边界。
> AI 玩家入口位于 **Minecraft → AI 玩家 → 玩家资料**（`/minecraft` → `/ai` → `/ai/<id>`）。公开资料由独立服务提供；资料内的身体状态、任务/回复、审批和控制由浏览器直接访问主站，仍须主站已验证管理员。`/body` 保留旧书签、游戏 fragment 配对及急停兼容。论坛默认关闭，既有代码与数据保留，不接写作。见 [aiplayer/README.md](aiplayer/README.md) 和 [v1.3 需求 §12](AI_PLAYER_PORTAL_REQUIREMENTS.md#12-用户确认的信息架构调整2026-10-20当前实施依据)。


### 门户与 Agent 界面（2026-10-21）

- 首页保留 Agent、游戏、Minecraft 三项入口和真实最近记录；安装说明集中到 `/minecraft`。`GET /api/portal/public` 仅调用显式注册的 `PublicCard`，公开 Minecraft 可用状态与在线人数，不包含玩家身份、聊天或机器诊断；成功结果短缓存，失败显示未知状态。
- `/api/games` 的 GET 目录允许游客读取；房间详情、创建、加入、落子、历史与控制仍需原有账号鉴权。游客点击创建或加入后原地登录并继续操作，邀请地址保留。
- Agent 分开显示模型和思考强度。`默认/低/中/高` 对应空值、`low/medium/high`；默认不代表关闭思考。模型默认跨提供商按日期排序，可筛选免费与提供商公布的图片、思考、工具能力；未公布能力保持未知。
- 网页个人偏好（人设、回复长度、语言）存于现有 `prompts.json` 的 `userStyle`/`userPersona`，通过 `user-settings` 原子保存，仅影响本人网页请求，不改变渠道规则或工具授权。静态回复示例不调用模型；内置模板只在与已知旧版本完全匹配时升级，保留管理员编辑。
- 分支入口复用 `/fork`，保留来源编号与原会话，创建时保留未发送草稿和附件；长任务状态随实际完成或取消清除，不再按 15 分钟隐藏。附件失败保留并可重试，未确认的 Agent 操作不会自动重发。
- 对局解说默认关闭，开启后在棋盘之外显示；棋盘坐标移出落子区，小屏操作保留悔棋、更多、认输。演示模式统一标识，不能用于判断真实模型或服务器可用性。
- 两个服务的 `tokens.css` 保持一致；原生 ES 模块和 Go 嵌入资源结构不变。没有新增前端运行时或构建依赖。主要控件按 44px 触控区域设计，检查 320/390/768/1280px、深色和减少动态效果。

验收：`go test ./...`、`go vet ./...`、相关包 `-race`；`npm run test:models`、`test:prompts`、`test:community`、`test:review`、`test:conversations`。新增真实隔离流程：`go build -o /tmp/zkun-ui-fixture ./scripts/passkey-fixture` 后运行 `node scripts/portal-refresh-e2e.mjs /tmp/zkun-ui-fixture`，覆盖游客登录后创建/加入、邀请保留、偏好持久化、思考参数、分支草稿保护与附件重试；截图位于 `/tmp/zkun-delivery-ui`。不连接生产配置、真实模型或 QQ。

整合包仍需按发布清单完成 Windows/PCL 实测后才开放下载；未把页面检查视为安装验证。手机软键盘、iOS 真机和真实用户 Web Vitals 需另行验证。


## 能做什么

- **聊天问答**：像和群友聊天一样提问，回答会发在服务器聊天里，所有人都能看到
- **服务器状态**：TPS、内存占用、在线人数、服务器版本，卡不卡一问便知
- **在线玩家**：谁在线、各自在哪个世界、延迟多少
- **玩家信息**：某位玩家的坐标、血量、饥饿值、游戏模式
- **世界信息**：现在是白天还是黑夜、什么天气；还有哪些世界、装了哪些插件
- **搬运操作**：把玩家传送到另一个玩家身边、给玩家物品、执行服务器命令
- **记得住上下文**：最近聊过什么它都知道，可以追问；聊得多了会自动压缩记忆，不会忘事
- **联网搜索**：`web_search` 搜索、`web_fetch` 抓正文（只允许公网、限速限量，返回内容当不可信资料）
- **定时提醒**：`remind` 到点通过原渠道发消息（例：`10 分钟后提醒我喝水`）；`/usage` 查模型额度
- **多人共用一个助手**：MC 全服玩家共享同一份聊天记忆，回答公开发布；
  QQ 侧按私聊每人、群聊每群隔离记忆，互不串话
- **富文本渲染**：网页端支持 Markdown（标题/列表/任务清单/引用/表格/链接/删除线/图片）、
  **LaTeX 公式**（`$...$` 行内、`$$...$$` 块级，KaTeX）与**代码块语法高亮**
  （highlight.js，带语言标签和复制按钮）；两个库自托管在 `static/vendor/`、按需懒加载
- **看图**：网页端上传的图片会作为视觉输入直接给模型（模型本身能看图），
  不用 PIL"猜图"；**队列**：默认全通道 FIFO、一次一个任务（`agent.maxConcurrentRuns=1`），
  同会话严格按序，状态/取消直接处理；显式调大才允许不同会话并行

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

## 游戏内界面（Paper 插件 0.3.2）

输入 **`/agent`** 打开原版助手面板，`/ai` 也可用。`/agent ui`、`/mineagent ui` 或 `/ma ui` 自动选择 MineUI 增强界面或原版回退。原有 `/agent <问题>` 和聊天 `@agent` 保持可用。

- **无需客户端 mod**：直接使用 Paper 原版对话框，提供文本输入、菜单、近期记录与审批；`/agent dialog` 可强制使用。
- **默认入口与回退**：0.2.1 的无参数 `/agent` 直接走 `/agent dialog` 的原版路径。自动模式确认客户端支持 MineUI 后才关闭原版对话框；原版与旧版 MineUI 客户端直接打开原版面板。
- **可选 MineUI 增强**：通过 `/agent ui` 等自动模式入口打开，安装 MineUI 服务端且玩家的 MineUI 客户端 ≥0.16.1 且已握手 `server_ui` 能力时使用 MineUI 页面。只通过公开 `com.mineui.api` 接入，无需新增 MineAgent 客户端 mod；能力不支持或适配失败会回到原版对话框。
- **功能**：提问、服务器/玩家/时间天气查询、共享记忆概况/摘要、模型额度、身份查询、近期回复、待审批详情与批准/拒绝、管理员重连；权限不变，按钮点击时再次校验。
- **消息与状态**：成功提交时显示 ActionBar 回执，回复仍遵循后端 `minecraft.replyMode`；MineUI 页面自动更新，原版对话框点击刷新。MC 会话仍是全服共享，不因打开面板变成私聊。
- **记录**：只在内存保存本人本次在线实际收到的最近 30 条助手回复/面板提问，单条最多 1600 字符，离线即清理；定向回复不会进入其他玩家的记录。不是后端持久会话列表。
- **审批**：仅对 `mineagent.approve` 开放原有 Minecraft 操作审批；核对请求详情后才批准。失效、已处理、断线前的审批不能通过旧按钮再次提交。UI 提交不代表动作已完成，以后端回复为准。
- **AI 玩家**：主页始终有「AI 玩家」，接入已有身体状态、私密任务/回复、动作审批、暂停/恢复/断开和急停。首次从面板获取 2 分钟一次性代码，在网站 `/body` 登录并核对当前游戏连接后授权，最长 10 分钟；断线、撤销权限或网页登录退出都会撤权。需要 `mineagent.body`（默认 OP）和真实网页管理员两层授权；不以离线 MC 名字或 OP 替代认证。详见 [身体面板使用说明](body/README.md#游戏内-ai-玩家面板paper-030)。
- **网页入口**：设置 `ui.portal-url` 后显示网页助手；身体授权优先使用后端配置的网站地址。论坛、网页文件/模型设置仍由门户承载。身体未启用时面板明确显示尚未启用，不自动启动执行器或修改生产区域策略。

权限 `mineagent.use` 默认允许，控制界面及 `/agent` 提问；`mineagent.admin` / `mineagent.approve` 分别控制重连和原有 MC 操作审批。新增 `mineagent.body` 默认 OP，仅允许发起身体管理员关联，实际控制每次复核已验证网页身份与当前游戏连接。普通玩家只看身体基础状态，完整状态与控制按钮须授权后显示。

插件私有 `config.yml` 可新增（未配置沿用默认值，无须为开发验证改线上配置）：

```yaml
ui:
  mode: auto       # auto 或 dialog；dialog 完全使用原版对话框
  portal-url: ""  # 可选 HTTPS 门户根地址；不含账号、令牌、查询参数或 fragment
```

构建使用 Java 25 / Paper 26.2。首次编译需先在相邻 mineUI 源码中运行 `./gradlew :mineui-paper:publishToMavenLocal` 提供 0.16.1 公共 API；该依赖只用于编译，**不打入 MineAgent jar**。随后：

```sh
cd paper-plugin
./gradlew --no-daemon -Dorg.gradle.jvmargs=-Xmx384m -Dorg.gradle.parallel=false test jar
```

产物为 `paper-plugin/build/libs/MineAgent-0.3.2.jar`。身体面板须配套本版后端；仅构建不会安装插件或重启 Paper，正式更新须按项目部署流程核查在线人数及特殊任务；无特殊任务时先发布全服维护通告，再部署重启，无须逐次确认。

隔离集成测试：安装 `body/` 锁定依赖后执行 `node scripts/paper-ui-e2e.mjs <已标记的测试 Paper 目录> <ViaProxy-3.4.14.jar>`。它只允许回环 25665、测试世界 `m0-world`，且目录必须包含 `.mineagent-body-test` 隔离标记；会覆盖测试插件配置并分两阶段启动自有 Paper（不装/安装 MineUI），请勿指向正式目录。测试覆盖原版按钮/表单回调、私密回复隔离、审批防重放/冒用/撤权/过期/重连，以及 MineUI 握手、输入、状态更新和旧会话拒绝；这是服务端及协议验证，客户端视觉布局仍需游戏内人工体验。

## QQ 用法

- 私聊机器人直接说话就行，不用 @；群里 @机器人再说话
- 可以直接发送图片、文件、视频，或引用原消息后提问。引用原文和附件只在 QQ 实际下发、或当前会话短期缓存命中时可读取；合并转发卡片若只有预览，请改发聊天记录导出文件、原文或截图。
- 查服务器：`在线有谁`、`服务器卡不卡`、`有哪些世界`、`装了什么插件`
- 绑定 MC 身份后才能申请 MC 操作：先发 `绑定 <MC玩家名>`（如 `绑定 Steve`），
  之后 `把我传送到 Alex 身边` 这类请求会转到游戏内管理员审批；
  `解绑` 解除绑定
- 写代码/跑代码（仅管理员）：`在 workspace 写个 hello.py 并跑一下`。
  文件工具的路径限制在 `workspace/` 内；执行使用 Linux **Bubblewrap** 隔离：
  仅 workspace 可持久写入，系统运行时只读，宿主配置/其它用户文件不可见。
  需要安装 `bwrap`、util-linux 的 `setpriv` 并允许非特权用户命名空间；缺失或启动失败时拒绝执行，不回退到宿主 shell。
  启动前仅清除子进程继承的 Linux capabilities 并设置 no-new-privs，网站监听端口的权限不变。
  有超时、输出上限和全程审计；输出在收集时限制内存，超时会终止整组进程与沙箱子进程。
  执行分三层：危险命令（rm/sudo/ssh/docker 等）直接拒绝；
  装依赖/下载（`$VENV_BIN/pip install` 装进 workspace/.venv、
  curl/wget 从公开 http(s) 下载到 workspace 内）先过静态约束再送 LLM 语义审查，
  审查不通过或审查器不可用则拒绝；普通跑代码命令在无网络沙箱内执行，
  只有下载/装包命令通过审查后才开放网络。命令静态检查是额外护栏，不作为隔离边界。
  各通道只认自己的管理员名单（QQ / 企微应用 / 智能机器人 / 个人微信 / 网页），同名不能跨通道提权。

## 对话命令（QQ 与网页）

这些命令由系统直接处理，不占 Agent 队列、不调用模型；回答进行中仍可使用：

- `/help`：列出当前渠道所有系统命令。
- `/new`：进入空白新对话，旧消息和摘要保留。
- `/fork`：复制当前已保存的消息与摘要，进入独立分支；这是命令时刻的快照，不同步原对话后来产生的回复，也不复制执行中的工具、审批或提醒。
- `/resume`：列出会话及稳定编号；`/resume <编号>` 恢复；`/resume default` 恢复旧默认对话；`/resume page <页码>` 每页查看 20 个。
- `/effort`：查看当前会话的思考强度；`/effort low|medium|high|default` 设置或恢复默认，也接受 `低/中/高/默认`。QQ、网页均由系统直接处理，不进模型队列；只影响后续新请求，已提交和运行中的任务不变。QQ 群内作用于当前共享对话，私聊/群/网页会话相互隔离，设置保存在数据库，重启不丢失。`/new` 不继承覆盖值，`/fork` 继承，`/resume` 恢复原会话值。网页滑杆与此命令共用会话设置；未覆盖时仍沿用原账号偏好或站点默认。

QQ 私聊按用户、群聊按群保存当前选择，重启后仍有效，无法恢复其他私聊或群的会话；群内命令切换当前群共享的对话。QQ `/tasks` 和 `/stop` 仍可管理当前聊天范围中本人在旧对话的任务，管理员权限沿用既有规则。旧任务不会因切换自动中止，晚到的纯文本回复会标注原会话编号。

网页会话沿用已验证账号与现有侧栏，命令返回后同步切换页面与历史；旧任务可切回原会话停止。系统命令反馈可见但不作为模型上下文；`/new` 与 `/memory clear` 不同，后者会删除当前历史。Fork 保留上传文件的历史引用，不重新授予已经过期的 QQ 附件工具或执行权限。

## 共享 Agent Skills

`agent.skillsDir`（或环境变量 `MINEAGENT_SKILLS_DIR`）指向一份由维护者管理的共享目录，使用绝对路径；留空不启用。推荐使用用户级 `.agents/skills`，可与 DSH 等读取相同目录的客户端共用，Codex 可通过其用户技能目录的符号链接引用同一份文件。网页 `web.skills` 仍是快捷提示词菜单，与这里的标准技能包不同。

目录格式为 `<共享目录>/<技能名>/SKILL.md`，文件带 `name` 与 `description` YAML frontmatter，名称与目录相同；脚本、参考文档、素材保留在包内。所有 MineAgent 通道按需发现共享技能：只向模型提供名称和有上限的简介，调用 `skill` 才读取正文或资源；资源支持分页，拒绝路径和符号链接越界。新增技能在下一次任务发现，无须给每个会话安装副本。

共享目录只读挂在执行沙箱的 `/opt/mineagent-skills`，脚本可直接引用共用库，结果写到当前 workspace；只需修改个别示例时复制对应代码，不能在共享目录构建产物。沿用原有管理员权限、网络审查与操作审批，安装技能不自动执行代码、不授权上传发布、不开放普通用户的代码执行。需要浏览器检查的技能可复用现有 Playwright Chromium 安装，只挂载该浏览器运行时并设置 `CHROME_PATH`，不暴露用户整个缓存目录。

可用 `python3 scripts/share-agent-skills.py` 预览现有 Codex 用户技能的归并，再加 `--apply` 把原目录移动到 `~/.agents/skills` 并留下 Codex 链接；它保留整个技能包，冲突即停止，不移动 `.system` 或插件托管包、不下载或执行技能。DSH 的 user-agents provider 默认读取此目录；自定义 `agentsHome` 或 `DSH_AGENTS_HOME` 时应指向其父目录。MineAgent 的 `skillsDir` 指向其中的 `skills` 目录；以后直接把新技能装到共用目录，重跑脚本可补齐 Codex 链接。目录统一不等于客户端工具能力完全相同，技能中的 CLI 专属操作仍须检查当前工具是否可用。

[Anatomy](https://github.com/wheresryan22/anatomy) 是交互式 SVG 技术示意图技能，需要 Node.js 20.10+，浏览器验收需要 Chrome/Chromium。安装包与依赖由维护者统一管理，不加入本项目仓库。可要求 Agent“用 Anatomy 制作……示意图”；读取技能后按任务生成 HTML/SVG，QQ 可通过文件能力交付。本项目包含可选的 `MINEAGENT_TEST_SKILLS_DIR=<共享目录> go test ./internal/tools -run TestInstalledAnatomyBuildAndCapture`，验证共享 kit 构建、只读挂载和沙箱截图。

## 可选 DSH 执行后端

`agent.backend` 默认 `eino`，可切换为 `dsh`，把任务交给现有 DSH Web 宿主里的本地桥接插件。会话身份、FIFO、停止、QQ 附件与发送、共享技能、沙箱和 Minecraft / 身体审批仍在 MineAgent 校验；自定义人设保留并补充实际运行环境说明，普通聊天 DSH 默认 high 思考，身体任务优先使用身体预算/思考配置（官方 DeepSeek 将身体 medium 映射到 low）。未映射的显式模型选择继续走原提供商，已开始的 DSH 任务失败不会自动换引擎重跑。

配置、路由、历史范围、取消行为、安装与隔离验收见 [桥接说明](dsh-bridge/README.md)。只改配置前先确认宿主适配器与默认模型显示名称一致；本项目不自动安装或重启 DSH，也不承诺移除 Go 进程或显著减少其常驻内存。

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
| `/agent` / `/agent ui`（别名 `/ai`） | 打开游戏内助手面板；`/agent dialog` 强制原版界面 |
| `/agent <问题>` | 提问，等价于聊天 `@agent` |
| `/mineagent ui`（别名 `/ma ui`） | 打开助手面板 |
| `/mineagent status` | 查看助手连接状态 |
| `/mineagent reconnect` | 手动重连后端 |
| `/mineagent approve\|deny <审批ID>` | 批准/拒绝操作（Tab 可补全待审批 ID，别名 `/ma`） |

## 安装与运行

- 服务端：Paper 26.2 插件 `MineAgent.jar`，放入 `plugins/` 即可
- 后端：同机运行 `mineagent` 服务（默认 `127.0.0.1:8765`），负责对话、审批与记忆存储
- 模型：需要配置一个 OpenAI 兼容的模型接口（如 opencode-go），在 `config.json` 中填写
- 日常运维：`systemctl status mineagent`，日志 `journalctl -u mineagent -f`；更新与重启走 `scripts/deploy.sh`，避免清空进行中的棋局。

### 验证与部署

验证：`go test ./...`、`go vet ./...`、`python3 scripts/deploy_test.py`。
Bubblewrap 集成测试需要 Linux、`bwrap` 和用户命名空间；不支持的测试环境会显示跳过。
部署脚本测试只在临时目录运行模拟命令，不操作真实服务。
社区与认证回归：`npm run test:community`、`npm run test:passkeys`（临时数据库和 Chrome 虚拟认证器，不读取生产配置）。
模型列表回归：`npm run test:models`（Playwright + 临时静态服务/模拟 API，不访问线上配置或数据）。

`scripts/deploy.sh` 先把二进制构建到临时目录，再查询 `/api/games/active`。
仅 HTTP 成功、响应包含有效的 `playing: 0` 时才替换 `bin/mineagent` 并重启；
有对局、令牌缺失/过期、网络错误、响应格式无效都会拒绝部署。
管理员登录令牌保存到 `<web.dataDir>/deploy-token.txt`（0600，可用 `TOKEN_FILE` 覆盖）；
令牌不会出现在 curl 参数中。`--force` 显式跳过对局检查，会清空内存房间。
构建时可用 `make build BUILD_OUTPUT=/tmp/mineagent-check` 验证而不覆盖运行二进制。
`config.json` 与 `config.json.bak-*` 属于私有运行配置，不提交到 Git。

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

## 网页入口 / Mine 门户（web）

浏览器打开 `http://<公网IP>:8766/` 就是 **Mine 门户**（默认）：

| 页面 | UI 模式 | 说明 |
|---|---|---|
| `/` | Friends Hub | 公共首页：欢迎区、世界图片区、Agent/游戏/Minecraft 三个入口、共享发布信息；真实登录后显示继续对话与实时详情 |
| `/minecraft` | Guide | PCL 安装、升级备份和故障处理；发布记录不完整时显示整合包准备中 |
| `/agent` | Productivity App | Agent 聊天页（ChatGPT 风全屏工作区：多会话/文件收发/模型切换，见下） |
| `/games` → `/games/chess` | Entertainment App | 游戏目录 → 国际象棋（大厅 → 房间 → 对局 三态） |
| `/screens` | Control App | **显示屏**（MineDisplay）：世界里的浏览器屏列表/开屏/换网址/关屏 + 导航白名单（开关/增删/一键加常用站）；写操作限管理员。服务端代理到 MineDisplay 本地控制 API（`web.displayApi` + `web.displayTokenFile`，插件自动生成令牌） |
| `/account` | Settings | 设置页：账号 / 外观 / 安全 / 设备 / 游戏 / 关于；人设和提示词在 `/agent` 内调整 |

**顺手就能用**：`Ctrl/Cmd + K` 打开命令面板（新建对话 / 打开棋局 / 对局历史 / 切换主题…）；
支持安装成 PWA（`manifest.webmanifest` + 纯网络 Service Worker，手机"添加到主屏幕"后全屏运行）；
手机端底部有 Tabbar（首页/Agent/游戏/Minecraft/我的）；Agent 工作区保留顶栏和自己的输入布局。

**图标与品牌**：线性图标用 Lucide（ISC，内联在 `static/js/ds.js` 的 `ICON_PATHS`，34 个），
品牌 mark / favicon 用站点图标 `static/img/logo.png`（256px）与 `static/img/favicon.png`（64px，
`/favicon.ico` 也指向它），棋子是 Cburnett SVG（CC BY-SA 3.0，见 `static/img/pieces/NOTICE.txt`）；
`/static/img/NOTICE.txt` 汇总了素材来源与许可。

**交互与视觉**：浅灰页面 + 白色 surface + 极细边框 + 品牌蓝（`--brand: #0a84ff`）；
组件遵循圆角体系（控件 10px / CTA 12px / 卡片 16px / 弹层 14-22px，只有 Tag 是胶囊）；
公开的交互原语在 `static/css/ds.css` + `static/js/ds.js`（Button/IconButton/Card/Dialog/
ConfirmDialog/Toast/Field/Avatar/Badge/EmptyState/Skeleton），**页面里没有 alert/prompt/confirm**；
顶栏导航只放核心入口（首页 / Agent / 游戏 / Minecraft），显示屏收进账户菜单，主题是明确的选择器（跟随系统 / 浅色 / 深色），
未登录时是整页登录态（共享首次设置密码/密码登录表单，另提供 Passkey 入口）。

`web.portal=false` 可一键回滚成"`/` 就是聊天页"的旧版单页模式。

### 账号体系（首次设置密码）

- 按站主确认的迁移方式：未设置密码、也未绑定 Passkey 的账号，下次进入时输入名字并创建密码；原账号 ID、对话和战绩保留。首次成功设置者完成该账号认领。
- 密码须为 12–128 个字符，必须重复确认；使用随机盐与 Argon2id 保存哈希，不保存明文。设置后不可凭名字重设密码，后续登录必须验证密码。
- 首次设置会撤销该账号的旧免密码会话；其他旧令牌也不能访问新认证接口。Passkey 仍作为可选登录方式，已绑定 Passkey 的账号不能被名字认领流程绕过。
- 忘记密码由管理员核验后使用本机 `--auth-reset-password USER` 撤销密码与会话，再由本人重新设置。管理员权限只看服务端 `web.adminUsers`。
- 管理员密码登录成功后，自动以 0600 文件原子更新部署检查令牌，不输出令牌。
- HTTPS 来源使用 `web.domain`（或显式 `web.passkeyOrigins`）；开发测试仅使用 localhost。
- 认证和部署记录见 [社区改造与认证迁移记录](MINE_COMMUNITY_IMPLEMENTATION.md)。

### 门户扩展点（加功能不用改骨架）

新增门户功能 = 在 `cmd/mineagent/portal_apps.go` 里 `portal.Register` 一个应用
（`internal/portal/apps.go` 的 `App`：ID/名称/路径/排序/是否管理员可见/`Card` 函数），
门户首页会自动出现导航与卡片；卡片数据由 `GET /api/portal/home` 服务端逐个聚合，
单个应用失败只显示"暂不可用"，不拖垮整页。卡片类型：`stat`（数值/键值对）、
`list`（列表，如公告栏）、`link`、`text`；`enabled:false` 的应用显示"开发中"占位。

- **公告栏**：管理员可在门户卡片上直接发布/删除（`GET /api/portal/announcements`
  公开读，`POST`/`DELETE` 仅管理员）
- **MC 状态卡**：经网关调 `minecraft_server_status`，显示在线/TPS/内存/版本
- **Agent 卡片**：显示会话数，点击进 `/agent`

### 文件分发（`/downloads` 管理，`/download#…` 领取）

管理员访问 `/downloads`，上传非空文件（最大 256 MiB）后复制独立随机链接；接收者无需账号或身份验证。
此应用不展示在首页或公共导航。链接持有者均可下载，转发不受阻止；不用于证明接收者身份。
上传与下载 API 在启用域名 HTTPS 后拒绝明文 HTTP（包括 8766 端口）。

- 上传文件保存在 `web.dataDir/private-downloads/`，目录 0700、文件 0600，不经静态目录或公开文件服务器提供。生产环境应继续保持 dufs 停用，禁止另行公开此目录。
- 分享秘密由 32 字节安全随机数生成，放在 URL fragment 中，仅通过 POST 表单提交；数据库只存 SHA256 校验值。原链接仅生成时显示，刷新后不可找回；“生成新链接”会立即撤销旧链接。文件名、大小与 SHA256 只向持有有效链接的人返回。
- 记录保存在 SQLite 新增的 `download_files` / `download_events` 表；管理、记录查询、生成及停用链接均需服务端管理员权限。统计页展示全部累计次数与最近 200 条时间、连接 IP、字节数、传输状态。
- **次数口径**：每个有效的文件领取 POST 请求计一次；页面访问、元数据、GET/HEAD、无效/停用链接、缺失文件不计数。重复下载分别计数。不支持断点续传，每次从头传输。中断占一次请求，单独标记；“传输完成”表示服务器已发送全部字节，不能证明文件已保存。进程在传输期间退出会留下“未确认结束”记录。
- 直接下载记录 TCP 连接来源，不信任公开请求的 `X-Forwarded-For` / `CF-Connecting-IP` 等头。配置签名中转后，Worker 以 HMAC-SHA256 签名访客地址、时间、方法、路径与领取秘密摘要，源站只在 HTTPS 且签名有效、时间差不超过 120 秒时使用该地址；伪造、篡改或过期签名在计数前拒绝。现有 IPv6 Xray 透传可能使未签名连接显示为内网地址；代理、NAT 或共享网络意味着访客出口 IP 不能代表人数或真实身份。
- 停用链接阻止后续领取，保留文件与记录；已开始的传输及已下载文件无法收回。备份时应一致备份 SQLite，并同时备份此文件目录。

验证：`go test -p 1 ./...`、`npm run test:downloads`（真实 Go handler + 临时 SQLite/上传目录，测试文件不进入生产数据；截图在 `/tmp/zkun-download-ui/`）。构建输出到临时路径，部署仍用 `scripts/deploy.sh` 检查在线对局。

**可选：Cloudflare Worker 下载入口**

没有额外域名时，可使用账号的 `workers.dev` 地址。打开 Cloudflare 控制台的 **Workers & Pages → 你的 Worker → Edit code（编辑代码）**，把示例代码完整替换为 [cloudflare-download-worker.mjs](scripts/cloudflare-download-worker.mjs)，配置签名 Secret 后点击 Deploy（部署）。无需 KV、R2 或修改主站 DNS。缺少或错误的签名配置会明确拒绝文件下载，不再静默退回记录 Cloudflare 回源 IP；文件信息与当日密钥接口不受此检查影响。

主站 `/downloads` 仍用于管理员上传和统计。在私有配置中设置 `web.downloadPublicOrigin` 为 Worker 的 HTTPS 域名地址（仅 origin，不带路径、查询或片段），按部署流程生效后，上传及“生成新链接”都会自动返回 Worker 的完整分享链接；留空则沿用本站地址。域名只写私有配置，不硬编码在公共源码中。此设置不重置文件、次数或既有领取密钥；手头的旧链接也可仅替换域名，保留 `/download#` 及其后完整秘密。单独打开 Worker 地址应出现通用下载页并提示链接不完整；这是正常状态，不会展示文件清单。页面、脚本和表单只访问 Worker 域名，文件由 Worker 流式转发；不重定向回主站，不暴露管理员入口，不缓存文件。

记录访客出口 IP 时，生成独立的随机 32 字节十六进制签名密钥，写入源站私有配置 `web.downloadProxySecret`，按部署流程生效；在 Worker 设置同值的 Secret `DOWNLOAD_PROXY_SECRET`，再部署新脚本。也可用 `python3 scripts/build_download_worker.py --config /private/config.json --output /private/worker/deploy.mjs` 生成带固定签名配置的完整私有副本，副本中的配置优先于旧的 Worker Secret；文件只能输出到仓库外的 0700 私有目录，文件 0600，不得提交到 Git 或公开分发。部署后访问 Worker 的 `/api/downloads/status`，必须返回当前 `version` 和 `signedIP: true`；该状态不暴露签名密钥、IP 或源站地址。响应头也包含版本标识，用于区分预览、旧版本与正式发布。Worker 只读取 Cloudflare 的 `CF-Connecting-IP`，不沿用浏览器提交的转发/签名头；签名密钥不进入网页、下载内容或响应。尚未更新的老 Worker 不带签名时，源站仍记录连接来源，不能冒充访客；既有连接 IP 记录无法还原。访客开代理时记录的是其代理出口。Cloudflare 作为中转方能接触请求和文件，下载统计的“完成”也只表示源站完成向中转发送。此入口降低普通下载者看到主域名的机会，不承诺无法通过文件内容或其他线索关联。

Worker 代码测试：`node --test scripts/cloudflare-download-worker.test.mjs`。Worker 默认子域名的国内可达性需接收者实测；上游不可用时返回通用错误，不回传源站地址。此脚本须由账号持有人在 Cloudflare 控制台部署，本地文件存在不代表远端 Worker 已更新。

### TextCrypt 当日文本工具

Windows 客户端源码单独保存在工作区 `textcrypt/`，通过 `POST /api/textcrypt/key` 领取当天参数。只传批次 ID 和程序授权头，不上传原文/密文。以服务端北京时间判断日期，密钥为每批次每日独立的随机 32 字节值，客户端不能指定历史日期。每次计算重新联网取钥，客户端只在内存使用，不做离线缓存；内存转储、换页或修改程序仍可能留存密钥，不承诺不可提取。

- 私有密钥库：`web.dataDir/textcrypt-vault/vault.json`，目录 0700、文件 0600；凭证只存 SHA256，历史日密钥按站主要求保留。不得公开此目录、记录响应内容或提交到 Git。
- 管理入口 `/textcrypt-admin`：仅管理员列出批次、日期、停用批次、手动清除某日密钥，不展示密钥。清除当天密钥后当日不再自动重建；清除当前库不意味着清除备份、快照或物理残留。密钥库完整备份属于敏感备份。
- 操作员创建批次：`POST /api/textcrypt/admin`（管理员凭证，JSON label），仅创建时返回程序凭证；它应直接写入 0600 的本地构建配置，不输出、不放进命令参数。批次停用会拒绝后续领取，不能收回已保存的密钥。
- Worker 脚本同时支持 `/api/textcrypt/key`，不转发管理接口、不缓存密钥，不在页面或响应里包含主站地址；账号持有人需更新 Worker 一次。Worker 能接触密钥，是方案信任的一部分。
- 此方案是客户端当日限制；服务端保存历史密钥，所以没有针对服务端密钥库泄露的历史保密保证。网站不会处理消息内容，也不能保证设备已失陷时的密钥安全。

测试包含北京时间零点切换、并发请求同日一致、历史保留、清除后不重建、批次停用、来源/权限和 HTTPS 限制。上线走既有对局检查脚本，部署前同时备份密钥库（若已有）。

### 小游戏（`/games`：国际象棋、五子棋）

`/games` 是游戏目录（每张卡显示开放房间数与最近战绩），点进各自游戏页：
**国际象棋 `/games/chess`**、**五子棋 `/games/gomoku`**。两个游戏共用同一套房间机制
（创建房间 / 输房间码加入 / 开放房间列表 / 访客邀请链接 `/games/<id>?room=ABC123` 自动加入），
房间与大厅 UI 在 `static/js/gameroom.js` 里共用，服务端是通用棋室（`internal/games`）+ 各游戏规则包。

- **在线房间对战**：房主执白，加入者执黑；服务端权威裁判（走法校验、将杀/逼和判定），
  SSE 实时同步，刷新/断线重连不丢对局；同时只在一个房间里（开新房或离开视为旧局认输）。
- **大厅 / 对局**：`/games/chess` 自己管三态——没房间时是大厅（创建房间 / 输入房间码加入 /
  开放房间列表），进房间后是棋盘页（等待对手 → 对局 → 终局）。
- **音效**：落子/吃子/轮到你/将军/胜负，全部用 Web Audio 合成（零素材）；账号页可关（`游戏音效`）。
- **提和**：对局中点「提和」发出请求（和悔棋/换边同一套），对手同意即判和棋；走出新的一步会让请求作废。
- **AI 弹幕（解说）**：每一步落下后，一条 AI 点评像直播间弹幕一样飘过棋盘（多条自动分轨、不叠字；
  操作行有「弹幕开/关」）。语言风格约束成 20 字以内的直播弹幕（会玩梗、不骂人、不复述数据）。
  喂给模型的是**完整对局进程**：
  刚刚谁用什么子从哪走到哪、吃掉了什么、是否将军/将杀；最近 16 手进程；**双方剩余子力**（象棋按
  后/车/象/马/兵计数）；当前棋盘（象棋 ASCII + FEN，五子棋带行列的 15×15 盘面）以及之前几手的解说
  （保持叙事连贯）。每手只生成一次、双方共享；没配模型时自动隐藏；
  终局把解说写进 `game_runs.metadata`，回放页也能看。
- **桌面通知**：账号页可开启（需浏览器授权）；页面在后台时提醒「轮到你 / 将军 / 对局结束」。
- **终局呈现**：棋盘上浮出「胜利/惜败/和棋 · 将杀/五连」+ [再来一局][查看棋谱]，胜利时少量粒子；棋盘几何保持不变。
- **悔棋（需双方同意）**：点「悔棋」发出请求，对手在状态栏看到「同意 / 拒绝」；
  同意则撤销你最近一手（及其后的对手回应，回到你重走），拒绝或 2 分钟超时则继续；
  走出新的一步、认输或终局都会让请求自动作废。
- **棋盘交互**：内置 Cburnett SVG 棋子（不依赖系统字体）、边线坐标、点击棋子 →
  服务端下发合法落点高亮 → 点击走子；上一手/被将军/选中/可吃子都有标记，
  黑白视角自动翻转；着法用标准代数记谱（SAN：`e4` `Nf3` `O-O` `exd5` `e8=Q+` `Qh4#`）
  以编号两列表格展示；房间码点击复制，另有"复制邀请链接"。
- 手机端第一屏是「对手 → 棋盘 → 自己」，房间信息和着法在下方。
- **规则范围（v1 最简可用）**：全部棋子走法/吃子、将军/将杀/逼和、王车易位、升变
  （默认升后，可指定 q/r/b/n）；**暂不支持吃过路兵、50 回合/三次重复和棋**（后续再补）。
- **房间在内存里**：`mineagent` 重启会清空未结束的房间（约 30 分钟无人的等待房间、
  2 小时无动作的对局会自动清理）。
- **五子棋（`/games/gomoku`）**：15 路棋盘、黑先、横竖斜连成 5 子或以上即胜（自由规则，
  不做禁手/三三）；棋盘是简约白底发丝网格 + 圆子，最后一手有描边、获胜连线高亮；
  棋盘满了算和棋。悔棋规则与象棋一致（两个游戏共用 `gameroom.js` 的请求/同意交互）。
- **记录**：每局结束给双方各写一条 `game_runs`（胜/负/和 + 原因/颜色/回合数），
  房间锁内捕获不可变记录、解锁后由终局请求同步写库；数据库繁忙不会阻塞其它房间，
  该请求返回前完成写入尝试，失败记录警告日志（不自动重试）。
  只在"我的最近战绩"里展示；积分与排行榜模型仍未定，等游戏类型稳定后再设计。
- **加新游戏**：`internal/games/<id>` 写规则 → `internal/games/match_<id>.go` 适配
  （Turn/Play/Snapshot）→ 在 `internal/games/games.go` 的注册表加一条 →
  前端复制一份游戏页（用 `gameroom.js` 复用大厅/房间/SSE）。

| 接口 | 说明 |
|---|---|
| `GET /api/games` | 游戏注册表 |
| `GET/POST /api/games/<id>/rooms` | 开放房间列表 / 创建房间 |
| `POST /api/games/<id>/rooms/join` `{room}` | 加入房间 |
| `GET /api/games/<id>/room` | 我当前房间状态（含轮到我时的走法提示） |
| `POST /api/games/<id>/move` | 走子：象棋 `{from,to,promotion?}`，五子棋 `{point:"h8"}` |
| `POST /api/games/<id>/undo` `{action}` | 悔棋：`request`/`accept`/`decline`/`cancel`（需双方同意） |
| `POST /api/games/<id>/draw` `{action}` | 提和：`request`/`accept`/`decline`/`cancel`（同意即和棋） |
| `POST /api/games/<id>/resign`、`/leave` | 认输 / 离开 |
| `GET /api/games/<id>/runs` | 我的最近战绩 |
| `POST /api/games/<id>/comment` | 请求"最后一手"的 AI 解说（每手只生成一次，双方共享） |
| `GET /api/games/events?token=` | SSE 房间事件（`room` / `comment`） |

### 聊天页（`/agent`）

支持**多会话**和收发**文件/图片**：拖拽、粘贴或点 ＋ 上传文件（图片先在浏览器端
压缩到 1600px/JPEG，带上传进度），agent 用 `web_file` 把 workspace 里的文件/图片
发回来。图片点击进查看器（缩放/拖动/多图切换/下载），文件按类型显示角标卡片、
点击或悬停下载，下载文件名保留原始名。

会话生命周期是 ChatGPT 那套语义：点「新聊天」只进入**前端草稿态**（不建库、
重复点无副作用、侧栏不出现），发出**第一条消息时才由 `/api/send` 原子创建**会话，
首条消息自动命名；侧栏按 今天/昨天/更早 分组、可搜索、可重命名/删除；
`GET /api/conversations` 是纯读（不写库、不改排序）。旧版单会话自动兼容为
「默认会话」（`conv=""`）。

界面为 ChatGPT 风格（浅色/深色/跟随系统 + 强调色，侧栏可折叠）：空会话时标题 +
输入框居中，发出第一条消息后输入框动画落到底部；输入框是两层结构（多行文本 +
底部工具条）：
- **＋ 附件**：上传文件/图片，或选 `web.skills` 里的快捷指令（点一下填充 prompt）
- **思考强度**：保留蓝白滑杆的连续拖动、松手吸附，以及最高档紫粉渐变、白色粒子与滑钮光晕。
  `默认 / 低 / 中 / 高` 对应 `reasoning_effort` 的 `''/low/medium/high`；默认沿用站点设置。
  刻度按钮支持键盘方向键、Home/End，保存失败恢复原档位，系统减少动态效果时关闭动效。
  **模型**由独立按钮打开列表（默认从网关 `GET /models` 拉，10 分钟缓存，也可用
  `model.options` 限定）；每个账号各选各的，存 `data/webui/prefs.json`
  模型列表默认隐藏未配置密钥、探测失败、明确不支持工具/文本输出或已下线的条目；可打开「显示不可用」查看原因。
  支持搜索、最新/免费/名称排序、「只看免费」及手动刷新。最新排序采用提供商 `created` 日期；
  未提供日期的模型排在有日期的后面并按名称排序，不推测发布日期。
  免费标记优先依据提供商定价（输入/输出及已公布的请求、图片、缓存等费用必须为零）；
  未提供定价时仅识别 `-free`、`:free` 和 `openrouter/free`，Contributor 不代表免费。
  免费也可能需要 API key、受限额影响或仅限活动期，实时定价/可用性以提供商为准。
  默认网关保留 1-token 后台探测；成功显示「已验证」，404/503 暂时标不可用，20 分钟后会重新探测，
  不再永久跳过失败模型。额外提供商仅根据目录元数据判断，不批量生成付费请求。
  探测携带一个不执行的工具定义，明确返回不支持对话协议/工具的 400/422 也标为不可用；
  鉴权、限流和普通参数错误不会被误判为模型下线。探测通过仍不保证账户额度、附件或思考参数可用。
  目录请求失败时保留上次成功缓存；读取列表不再清空账号已选模型。
  OpenRouter 配置位于 `config.json` 的 `model.providers`，找到 `id: "openrouter"` 后填写其 `apiKey`；
  `freeOnly: true` 仅列免费模型，设为 `false` 可列全部并使用界面的免费筛选。修改配置后按对局保护部署流程重启生效。
  未填写 key 时可查看公开目录，但不能选择调用，面板显示未配置提示；密钥不会发送给浏览器。
  目录支持嵌套定价元数据（如 `pricing.overrides`），分时覆盖计费或未知费用不会标为永久免费。
- **Workspace / 外观 / 用量 / 退出登录**：收进侧栏底部的账户菜单
  （Workspace 仅 `web.adminUsers` 可见；用量弹窗有滚动/周/月三条进度条）
- **Agent 设置**（`/agent` 顶栏齿轮、侧栏或账户菜单）：三个分区——「我的人设」提供卡片选择与正文预览；
  「渠道设置」提供渠道选择、默认人设、内置/自定义规则与大文本编辑；「模板库」可新建、编辑、恢复或删除共享模板。
  普通用户只看到本人人设，渠道与模板管理仅管理员可见。支持手机与深色模式、字数提示、保存状态和未保存更改确认。
  系统提示词是**能力层**——每个服务（网页 / QQ /
  企微 / 企微机器人 / 个人微信 / Minecraft）一套"能力与规则"，管理员可完全替换内置版本；
  **人设**是**角色层**——可增删改的模板（内置：默认助手 / MC 老玩家 / 简洁客服 / 技术搭档 /
  毒舌损友 / 中英翻译 / 严谨老师），管理员可为每个服务选一条，网页用户还能给自己选一条。
  最终提示词 = 人设正文 + 系统提示词。渠道的人设与规则一次保存，写盘失败时不改变生效配置。
  改动从下一条消息起生效，存 `data/webui/prompts.json`。七个内置模板已补充表达方式、任务深度与边界；
  未编辑的旧版内置模板自动升级，已编辑模板和渠道覆盖保持原样。
  浏览器交互回归：`npm run test:prompts`（Playwright，临时本地 API，不接触线上配置与数据；截图默认在 `/tmp/mineagent-prompts-ui`）。
  纯样式预览：`/agent?ui=1&panel=prompts`，使用固定假数据，不读取或保存真实提示词。

交互细节：输入框打 `/` 弹出命令面板（↑↓ 选择、Tab/Enter 补全）；消息列表往上滚
可「载入更早消息」；模型/思考切换、上传失败等用 toast 提示；SSE 断线时顶部重连提示；
消息悬停可复制（代码块单独可复制）；侧栏桌面端可收起（记忆在本地）。

调样式的时候可以用**渲染调试模式**：`/?ui=1` 不连后端、用假数据渲染完整界面
（门户/账号页同样支持），可选 `&panel=intel|appearance|confirm|rename`、`&theme=dark`、
`&empty=1`、`&sidebar=0`，配合无头浏览器截图做视觉对比。

### 配置与接口

0. **访问入口**：`https://zkun.art/`（Let's Encrypt 证书由内置 autocert 自动签发/续期，
   首次握手按需申请，缓存在 `<web.dataDir>/certs`）或 `http://zkun.art/`
   （80 端口与企微回调共用——`/wecom` 仍走回调，其它路径转发给门户；80 同时服务 ACME 验证），
   也可以走 `http://<公网IP>:8766/`。启用 HTTPS 只需在 `web.domain` 填域名。
   ⚠️ 公网要能连：控制台防火墙需放行 TCP 80/443（来源 `0.0.0.0/0` 与 `::/0`）；
   DNS 只有 A 记录时，IPv4 的 443 必须放行。
1. **数据位置**：运行数据不在代码目录里——本机放在 `/home/ubuntu/mineagent-data/`
   （`mineagent.db` / `webui/` / `workspace/`，由 `config.json` 的 `storage.path`、
   `web.dataDir`、`workspace.root` 绝对路径指定；`scripts/deploy.sh` 的令牌也从这里读）。
2. `config.json` 里 `web.listen` 默认 `0.0.0.0:8766`（空字符串=禁用网页入口）；
   公网访问要在腾讯云控制台放行 TCP 8766（ufw 未启用）
3. 公共首页可直接浏览；登录和设置密码使用正式 HTTPS 域名；裸 HTTP/IP 不作为密码登录入口。
4. 想用 workspace 写代码/跑代码，把名字填进 `web.adminUsers`（`/myid` 可看自己身份）

接口一览（`/api/auth/*`、`/api/portal/*` 是门户新路径；旧路径保留为别名）：

| 接口 | 说明 |
|---|---|
| `POST /api/auth/login`、`POST /api/login` | 旧名字登录已停用，返回 410 |
| `POST /api/auth/password/status`、`/setup`、`/login` | 查询是否需设密码、首次设置、验证密码登录 |
| `POST /api/auth/passkey/login/begin`、`/finish` | Passkey 挑战与签名登录 |
| `POST /api/auth/passkey/register/begin`、`/finish` | 一次性激活码/恢复码验证与 Passkey 绑定 |
| `GET /api/auth/me` | 当前用户（旧路径 `/api/me`） |
| `POST /api/auth/logout` | 注销当前 token（旧路径 `/api/logout`） |
| `GET /api/account/sessions` | 已登录设备列表 |
| `POST /api/account/sessions/revoke` `{id}` 或 `{others:true}` | 踢设备 |
| `GET /api/portal/apps` | 应用注册表（导航数据源） |
| `GET /api/portal/home` | 首页聚合（欢迎语 + 各应用卡片） |
| `GET/POST/DELETE /api/portal/announcements` | 公告栏（读公开，写仅管理员） |
| `GET /api/portal/presence` | 现在有哪些人开着网页（Presence） |
| `GET /api/history?conv=&after=<id>` | 历史消息（含附件引用） |
| `POST /api/clear` | 清空当前会话（前端"新会话"），并广播其它标签页 |
| `GET /api/options` | 技能 / 可选模型 / 思考强度 / 当前偏好，可带 `conv` 读取本人会话覆盖值 |
| `GET /api/options?refresh=1` | 刷新提供商模型目录（10 秒内重复刷新复用缓存） |
| `GET /api/conversations` | 会话列表（纯读；旧单会话会合成返回） |
| `POST /api/conversations/delete`、`/rename` | 删除 / 重命名会话 |
| `POST /api/prefs` `{model, effort, conv?}` | 保存模型与思考强度；带 `conv` 时仅覆盖本人该会话的思考强度，不带时保留原账号偏好接口 |
| `POST /api/stop` | 停止本人指定会话的任务；已完成的外部操作不撤销 |
| `GET /api/conversations?q=关键词` | 标题和正文搜索，返回命中片段与消息 ID |
| `POST /api/conversations/settings` | 本人会话置顶、归档（支持取消） |
| `GET/POST /api/prompts` | 人设与提示词：登录用户可预览模板正文，管理员可看渠道规则；POST `scope=service-settings`（一起保存渠道人设与规则）、`capability`、`service-persona`、`persona-save`/`persona-delete`（以上仅管理员）、`user-persona`（本人网页人设） |
| `GET /api/usage` | 模型 key 额度（滚动/周/月，网关 `/usage`，60s 缓存） |
| `GET /api/workspace?path=` | 列 workspace 目录（仅管理员） |
| `GET /api/workspace/file?path=` | 下载/预览 workspace 文件（仅管理员） |
| `GET /api/events?token=` | SSE 实时推送 |
| `POST /api/upload?name=<文件名>` | 上传单个文件，返回引用 |
| `POST /api/send` `{conv?, text, files}` | 发消息；**不带 conv = 新会话草稿**（服务端原子创建并返回 `conv`） |
| `GET /api/msgfile?m=<msgId>&i=<序号>` | 下载消息附件（只能取自己会话的） |

嵌入说明：页面允许 iframe（`frame-ancestors *`），API 支持跨域带
`Authorization: Bearer <token>`（无 Cookie），`web.allowedOrigins` 可收紧来源。
上传文件存在 `workspace/web-files/u<用户ID>/` 下，单文件默认 20MB（`web.maxUploadMB`）。

## UI 预览 / 视觉回归（开发工具）

`?ui=1` 不是"随手假数据"，而是固定的 **UI Preview Mode**：每个页面都能用 URL 指定状态，
方便截图对比、做视觉回归。常用：

```
/?ui=1                          /games?ui=1                     /account?ui=1
/games/chess?ui=1&state=lobby|waiting|playing|check|finished-win|finished-lose|undo-request|swap-request
/games/gomoku?ui=1&state=lobby|waiting|playing|finished-win|undo-request
/games/chess?ui=motion          # 自动演示一遍（大厅→等待→开局→落子→悔棋→终局），给录屏用
```

一键生成截图 + 动效视频（Playwright，仅开发依赖，不参与服务端构建/部署）：

```bash
npm install && npx playwright install chromium   # 一次性
npm run ui-preview                               # 默认打 http://127.0.0.1:8877
node scripts/ui-preview.mjs --base https://zkun.art --only shots
```

产出 `ui-preview/`：`desktop/`、`mobile/` 固定尺寸截图，`states/` 关键状态，
`motion/full-flow.webm`（Portal→Games→棋局全流程）与 `motion/gomoku-flow.webm`。
改完 CSS 重跑一遍即可前后对比。

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

### 第二轮体验修复（2026-10-17）

发送时立即快照文本和附件并清空本次输入，等待期间的新草稿不被请求结果覆盖；未确认发送提供恢复草稿入口，不自动重发。当前会话运行期间保留草稿并禁止再次提交，支持服务端停止、刷新后恢复运行状态、工具记录与审批等待提示。

会话支持正文搜索和命中定位、重命名、置顶及归档。首轮回复后按标题/主题片段生成简短标题，显式改名优先；首页显示主题、更新时间和最近回复摘要。Markdown 使用本地 markdown-it 15.0.3（MIT），保留列表编号和嵌套、独立滚动表格、代码高亮与原文复制，禁用原始 HTML。

游戏大厅的演示回放使用 `/account/history?demo=gomoku`，保留真实账户与导航，演示战绩单独标注；支持播放、暂停与速度。显示屏普通用户仅可读取状态；管理员操作、白名单确认与服务端授权分别生效，成功白名单变更写审计日志。

回归：`npm run test:review`、`npm run test:community`、`npm run test:models`、`npm run test:prompts`；Go 用 `go test -p 1 ./...`，并对 agent/storage/webui/portal 运行竞态检测。部署继续使用 `scripts/deploy.sh`。真实素材、整合包发布及 Windows/PCL 端到端验证仍须实际完成后才能标为可用。

### QQ Agent 与连续任务（2026-10-21）

- 入站支持 `attachments`、type 103 的嵌套 `msg_elements`，以及 `message_reference` / `message_scene.ext` 的引用编号。当前指令与引用资料分开；引用内的 `/stop` 等文本不会执行系统命令。缓存按私聊/群聊隔离，最多 256 个索引、保留 1 小时，重启即清空；内联引用内容优先，不凭引用编号抓取任意群历史。
- 图片通过模型视觉输入发送；视频用 FFmpeg 在无网络沙箱中抽取最多 4 个均匀分布的关键帧，并附时间点，**不转录音轨，也不声称完整理解视频**。图片单张 4MB，本次视觉输入最多 8 张。PDF 提取前 100 页文字；Office/ODF 提取文字及单元格原始值，保留格式/图片/公式解析的限制；文本、代码、日志与 UTF-8/UTF-16 聊天记录支持正文预览及 `qq_read_attachment` 分页读取。压缩包仅列目录，不自动解压或执行。扫描 PDF 请发送截图。
- 入站每次最多 6 个附件，单文件 32MB、合计 64MB；文字解析最多 2MB，超限/过期/下载失败/不支持的格式均明确标注。此为附件资源保护，与任务总轮次/总时长独立。下载在 FIFO 任务内进行，可被 `/stop` 取消；仅允许公网 HTTP(S)，校验实际连接 IP 和重定向，禁用环境代理，不携带机器人凭据，也不把附件签名 URL 写入模型输入或日志。
- 下载原件仍放在每任务独立的 0700 临时目录（文件 0600），正常完成、失败或取消后清理。QQ 管理员收到图片/动图（包括引用附件）时，额外自动将原始字节保存到 `workspace/qq-inputs/<随机任务目录>/attachment-N.<实际格式>`；目录 0700、文件 0600，不采用用户文件名，拒绝符号链接逃逸和覆盖。GIF/WebP 不转码或重编码，原有帧、时序与元数据保持原字节；模型视觉输入仍只保证其实际支持的画面分析。
- 图片副本的相对路径、格式、字节数、SHA256 与单独保存状态进入实际上下文和 `qq_read_attachment` 元数据，可直接用既有 `workspace_exec`/发送工具处理，无需重新下载或由 base64 重建。副本任务结束后保留供后续处理，使用前需核查文件是否存在。超过视觉 4MB 上限但在原有下载上限内的图片仍可保存；副本写入失败不抹掉有效视觉输入，视觉不支持的格式保留原图文件供转换，不冒充已看过。
- 普通用户仍只能读取本任务的附件解析结果，不获得 workspace 访问或持久写入权限。审批恢复复用本任务输入与同一份文件副本；重启不恢复未完成的附件任务。聊天历史保留有上限的解析预览、状态及原图副本元数据，不含附件下载签名 URL。
- 文档/视频解析依赖 Linux、Bubblewrap、`setpriv`、`ffmpeg`/`ffprobe` 与 `pdftotext`（Debian/Ubuntu 包 `ffmpeg poppler-utils`）；缺少解析器或沙箱时明确失败，绝不回退到宿主执行。模型自身仍须支持图片输入。协议依据：[腾讯官方 SDK 入站字段说明](https://github.com/tencent-connect/qqbot-nodejs/blob/main/USAGE.md#53-qqbotinboundmessage)。
- 入站验收覆盖真实 HTTP 模拟的引用图片→模型多模态→文件分页读取、无正文附件、跨会话/过期引用拒绝、引用内命令隔离、下载长度与公网限制、取消下载后 FIFO 继续执行、审批恢复复用输入及临时文件权限/清理；PDF 和视频使用真实文件及沙箱解析器验证。未向真实 QQ 用户发送验收消息，实际引用/合并转发字段仍取决于平台下发内容。

- 支持 `qq_file` 发送文档/代码/压缩包等文件（200MB 内）；`qq_image` 发送 PNG/JPG/GIF/WebP/BMP（20MB 内，GIF 保留原动画）；`qq_video` 发送 MP4（30MB 内）；`qq_audio` 发送 SILK/MP3/WAV/OGG（20MB 内）。`qq_emoji` 发送 Unicode 表情/颜文字，自定义表情包使用图片工具。文件统一来自 workspace，沿用 QQ 管理员权限；只能发回当前会话，读取时拒绝符号链接逃逸。分片与校验按文件流处理，不把大文件整体装入内存。
- QQ 默认 `qq.reasoningEffort="high"`，独立于网页账号偏好；默认提供商传 `reasoning_effort`，额外提供商沿用已有 reasoning 参数适配。不会将隐藏思考过程发给 QQ。已有自定义人设/系统提示词继续保留，额外附上当前 QQ 适配器的真实媒体能力说明，避免旧模板仍声称只能发 PNG/JPG。长回复按完整段落分成消息，不再截断到 1000 字；Markdown 单次工具最多 5000 字，完整发送。
- `agent.qqMaxIterations=0`、`agent.runTimeoutSec=0` 分别表示不设对话工具轮次和任务总时长上限。`workspace.execTimeoutSec=0` 不设置单条代码命令的额外时限；正数仍可显式启用限制。主动停止、服务停止和原有审批/授权撤销有效；平台 API/网络操作自身的超时、文件大小与输出内存上限继续生效。普通 Minecraft 公共聊天仍是 8 步，身体任务保留自身预算及生命周期规则。
- `agent.maxConcurrentRuns=1` 默认启用全通道 FIFO，网页、QQ、Minecraft 与身体任务一次处理一个；同一会话始终串行，长任务及 DSH 工具审批等待会让其他会话排队。同会话快速连续输入保留排队，队满明确回复。Eino 模型 HTTP 请求另有可取消串行入口，DSH 客户端和插件也各限定一次一个任务，只提高外层并发数不会让模型调用并行。
- 排队/队满通知作为系统消息保存，只用于显示，不进入模型上下文。QQ 历史以触发消息结尾，允许纳入前一轮排队任务晚到的真实回答，但排除当前轮通知及后续输入；旧版本保存的队列提示不再喂给模型，不删除聊天记录。
- QQ `/tasks` 或 `/queue` 显示自己的任务状态与后端总计数；`/stop`（或“停止任务”“停止”“取消任务”）停止自己的运行、等待审批及排队任务。配置的 QQ 管理员可停止当前群内任务；普通群成员不能取消其他人的任务。控制命令不进入模型队列；排队取消立即释放排队位置。任务及队列在内存中，服务重启会结束它们，不承诺自动续跑外部动作。
- `qq.streamProgress=true` 使用官方私聊流式接口显示排队/工具进度；群聊只稀疏提示，亦可主动查询。进度不含工具参数、原始结果或其他用户请求。平台未开放流式权限/连接异常时停止该流并使用稀疏通知；不会让状态消息打断执行。
- 每个入站 `msg_id` 的文本、Markdown、媒体和流式进度共享递增序号。仅在平台明确拒绝过期/次数耗尽时降级主动消息；网络超时、5xx、消息去重和未知失败不盲目重发，避免重复附件或动作。主动消息仍取决于机器人权限、接收方开关及平台额度。

能力边界以 QQ 官方接口为准：[富媒体/GIF 与文件](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/rich-media.html)、[收发频率和去重](https://bot.q.qq.com/wiki/develop/api-v2/server-inter/message/overview.html)、[私聊流式协议](https://bot.q.qq.com/wiki/develop/api-v2/autogen/api/v2_users_user_openid_stream_messages.post.html)。本项目是官方 Bot：不声称拥有个人 QQ 号的全部社交/通话能力；群聊/单聊的原生“表情表态”接口不开放，Unicode 表情和图片表情包可发送。新配置默认不限制任务，旧配置中显式正数须改为 0 后重载才生效。

### 模型调用超时

任务上下文是模型等待的唯一总预算。`agent.runTimeoutSec=0` 默认无限；配置正数时统一约束每个运行阶段（含审批后恢复），较短的主动取消、进度摘要等上下文仍优先生效。`backgroundAfterSec` 只控制通用长任务提示，QQ 自己管理进度时不重复发送，也不额外调用模型总结进度。失败不自动重发请求。
调用错误会区分主动停止、超时、请求/协议不兼容（400/422）、鉴权失败（401/403）、额度不足（402）、模型下线（404）、限流或上游容量不足（429）和网关故障（5xx）。网页提示不回显提供商原始错误正文或凭据；排查时先按失败时间与所选模型核对服务日志，不自动重发失败请求。


2026-10-21 本轮隔离验收：全仓 `go test -p 1 ./...`、`go vet -p 1 ./...`、10 项部署脚本测试通过；agent/QQ/tools/storage/config/main 竞态检查通过。新增真实 HTTP 模拟验证文件与 GIF 原字节分片、媒体类别/中文文件名、路径及符号链接拒绝、200MB 预检、跨媒体递增序号、明确失败才降级、完整长文、流式协议、群成员取消隔离、管理员多身份识别、FIFO/取消立即释放、连续问题上下文，以及真实 Eino 超过原 80 轮限制。未向真实 QQ 用户发送验收消息；平台账号的文件/流式权限及手机端实际呈现仍需在用户对话中确认。现有上游最小 high 参数请求返回 HTTP 200，未触发外部工具。

2026-10-21 身体 UX 候选修订：MC 普通聊天可读身体公开投影，已验证游戏委托才获得私密完整控制；碰撞修正不再无差别暂停，body_stop 仅取消动作。身体默认12步、连续只读3次、medium思考强度，observe 含地形，goto 可省略y。详见 [body/README.md](body/README.md) 与 [本轮实测及迭代](BODY_UX_FOLLOWUP_2026-10-21.md)。已与DSH/会话指令后端合并，2026-10-21按服主授权发布正式后端a90cafc与Paper0.3.3；真实上游可靠性和长期性能余项见实测文档。

本轮补充：玩家资料页的身体任务显示最近20项工具状态及耗时，仍只对已验证操作者可见；Node身体可配置客户端视距（默认4）。完整隔离测试使用硬内存上限和主机余量预检，不以JVM堆上限代替总栈资源预算。测试结论与真实模型限制统一记录在上述体验文档中。

原图与思考指令回归：`go test ./internal/qq ./internal/storage ./internal/tools ./internal/webui ./internal/agent`；`npm run test:conversations` 验证真实浏览器命令、滑杆同步和恢复；`npm run test:models` 保留思考滑杆动效回归。真实 GIF 原件通过帧/时序与字节校验，并在 Bubblewrap 工作区内实际读取。显式会话思考强度可覆盖网页身体任务的思考档位，但不修改身体工具、审批、12 步预算或只读预算；未显式设置时仍沿用身体自身配置。
