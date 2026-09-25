# MINE_MINECRAFT_DESIGN.md — Minecraft 通道体验设计（v1）

> 状态：**设计评审（2026-09-25）**，待评审通过后实施
> 目标读者：实现这个仓库的 coding agent / 后续维护者
> 范围：只改 MC 通道的**交互体验层**（反馈/呈现/触发/节奏/异常），不动 Agent 核心、不动其他通道
> 已拍板：会话 `shared`（保持现状）· 默认 `broadcast + @归属` · 状态用 ActionBar · 长回答分段多条 · 富客户端（聊天框 mod）**以后再说**
> 约束：轻量化——不引入新依赖、不新增抽象层；复用现有 `Request.Progress` / `sessionPool` / `SendProtocol` / 系统命令体系

---

## 0. 背景与现状诊断

Agent 后端已完成多通道化（QQ/企微/网页均可每会话隔离、有进度回执、长文本切分），但 MC 通道还停留在最早的一版：**能答，但用起来"没反馈、不知道在回谁、刷屏"**。

| 环节 | 现状（代码位置） | 体感问题 |
|---|---|---|
| 响应反馈 | MC 的 `agent.Request` 不填 `Progress`；`internal/webui/channel.go:495` 是唯一使用者 | 发出后 2~20s 全静默，玩家以为没收到、重复发 |
| 触发 | 只认聊天 `@agent`（`cmd/mineagent/main.go` 的 `matchTrigger`）；`/agent` 用纯命令实现 | 聊天框无补全；`/agent` 只有 5 个补全词；两入口行为不一致 |
| 回复 | 整段文本一条 `agent.message`（`channels/minecraft/channel.go`） | 无归属（不知道在回谁）；超长难读；Markdown 符号（`**`/`#`/`-`）原样出现 |
| 路由 | `replyMode` 只有 `broadcast`/`player`（`agent.target`） | 多人时全服刷屏，或完全看不到别人的问答 |
| 节奏 | MC 无冷却、无 per-player 排队上限（QQ 有 `MinIntervalMs`/`MaxPendingPerUser`） | 单人刷屏占满该会话队列，其他玩家被饿死 |
| 异常 | 后端离线时 `ChatListener` 静默 return；`Agent.Submit` 队满只写日志 | "发了没反应" |
| 会话 | 全服共用一个 `minecraft-main`（本次确认保持） | 群聊氛围，符合定位；不改成 perPlayer |
| 发现性 | `@agent /help`、`/status`、`/memory`、`/myid` 已在 `tools/syscmd.go` 实现 | 玩家不知道有这些 |

**结论**：核心能力不缺，缺的是把已有能力"呈现出来"。本设计不碰 Agent/Eino、不碰协议安全（role 授权、审批可靠性已在上一轮修复）。

---

## 1. 目标与非目标

### 目标
1. **即时反馈**：玩家触发后 ≤1s 内有可见状态，结束/失败都有收尾。
2. **可读的回复**：明确归属（在回谁）、长回答分段、Markdown 清理。
3. **好用的入口**：多触发词 + `/agent` 子命令 + 更全的 Tab 补全。
4. **不过载**：per-player 冷却与排队上限，异常有节流提示。
5. **协议结构化**：现在就把内容发成结构化事件，未来接富客户端时只换渲染器、不返工。

### 非目标（v1 明确不做）
- 不做富客户端/聊天框 mod（见 §10，以后另立设计）。
- 不做 token 级流式输出（MC 聊天会把半句话拆碎）。
- 不改会话模型（保持全服共享 `shared`）。
- 不改审批规则/安全边界（GUI/聊天只是呈现，规则仍在后端）。
- 不给 MC 引入图片/文件/语音。

---

## 2. 设计总览

分两层，v1 只做 L1：

```
L1 vanilla（所有玩家，本设计）       L2 富客户端（以后再说）
────────────────────────────         ──────────────────────────
ActionBar 状态 / 分段 / 清理 / 归属   聊天窗：输入历史、流式、富文本、按钮
        ▲                                    ▲
        └──────── 同一套结构化协议 ───────────┘
              agent.status / agent.message(+markdown/segments)
```

后端始终产出**结构化内容**；Paper 插件按当前能力渲染：v1 全部按 vanilla 渲染（ActionBar + 纯文本分段），将来有客户端能力时同一条消息渲染成富文本。

---

## 3. 协议设计（在现有 v1 上增量）

新增 `agent.status`（后端 → 插件）：

```json
{ "target": "Steve", "state": "thinking|approval|done", "text": "正在思考…", "style": "actionbar" }
```

扩展 `agent.message`（全部可选字段，老插件忽略即可，向后兼容）：

```json
{
  "target": "",              // 空=广播；非空=点名玩家
  "text": "清理后的纯文本",   // vanilla 渲染用（Markdown 已清理）
  "markdown": "原始 Markdown",// omitempty，富客户端用
  "requester": "Steve",       // 归属：谁问的
  "kind": "answer|notice|error|progress",
  "segments": [ { "index": 1, "total": 3, "text": "…" } ],
  "replyTo": "80"
}
```

- **兼容**：`V` 仍为 1；插件对未知消息类型已有兜底；`text` 仍存在，字符串渲染不变。
- **归属**：后端拼 `[AI→Steve]` 还是插件拼？——后端产出 `requester`，插件按 `replyMode`/`mentionOnBroadcast` 决定展示，避免后端硬编码展示样式。
- **预留**：`agent.delta`（流式）本期不实现、不占号，L2 再定。

---

## 4. 后端设计（Go）

### 4.1 入口与触发（`cmd/mineagent/main.go`）
- `matchTrigger` 支持多触发词：新增 `minecraft.triggers []string`（默认 `["@agent","@ai"]`），兼容旧的 `minecraft.trigger`（`triggers` 为空时回退）。
- MC 的 `agent.Request` 增加 `Progress` 回调（webui 已有先例），实现：

```go
Progress: func(text string) { /* mc.SendStatus(player, "thinking", text) */ }
```

- 系统命令分支保持"命中直回、不走 LLM"：`/agent help|status|memory|myid` 与 `@agent /help` 等价。

### 4.2 呈现（新增 `internal/agent/format.go`）
- `CleanMarkdown(text string) string`：去掉/转换 `**`、`__`、`` ` ``、`#` 标题、`- ` 列表、代码围栏、`[文本](链接)`；中文标点保留。
- `SplitReply(text string, maxChars, maxSegments int) []string`：按句子切分、超长硬切，段数与单段长度可配；返回段列表。
- 单元测试覆盖：中英混排、代码块、超长无标点、精确边界。

### 4.3 回复投递（`channels/minecraft/channel.go` + `agent.consume`）
- MC 的 `Reply` 改为：`CleanMarkdown` → `SplitReply` → `SendProtocol(agent.message, {target, segments, requester, ...})`，由插件按 `segmentDelayMs` 顺序发出。
- 归属由 `requester` 字段下传；`target` 仍由 `replyMode` 决定（`broadcast` 空 / `player` 玩家名 / `nearby` 由插件按坐标过滤）。
- **不改** `session.Reply` 的存储语义（落库仍是整条），只在发送层做呈现切分。

### 4.4 节奏与排队
- `minecraft.cooldownMs`（默认 8000）：同一玩家冷却内再触发 → 直接 `SendStatus` 提示"还要等 Xs"，不入队。
- `minecraft.maxQueuedPerPlayer`（默认 2）：MC 层维护 per-player 在途计数；`agent.Request` 增加 `OnFinish func()`，`consume` 结束时回调，计数减一；超限提示"还在处理上一条"。
  - 说明：`sessionPool` 已有同会话串行与容量 16，这里是**玩家维度**的公平性，二者互补。
- `Agent.Submit` jobs 队满、`sessionPool` 队满、模型未配置：统一走 MC 的短提示（不再只写日志）。

### 4.5 状态（ActionBar）
- 触发：`state=thinking`；工具/进度回调更新文案。
- 审批：`handleInterrupt` 时 `state=approval`；`resume` 决策后 `state=done`。
- 结束：无论成功/失败/超时，`state=done`（清空 ActionBar）。

---

## 5. Paper 插件设计（Java）

| 文件 | 改动 |
|---|---|
| `BackendClient` | 识别 `agent.status` → 转主线程；未知类型仍忽略 |
| `MineAgentPlugin` | `handleAgentMessage`：按 `segments` 顺序发送（`runTaskLater` 间隔 `segmentDelayMs`）；`requester` + `replyMode` 拼归属前缀；`kind=error` 用黄色；`nearby` 时只发给半径内玩家 |
| 新增 `StatusRenderer` | 收 `agent.status`：`target` 玩家 `sendActionBar(Component)`；`state=done` 传空清除；非 actionbar 样式可回退聊天 |
| `ChatListener` | 后端离线时给玩家节流提示（全局/每玩家 10s），不再静默 |
| `AgentCommand` | 子命令 `help/status/memory/myid` 直发达后端系统命令；Tab 补全：子命令 + 常用问法 + 在线玩家名；`/agent` 无参显示简短用法 |
| `MineAgentCommand` | 不变（status/reconnect/approve/deny） |
| `ToolExecutor`/`ApprovalHandler` | 不变（ActionBar 状态由后端 status 驱动，插件只渲染） |

- 分段发送必须是**主线程调度**（`runTaskLater`），避免异步线程操作聊天；间隔期间玩家掉线要跳过。
- ActionBar 可能与其他插件冲突（TAB/菜单）：低风险，失败不影响聊天主流程；提供配置开关 `showActionBar`。

---

## 6. 配置项（`config.json` 的 `minecraft` 段）

| 字段 | 默认 | 说明 |
|---|---|---|
| `trigger` | `@agent` | 兼容保留 |
| `triggers` | `["@agent","@ai"]` | 新增，优先于 `trigger` |
| `replyMode` | `broadcast` | 已确认；可选 `player`/`nearby` |
| `nearbyRadius` | `48` | `nearby` 模式半径（格） |
| `mentionOnBroadcast` | `true` | 广播时是否加 `[AI→玩家名]` 归属 |
| `maxReplyChars` | `120` | 单段最大字符数 |
| `maxReplySegments` | `4` | 最多分段数，超出截断并提示 |
| `segmentDelayMs` | `250` | 分段间隔 |
| `cooldownMs` | `8000` | per-player 冷却 |
| `maxQueuedPerPlayer` | `2` | per-player 在途上限 |
| `showActionBar` | `true` | 状态反馈开关 |

`config.example.json` 同步更新；旧配置缺字段走默认值（`config.Load` 已有兜底模式）。

---

## 7. 体验时序

```
普通问答
玩家: @agent 现在服务器卡不卡
  ≤1s  ─ ActionBar(请求者): 「MineAgent 正在思考…」
  工具进度 ─ ActionBar 更新: 「正在查服务器状态…」（progressSummary 开时）
  完成 ─ 聊天(broadcast): [MineAgent][AI→Steve] 一点都不卡，TPS 20…
       ─ ActionBar 清除

长回答
  同上，但整段清理+切分后顺序发 ≤4 条，带 (1/3)；超出提示"回答较长，已截断"

审批
  玩家: @agent 给 Steve 1 个钻石
  ActionBar(请求者): 「等待管理员批准…」
  管理员: 点 [批准] / 控制台 /mineagent approve
  完成: ActionBar 清除 + 结果回复（按 replyMode）

冷却 / 队满 / 离线
  冷却: ActionBar「慢一点，还要等 5s」（不入队，不消耗额度）
  队满: 聊天短提示「我还在处理上一条，稍等」
  离线: 聊天节流提示「助手暂时离线，稍后再试」（10s 内不重复）
```

---

## 8. 实施计划与验收

| 步骤 | 内容 | 验收 |
|---|---|---|
| S1 | 协议字段 + 配置项（Go） | `go test`；老插件对新字段无感（向后兼容） |
| S2 | `format.go`：清理 + 切分（Go + 单测） | 单测覆盖边界（见 §9） |
| S3 | 触发词 + `/agent` 子命令 + 冷却/排队 + 异常提示（Go + Java） | 手测冷却/队满/子命令 |
| S4 | ActionBar 状态 + 分段发送 + 归属/颜色（Java） | 手测时序 |
| S5 | 文档（README/notes）+ E2E 清单 | 无回归 |

每步可独立部署（配置默认值安全，插件与后端各自向后兼容：老后端 + 新插件、新后端 + 老插件都不崩）。

---

## 9. 测试计划

- Go：`go test ./...`、`go test -race ./...`、`go vet ./...`
  - 新增：`CleanMarkdown`（中英/代码块/链接/超长）、`SplitReply`（边界/段数上限/无标点）、多触发词、冷却/排队计数、`agent.status` 构造。
- Java：Gradle build（无既有测试框架，逻辑尽量放在可测的纯函数/小类）。
- E2E 手测清单：普通问答、长回答、审批批准/拒绝/超时、冷却、队满、后端重启（插件自动重连）、后端停机（离线提示）、无 mod 玩家全程 Vanilla。

---

## 10. 以后再说：富客户端聊天框（L2）

方向已确认可行，但**本设计不实施**。留此节记录约束，便于将来另立设计：

- **前提**：无 mod 玩家永远存在，L1 必须始终可用；mod 是增强层。
- **可复用资产**：`mineUI`（Paper+Fabric 握手/能力/渲染/资源分发）、PackHost、既有客户端安装包流程。
- **两条路线**：扩 mineUI 注册 MineAgent 会话页（推荐先 PoC）；或独立薄客户端 mod。做之前先验证 mineUI 是否具备「文本输入 + 长列表 + 流式追加 + 按钮」。
- **协议已经为此铺路**：`agent.message` 带 `markdown`/`segments`/`requester`；将来加 `agent.delta`（流式）即可，L1 渲染逻辑不变。
- **规则边界**：GUI 只做呈现，审批/权限/审计仍在后端，不能成为新规则源。

---

## 11. 取舍与风险

| 取舍/风险 | 说明 | 处理 |
|---|---|---|
| 不做流式 | MC 聊天会把半句话拆碎，观感更差 | 整句分段；流式只留给 L2 客户端 |
| 分段多发可能被反刷屏 | 部分服务器有反刷屏插件 | 段数/间隔可配，可降为 1 段 |
| ActionBar 被其他插件占用 | TAB/菜单等 | 开关可关；失败不影响主流程 |
| 长回答截断 | 超过 `maxReplySegments` | 明确提示"已截断，可 /agent 继续问" |
| 全服共享会话 | 追问会看到别人聊天（已确认保留） | 群聊定位；系统命令 `/memory` 可各自清理前需确认语义 |
| 兼容性 | 后端/插件各自升级 | 字段全可选、类型可忽略；两步部署不崩 |

---

## 12. 实施记录

（待实施后填写：commit、上线时间、与设计的偏差、线上反馈）
