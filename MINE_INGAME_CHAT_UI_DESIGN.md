# MINE_INGAME_CHAT_UI_DESIGN.md — 游戏内 Chat 重构与权限放宽设计（v1）

> 状态：**部分实施（2026-10-26）**：公开两栏 UI、游戏内直接身体控制、保护区审批已落地；下文未实施的私有会话/Build Session 保留为后续设计。
> 范围：MineAgent Paper 插件 / MineUI 游戏内界面 / 后端会话协议 / 权限模型
> 目标读者：实现这个仓库的 coding agent、MineUI 维护者、服主
> 关联：[MineUI 组件文档](../mineUI/docs/COMPONENTS.md)、[MineUI 设计](../mineUI/docs/DESIGN.md)、`MINE_BODY_DESIGN.md`、`body/README.md`、`paper-plugin/src/main/java/com/mineagent/paper/ui/`

> **2026-10-26 服主最终决策（优先于下文）**
> 单人使用，权限直接放开；游戏内 Chat **全服公开**，不做私有会话/多会话；身体控制不再要求网页 nonce；512 格保护半径保留，**保护区内建造需要审批，区外免审批**。下文“私有会话 / Build Session / 多会话”为初版设计，未按此实施；MineUI 需求见 [MineUI 需求](../mineUI/docs/REQUIREMENTS_INGAME_CHAT.md)。实际实现见 `paper-plugin/.../MineUiPanel.java`、`AgentUi.java`、`BodyPanels.java`、`internal/bodyui/bridge.go` 与 `body/README.md` 的 2026-10-26 条目。

---

## 0. 结论（TL;DR）

1. **界面目标：把 `/agent` 从“功能面板”改成“DSH 式会话界面”**——左侧会话/身体/审批，右侧消息流，底部固定输入框；生产环境默认用 MineUI，原版 Dialog 只做回退。
2. **必须先补后端会话模型**：现在游戏内 UI 是插件内存里的 30 条共享记录，问题还会广播到全服；DSH 式体验需要 **每玩家私有的会话、历史、流式回复、工具卡和审批卡**。
3. **权限放宽建议采用“分级 + 范围授权”，不是关掉安全**：
   - 游戏内聊天不再要求网页管理员；`mineagent.use` 即可。
   - 身体控制从“网页管理员 + nonce”放宽为游戏内 `mineagent.body` + LuckPerms；网页关联保留为远程/高风险通道。
   - 普通建造/挖掘不再逐条审批；用 **Build Session（区域 + 时间 + 预算）** 一次授权，TNT/岩浆/容器/高危命令仍审批。
   - 512 格出生点保护不全局取消：给 `mineagent.builder` 开“建造区/会话例外”；未授权玩家仍受保护。
   - 保留：非 OP、无控制台、无 LuckPerms、高危审批、审计、急停、备份。
4. **MineUI 当前限制**：文本单行、页面 ≤24 KiB、列表模板不支持嵌套 list、没有 Markdown/流式文本节点。v1 用“固定 6 行卡 + 展开详情 + `streamText` 单独状态”实现聊天流；若要真正的 DSH 级气泡/多行排版，需要向 MineUI 提一个 **多行文本/可变高度条目** 的小需求（可选）。
5. **分三期**：P0 私有会话 + 两栏布局；P1 流式/工具卡/审批/模型选择；P2 身体建造会话、搜索、快捷键、移动端/小屏模式。

---

## 1. 现状与差距

### 1.1 当前实现

| 层 | 现状 | 文件 |
|---|---|---|
| 游戏入口 | `/agent` 打开原版 Dialog 或 MineUI；`/agent <问题>` 直接提问 | `paper-plugin/.../command/AgentCommand.java` |
| UI | `AgentUi` 维护每玩家 `Panel`，`MineUiPanel` 渲染标题/状态/文本行/按钮/输入框 | `AgentUi.java`、`MineUiPanel.java` |
| 记录 | 插件内存 `UiState`，每人最多 30 条；**提交时 broadcast 给全服，并记录到所有 viewer** | `UiState.java`、`AgentUi.submit` |
| 身体 | `BodyPanels` + `BodyUiClient`，需要游戏内面板 + 已验证网页管理员 nonce | `BodyPanels.java`、`BodyUiClient.java` |
| 通道 | 通过 `chat.message` 发 `@agent ...` 进入共享 `minecraft-main` | `AgentUi.submit` |
| MineUI | 单列 `column` + `scroll/list` + `input`，无会话侧栏，无消息气泡，无流式 | `MineUiPanel.definition` |

### 1.2 为什么不像 DSH

- DSH 的信息架构是 **侧栏（会话/工作区）+ 主区（消息流）+ 底部 composer**；当前是“标题 + 状态 + 一列文本 + 按钮”。
- DSH 的消息是 **卡片/气泡 + 工具事件 + 审批卡 + 流式**；当前是压平后的单行文本。
- DSH 有 **多会话、历史、重命名**；当前只有本次在线的 30 条，离线即清空。
- DSH 的输入区固定在底部，有模型/思考、停止、附件；当前只有输入框 + 刷新/更多按钮。
- 当前游戏内问题会 **全服广播**；DSH 的会话默认是私有的。

### 1.3 现有可复用资产

- MineUI 的 `row/column/scroll/list/input/button/box/text/tabs/slider`、`state`/`batch`/action、`vanilla:input/button/panel` 皮肤、`mineui:glass`。
- Paper 的 Dialog 回退（无客户端 mod）。
- `UiState.lines` 的 50 列换行逻辑（可复用为服务端预换行）。
- `BodyPanels` 的身体状态/审批数据。
- 后端已有的 `session`/`messages`/`agent.Request`/`ToolEvent`/`Progress`/`Approvals`。
- DSH 桥接已有 `backend=dsh`、工具事件、会话/停止/审批语义。

---

## 2. 目标与非目标

### 目标

- G1：`/agent` 在 1 秒内打开一个 DSH 式全屏会话界面。
- G2：每玩家私有会话；支持新建、切换、历史、重命名/删除、断线恢复。
- G3：消息卡片化：用户/助手/系统/工具/审批/错误分开；长回答可读。
- G4：底部固定 composer：发送、停止、草稿保留、字符上限；可选模型/思考强度。
- G5：工具调用与审批可见：`body_goto`、`body_observe`、WorldEdit、MC 审批；不显示敏感参数/结果。
- G6：身体状态/暂停/审批作为侧栏常驻卡；可从聊天直接进入。
- G7：原版客户端/未装 MineUI 自动回退到现有 Dialog；功能不消失。
- G8：权限放宽后仍有审计、急停、硬边界；不是“谁都能 OP”。

### 非目标（v1）

- N1：不在游戏内做完整 Markdown 渲染器/图片/视频。
- N2：不做多玩家共享房间聊天（默认私有；公开 `@agent` 仍保留）。
- N3：不做客户端 mod 新协议；只扩 MineUI 页面与服务端状态。
- N4：不做跨服/多身体。
- N5：不重写 MineUI 渲染器；若需要多行文本，只提最小需求。

---

## 3. 权限放宽设计（先定边界，再放行）

> 用户要的是“少一点手续”，不是“关掉安全”。建议用 **分级 + 范围授权** 替代“逐条审批/网页 nonce”。

### 3.1 身份与权限节点

| 节点 | 默认 | 能力 | 备注 |
|---|---|---|---|
| `mineagent.use` | true | 打开游戏内 Chat、私有问答、只读工具 | 不再要求网页登录 |
| `mineagent.body` | op | 查看身体状态、安全移动/观察/聊天/交互/命令（LuckPerms 限制） | **新增游戏内直接控制**，不再强制 web nonce |
| `mineagent.body.control` | op | 暂停/恢复/急停/断开 | 与普通 body 分离 |
| `mineagent.builder` | false | 建造、WorldEdit、飞行/传送、长任务 | 由 LuckPerms 组授予 |
| `mineagent.buildsession` | false | 创建/使用限时建造会话 | 管理员授予 |
| `mineagent.approve` | op | 审批高危动作/建造计划 | 可配置为 builder 组长 |
| `mineagent.admin` | op | `/mineagent status/reconnect`、查看审计 | 不变 |
| `mineagent.teleport` / `give` | op | 以请求者身份执行 | 现有，保持 |

**建议先放宽的三项**：

1. **游戏内 Chat 私有化**：`mineagent.use` 即可，不再广播，不再要求网页账号。
2. **身体控制游戏内直连**：`mineagent.body` 即可，不再强制网页管理员 nonce；网页 `/ai/main` 仍保留给远程/审计场景。
3. **普通建造免逐条审批**：`mineagent.builder` + Build Session 内，普通方块/WorldEdit 批量操作不逐条审批；TNT/岩浆/容器/高危命令仍审批。

### 3.2 Build Session（替代逐条建造审批）

```
POST /api/body/build-sessions
{
  "agent": "main",
  "area": {"world":"world","min":[300,60,520],"max":[340,80,550]},
  "durationMin": 30,
  "blockBudget": 2000,
  "allowWorldEdit": true,
  "reason": "建造农场与小屋"
}
→ { "id":"bs-...", "expiresAt": ..., "approvedBy": "..." }
```

- 一个 Build Session 内：普通 dig/place/WorldEdit 不逐条审批；每次动作仍校验区域、方块 denylist、预算、非 OP、审计。
- 超出区域/预算/时间 → 拒绝，要求新 session。
- TNT/岩浆/水/火/容器/PvP 仍需单次审批（或 `approve` 权限）。
- UI 侧栏显示 “建造会话 · 剩余 18 分钟 · 1200/2000 块”，可撤销。
- 512 出生点保护：Build Session 的区域若与保护重叠，默认拒绝；**不要**全局关闭保护。需要时由管理员在保护配置中给指定区域加白名单（Paper 侧）。

### 3.3 现有硬边界保持不变

- 身体不能 OP、不能用控制台、不能 LuckPerms/reload/stop/execute。
- bedrock/barrier/command_block/structure 等硬拒绝。
- TNT/岩浆/水/火/重生锚/末影水晶/容器/PvP 仍审批或默认关闭。
- 所有动作审计；急停/断开；写保护；备份。
- 普通玩家只读公开 profile；不能指挥身体。

### 3.4 观察/预算放宽

- `mineagent.use`：默认 12 步 / 3 次连续只读。
- `mineagent.builder`：可显式 `maxSteps=0`、只读预算提高（如 8），但保留单动作超时与急停。
- 普通聊天不再被身体长任务占满：body 与 chat 分开 run lane（或至少 `maxConcurrentRuns>=2`）。

---

## 4. 游戏内 Chat 信息架构

### 4.1 DSH 对照

| DSH | 游戏内 v1 |
|---|---|
| 左侧会话/工作区 | 左侧：新对话、会话列表、身体状态、审批入口 |
| 中间消息流 | 中间：消息卡片（user/assistant/system/tool/approval） |
| 底部 composer + 模型/模式 | 底部：输入框 + 发送/停止 + 模型/思考入口 |
| 顶部标题/面包屑 | 顶部：`MineAgent · 会话标题` + 连接/身体状态 |
| 工具调用卡/审批卡 | 工具/审批卡内联在消息流 |
| 流式输出 | 先状态流（思考/工具），P1 再接 delta |
| 附件/文件 | v1 不做；可显示物品/玩家预览 |

### 4.2 线框

```text
┌──────────────────────────────────────────────────────────────────────┐
│  MineAgent · 与 MineAgentBot 对话          在线 · 未暂停   [新对话] [×] │
├───────────────┬──────────────────────────────────────────────────────┤
│ ＋ 新对话      │  ┌──────────────────────────────────────────────┐   │
│ 今天           │  │ 你  10:42                                     │   │
│  农场建造      │  │ 去看看农场的小麦，缺水就补一下。              │   │
│  服务器状态    │  └──────────────────────────────────────────────┘   │
│ 更早           │  ┌──────────────────────────────────────────────┐   │
│  昨天的对话    │  │ MineAgent  10:42           [复制] [展开]      │   │
│                │  │ 我先观察一下农场的 72 株小麦……                │   │
│ ───────────    │  │ 工具：body_observe ✓ 1.2s                      │   │
│ 身体           │  │ 工具：body_goto ✓ 4.6s                         │   │
│ 在线 · 待机    │  │ 结论：……                                      │   │
│ [暂停] [身体]  │  └──────────────────────────────────────────────┘   │
│                │  ┌──────────────────────────────────────────────┐   │
│ 待审批 1       │  │ 审批：在 (306,64,535) 放置 TNT                 │   │
│ [查看]         │  │ 理由：清理……   [批准] [拒绝]                   │   │
│                │  └──────────────────────────────────────────────┘   │
│ 模型/思考      │  ┌──────────────────────────────────────────────┐   │
│ V4.1 Flash     │  │ │ 输入消息…                          [发送]  │   │
│ 标准           │  │ │ / 命令  @ 提及                      [停止]  │   │
└───────────────┴──┴──────────────────────────────────────────────┴───┘
```

### 4.3 布局规则

- 全屏 `row`：左栏 `width 24%（240–280px）`，右栏 `flex 1`；MineUI 没有 flex，可用 `width: "76%"`。
- 右栏：顶部 header（固定 28px）、消息流 `scroll/list`（约 58vh）、composer（约 80px）。
- 左栏：新对话 28px、会话列表 `list` 约 40vh、身体卡/审批卡/设置。
- 小屏/GUI scale 大：`焦点模式`（隐藏左栏）开关；或默认单栏 + “会话”按钮打开侧栏页面。MineUI 没有响应式断点，用状态 `compact` 切换。
- 色彩：沿用 Minecraft 深色半透明（`mineui:glass`），不要照搬 DSH 白色网页；用角色色区分：
  - user：暖色/金色边
  - assistant：青/蓝色边
  - tool/system：灰/绿色
  - approval/error：红/橙色
- 按钮最小高度 20px（Minecraft GUI 像素），实际视觉目标 24–28px；常用操作（发送/停止/批准）在拇指/鼠标易达位置。
- 动效：只做 120–180ms 的淡入/位移；尊重 MineUI 的 reduced motion 能力（若可用）。

### 4.4 状态与空态

- 空会话：标题 “今天想做什么？” + 3–4 个快捷建议（服务器状态 / 在线玩家 / 去农场 / 建造）。
- 思考中：显示 “正在思考…” + 呼吸点；不要阻塞输入（可停止）。
- 工具中：显示 “正在执行 body_goto…” + 耗时；结果回来变成可展开卡。
- 流式：最后一条 assistant 卡片显示 `▍` 光标；完成后消失。
- 错误：红色系统卡，含原因 + “重试/停止/打开网页”三类操作；不要显示“可能已提交”这种模糊文案。
- 审批：卡片显示 requester、动作摘要、reason、剩余时间、[批准]/[拒绝]；点击后立即禁用按钮并显示结果。
- 断线：顶部黄色条 “后端断开，正在重连”；已发送消息标记 “未确认”，不自动重发。

---

## 5. MineUI 页面设计

### 5.1 节点映射

| 区域 | MineUI 节点 | 说明 |
|---|---|---|
| 根 | `row` 100%×100% | `mineui:glass`，圆角/半透明 |
| 侧栏 | `column` 240px | 背景稍深；`clip` |
| 新对话 | `button` `vanilla:button` | action `new_chat` |
| 会话列表 | `list` `{state.sessions}` | itemTemplate: title/time/selected；action `open_session` + `itemIndex` |
| 身体卡 | `column` + `text`/`button` | `{state.body.*}`；按钮 `body_pause`/`body_resume`/`body_panel` |
| 审批入口 | `button` | 红点/数量 |
| 主 header | `row` | 标题/状态/模型/关闭 |
| 消息流 | `list` `{state.messages}` | itemTemplate: 角色变体 + 6 行文本 + 时间/操作 |
| 工具卡 | `list` 内 `system` 变体 | 小字：工具名/状态/耗时；点击 `expand_tool` |
| 审批卡 | `list` 内 `approval` 变体 | 按钮 action `approve`/`deny`；payload 带 index |
| 输入 | `input` `vanilla:input` | action `send`，maxLength 512 |
| 发送/停止 | `button` | `send`/`stop`；运行中禁用发送或变停止 |
| 模型/思考 | `button` → 设置页 | 不占主界面 |

### 5.2 消息数据模型（插件 → 页面 state）

```json
{
  "id": "m-18",
  "role": "user|assistant|system|tool|approval|error",
  "author": "你|MineAgent|系统",
  "time": "10:42",
  "lines": ["第一行", "第二行", "..."],
  "kind": "text|tool|approval|error",
  "tool": {"name":"body_goto","status":"ok","durationMs":460},
  "approval": {"id":"ap-...","prompt":"...","expireAt":...},
  "expandable": true,
  "streaming": false
}
```

页面用固定 6 行/消息：`l0..l5` + `visible`；服务端 `UiState.lines` 预换行（50 列）。超过 6 行时 `expandable=true`，卡片底部“展开”打开详情页（`scroll` + 全部行）。
- 长回答不直接截断；卡片给 6 行摘要 + “展开全文”。
- 工具/审批卡不显示参数/原始结果，只显示名称、状态、耗时、理由、审批按钮。

### 5.3 流式方案

- 不在每次 token 时改 `messages` 数组（会重建 list/滚动跳动）。
- 单独状态：`streaming`（bool）、`streamIndex`、`streamText`；最后一张 assistant 卡片的第 0–5 行绑定 `streamText` 的预切分（服务端按 50 列切好），或简化为 `streamText` 放在一个 `text` 节点，完成后切换到 messages 数组。
- 服务端 100–200ms 合并一次 delta；MineUI `batch` 一次 PATCH；不逐 token 发。
- 客户端不自动滚动 if 用户上滚；提供“回到底部”按钮；v1 可 `autoScroll=true` + highlight 最后一条，P2 再优化。
- DSH 后端/Eino 若没有流式，则状态流 + 完成后一次性消息；接口保留 `ui.delta`。

### 5.4 页面大小与列表

- 页面 JSON 目标 ≤18 KiB（MineUI 建议 ≤24 KiB）。
- 消息流 `list` 只放最近 12–20 条；更早用“加载更早”分页。
- 单列表 ≤512 条；每消息 6 行文本 + 少量按钮。
- 不使用嵌套 `list`；工具/审批用 `kind` 变体。
- 文本不使用 Markdown 原样；服务端 `CleanMarkdown`/`lines`：去掉 `**`/`#`/围栏，列表转 `•`，代码块转灰底/等宽行（MineUI 字体固定，颜色区分）。

### 5.5 MineUI 能力缺口（可选需求）

若要真正 DSH 级气泡/多行排版，建议向 MineUI 提一个最小需求（不阻塞 v1）：

- `text` 节点支持 `wrap: true` + `maxLines` + 自动高度（变高列表项已有基础）；
- 或新增 `richText`/`chatMessage` 节点（多行/角色样式）；
- 或 list item 的 `itemTemplate` 支持可变行数组（`{item.lines}` 迭代）。

v1 不依赖新节点：固定 6 行 + 展开详情即可。

---

## 6. 后端与协议

### 6.1 会话模型

- 每个玩家一个私有 namespace：`SessionKey = "mcui:<uuid>:<conv>"`；`conv` 为空表示新对话（首条消息时创建）。
- 存储复用 `sessions`/`messages`；新增 `game_conversations`（uuid/name/conv/title/updated）或扩展现有 conversation 表。
- 默认私有；不广播；`@agent` 公共聊天仍走现有 `minecraft-main`，两者独立。
- 断线/重启：会话从 SQLite 恢复；运行中的 run 取消并标记“中断”，不自动重放。

### 6.2 WS 消息（Paper ↔ Go）

保留现有 `minecraft` role，新增可选类型（v1 向后兼容）：

| 方向 | type | 说明 |
|---|---|---|
| Paper→Go | `ui.send` | `{conv,text,clientId,model?,effort?}` |
| Paper→Go | `ui.history` | `{conv,before?,limit}` |
| Paper→Go | `ui.stop` | `{conv}` |
| Paper→Go | `ui.approve` | `{conv,approvalId,approved}` |
| Go→Paper | `ui.accepted` | `{conv,messageId}` |
| Go→Paper | `ui.status` | `{conv,state,text}`（thinking/tool/streaming/error） |
| Go→Paper | `ui.delta` | `{conv,messageId,text}`（可选） |
| Go→Paper | `ui.tool` | `{conv,callId,name,status,durationMs}`（不含参数/结果） |
| Go→Paper | `ui.approval` | `{conv,id,prompt,expiresAt,requester}` |
| Go→Paper | `ui.done` | `{conv,messageId,text,error?}` |

- `authorizeMessage` 只允许已连接的 `minecraft` role 发 `ui.*`；先校验 player UUID/权限（插件侧）。
- `ui.send` 携带 `permissions`/`trust`（插件已校验 `mineagent.use/body/builder`）；后端只接受当前请求注册的工具，不继承其他通道工具。
- 身体控制：新增 `GamePrincipal{UUID,Name,Trust,Tools}` 或扩展现有 `body.Principal`；`mineagent.body` 即可，非 web nonce。高风险管理仍走 `Approvals`。
- 停止：`ui.stop` 取消该 conv 的 run；body stop 与 chat stop 分开。
- 审批：`ui.approval` 内联；批准/拒绝回 `ui.approve`；与现有 `Approvals` 共用。
- 流式：`ui.delta` 可选；DSH 后端支持时启用，Eino 不支持则 `ui.status` + `ui.done`。
- 限流：每玩家 1 个 active run；输入 1.5s 防抖；状态 100–200ms 合并；页面事件只发 UI 需要的最小字段。

### 6.3 工具集与权限

- `mineagent.use`：只读工具（服务器状态、玩家列表、时间/天气、body_profile）。
- `mineagent.body`：body_status/observe/goto/stop/say/command（按 LuckPerms）；高危动作审批。
- `mineagent.builder`：body_place/dig/interact/use_item、WorldEdit、长任务；Build Session 内免逐条审批。
- `mineagent.approve`：审批；`mineagent.admin`：暂停/停止/重连。
- 不在 UI 里暴露 workspace/QQ/邮件等无关工具；聊天 UI 只注册聊天 + 身体 + 审批。
- 工具参数/结果不进入 MineUI 文本；只显示摘要。
- 命令仍以身体玩家身份执行，LuckPerms 是边界；不因 UI 放宽而改用控制台。

---

## 7. 实施阶段与验收

### P0：会话骨架（1–2 天）

- 后端 `mcui` 会话/历史/消息模型；Paper `ui.*` 协议；私有不广播。
- MineUI 两栏：侧栏（新对话/会话列表/身体卡）+ 消息流 + 底部输入。
- 发送/停止/草稿/断线提示；原版 Dialog 回退。
- 验收：`/agent` <1s 打开；两个玩家会话互不可见；发送后 1s 内见状态；刷新/重连能看到历史；原版客户端仍可用。

### P1：DSH 式内容丰富化（2–4 天）

- 消息卡片（user/assistant/system/tool/approval）、6 行 + 展开；工具耗时；审批内联。
- 流式 `ui.delta`（DSH 优先）；状态流；错误卡。
- 模型/思考入口；会话重命名/删除；审批数量 badge。
- 验收：长回答可读；工具卡不泄露参数；审批可完成；无重复/错序；断线不丢历史；页面 ≤24KiB。

### P2：身体/建造/易用性（3–5 天）

- 身体卡：pause/resume/stop/body panel；Build Session 卡；建造会话剩余预算。
- 小屏/焦点模式；MineUI keybind（F8 可配）；会话搜索；公开/私有提示。
- 权限放宽开关：`permissions.relaxedGame=true`、`body.gameControl=true`、`builder.enabled=true`；默认仍 strict，逐项开启。
- 验收：builder 在 Build Session 内可连续建造；区域外/超预算拒绝；TNT 仍审批；审计完整；急停有效。

### 回归/安全测试

- 两人同时私聊，A 的历史/问题不出现在 B；管理员的会话不广播。
- 普通玩家不能打开完整 body 工具；`mineagent.body` 可以但非 OP；builder 受 Build Session 限制。
- `body.maxSteps=0`/长任务不阻塞普通聊天（并发测试）。
- 审批拒绝/超时/重连/停止；UI 按钮状态真实。
- 原版客户端/旧 MineUI 版本回退；页面损坏/过大时回退。
- 视觉：1280×720、1920×1080、GUI scale 2/3；对比度、按钮可点、长文本不溢出。

---

## 8. 风险与取舍

| 风险 | 影响 | 缓解 |
|---|---|---|
| 放宽 body 权限 | 管理员误操作/账号被盗 | LuckPerms + 审计 + Build Session + 急停；保留高危审批 |
| 私有会话 | 玩家无法看到别人问答 | 保留 `@agent` 公共聊天；UI 默认私有，可配置“公开” |
| MineUI 文本单行 | 长回答只能展开 | 6 行摘要 + 详情页；向 MineUI 提多行需求 |
| 页面 ≤24KiB | 消息/工具卡多 | 最近 12–20 条 + 分页；工具卡摘要 |
| 流式 | 包量/滚动抖动 | 100–200ms 合并；`streamText` 单节点；不逐 token 重建 list |
| 会话存储 | SQLite 增长 | 每玩家保留 N 个会话/消息，归档/清理 |
| 权限节点扩散 | 配置复杂 | 默认 strict，配置开关；文档/LuckPerms 清单同步 |
| 原版回退 | 功能不一致 | Dialog 保留基本问答/审批；不承诺同等体验 |

---

## 9. 待确认

1. 游戏内 Chat 默认 **私有** 还是继续“全服可见”？建议私有；`@agent` 公共聊天保留。
2. 身体控制是否完全去掉 web nonce，改为 `mineagent.body`？建议是；网页关联保留为远程/高风险通道。
3. 普通建造是否接受 Build Session（区域+时间+预算）一次授权？建议是；TNT/岩浆/容器仍审批。
4. 512 保护：只给 `mineagent.builder` 指定建造区例外，还是降低全局半径？建议只给建造区例外。
5. 是否允许 `body.maxSteps=0`/长任务占用 run slot？建议 body 独立 lane 或 `maxConcurrentRuns>=2`。
6. MineUI 是否接受“多行文本/可变高度条目”小需求？v1 可不依赖，但体验会差一档。
7. 流式：只对 DSH 后端开启，还是 Eino 也要？建议先 DSH，Eino 用状态+最终消息。
8. 模型/思考选择：普通玩家是否可选？建议 builder/admin 可选，普通玩家用服务器默认。

---

## 10. 最小落地建议

如果只做一轮，建议按这个顺序：

1. **先做私有会话 + 两栏 MineUI**（P0），不要等所有权限讨论完；这解决了“刷屏/泄露/不像 DSH”的根问题。
2. **同时把游戏内 body 权限从 web nonce 放宽到 `mineagent.body`**（P0），因为这是当前最影响“方便”的摩擦；保留审计/急停。
3. **再做 Build Session**（P1），把建造从逐条审批变成一次范围授权；这是第二个大摩擦。
4. 最后做流式/模型选择/小屏细节。

> 不建议一次性删除所有审批/保护。把“逐条审批”换成“范围授权 + 高危审批 + 审计”，既方便又不会把服务器变成无人看管的试验场。
