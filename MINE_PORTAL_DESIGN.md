# MINE_PORTAL_DESIGN.md — Mine Portal 设计文档（v1）

> 状态：**P0/P1 已实施并上线（2026-09-25，commit f8df1f5）**；P2+ 待做（见 §14 实施记录）
> 目标读者：实现这个仓库的 coding agent / 后续维护者
> 范围：把现有 MineAgent WebUI（单页 = 聊天）升级为 **Mine Portal**：账号 + Agent + 小游戏 + 排行榜。
> 约束：**轻量化**——单 Go 进程 + 单 SQLite + Vanilla ES Modules，不引入 Web 框架 / Redis / 微服务 / 构建步骤。

---

## 0. 目标与非目标

### 目标
1. 有门户首页（`/`），把 Agent 作为其中一个应用。
2. 有**账号系统**（当前只按名字），账号被 Portal 持有，Agent 与 Games 都依赖账号而不各自实现登录。
3. 小游戏入口：**先只做平台骨架**（游戏目录 + 页面壳 + 一局原始记录），
   **积分模型、排行榜、反作弊规则等游戏类型确定后再设计**（见 §8）。
4. 一次登录，门户 / Agent / 游戏 / 排行榜全站通用。
5. 现有 Agent 功能、数据、URL 与正在使用的线上实例**不被破坏**（可灰度、可回滚）。

### 非目标（v1 明确不做）
- 不做前后端分离、不上 React/Vue/Next、不加构建步骤。
- 不做微服务、不加 Redis/消息队列。
- **不定积分/排行榜/反作弊规则**：游戏类型未定，这些规则不先写死；
  平台只要求"一局能落一条原始记录"，规则层后置（见 §8）。
- 不做改名功能（见 §9.4 的设计取舍与预留）。
- 不做多人实时游戏（WebSocket 房间是 P5 之后的自然扩展，不在本期）。

---

## 1. 现状盘点（可直接复用的资产）

| 资产 | 位置 | 复用方式 |
|---|---|---|
| 单进程 HTTP 服务（含静态页 + SSE） | `internal/webui/server.go` | Portal 与 Agent 同进程，同一端口 |
| SQLite（WAL） | `internal/storage/storage.go` | 直接加表，不新建库 |
| 名字登录 + token（JSON + cookie） | `webui.Channel.tokens/prefs`、`/api/login` | P0 抽象为账号层，P1 迁到 `auth_sessions` |
| 会话隔离（按名字） | `web:c2c:<name>[:<conv>]` | ✅ 2026-09-25 已迁 `web:user:<ID>[:<conv>]`（§5） |
| 会话列表/文件/偏好 | `web_conversations`、`workspace/web-files/<name>/`、`prefs.json` | 语义不变，只换主键口径 |
| 前端模块化 + 分层 CSS | `static/js/*.js`、`static/css/*.css` | 复用 tokens/组件；新增门户与游戏页 |
| 资源版本化 + 版本自检 | `/static/<ver>/...`、`__VER__`、`/api/version` | 门户/游戏页沿用，避免缓存串版本 |
| 调试渲染模式 | `?ui=1&panel=...` | 扩展到门户/游戏页 |
| 现有只读工具 / 提醒 / 额度 | `internal/tools`、`/api/usage` | 门户卡片可直接读取 |

**现有 HTTP 端点（Portal 化前）**：
`/`、`/api/{login,logout,version,me,history,clear,events,upload,send,msgfile,options,prefs,workspace,workspace/file,conversations,conversations/delete,conversations/rename,usage}`、`/static/*`；
另有 `/ws`（127.0.0.1:8765，MC 插件）与 `/wecom`（:80，企微回调），与 Portal 无关，不动。

---

## 2. 总体架构

### 2.1 分层
```
                        ┌─────────────────────────────┐
                        │            Portal           │  页面壳 / 路由 / 导航 / 主题
                        │  （不实现业务，只组合应用）    │
                        └──────────────┬──────────────┘
                                       │
                        ┌──────────────▼──────────────┐
                        │           Account           │  用户 / 登录 / 会话令牌 / 资料
                        │   （唯一身份来源，user_id）    │
                        └───────┬─────────────┬───────┘
                                │             │
                 ┌──────────────▼───┐   ┌─────▼──────────────┐
                 │      Agent       │   │       Games        │
                 │ 会话/文件/偏好/审批 │   │ 目录/成绩/积分/排行  │
                 └──────────────┬───┘   └─────┬──────────────┘
                                │             │
                        ┌───────▼─────────────▼───────┐
                        │           SQLite            │  单库单 Store
                        └─────────────────────────────┘
```
**模块边界原则（实现时不要破坏）**
- Account 不属于 Agent；Agent 与 Games 都只依赖 Account，彼此不依赖。
- Portal 只做页面组合与路由，不承载业务逻辑。
- Games 不读 Agent 的表；Agent 不读 Games 的表。
- Minecraft 集成留作 P5 的应用/数据源，不进入本期依赖图。

### 2.2 进程与部署
仍是**一个二进制、一个端口**（HTTP :8766 + 内部 :8765 + 企微 :80）。不开新进程、不加反向代理、不加守护进程。

---

## 3. 目录规划

### 3.1 目标结构
```
internal/
├── portal/                  ← 新增：页面路由 + 壳 + 账号中间件 + 应用注册表
│   ├── server.go            页面路由（/, /agent, /games, /games/<id>, /leaderboard, /account）
│   ├── apps.go              应用注册表（新增功能只在这里注册，见 §7.5）
│   ├── home.go              /api/portal/home 聚合（逐应用 Card，失败降级）
│   ├── static.go            内嵌页面与静态资源注入（版本号/调试开关）
│   └── middleware.go        RequireUser / RequireAdmin / CORS / 访问日志
├── account/                 ← 新增：用户与会话（唯一身份来源）
│   ├── account.go           登录/登出/资料/统计
│   └── session.go           auth_sessions（token_hash、过期、多设备）
├── games/                   ← 新增：游戏平台（不含具体游戏实现）
│   ├── registry.go          游戏目录（代码内静态定义）
│   ├── score.go             成绩入库/校验/节流
│   ├── points.go            积分流水（ledger）与换算
│   └── leaderboard.go       排行榜/个人战绩查询
├── webui/                   ← 保留：Agent Web App（P2 再机械改名为 internal/web/agent）
│   ├── server.go            现有 API（P0 起增加 /api/agent/* 别名）
│   ├── channel.go           Agent 通道（会话/SSE/进度/文件）
│   └── ...
├── storage/                 ← 加表与方法（建议拆 storage_users.go / storage_games.go）
├── agent/ session/ tools/ … ← 不动
internal/webui/static/       ← 前端资源当前位置（P2 可选整体挪到 internal/portal/static，URL 不变）
├── index.html               → 迁移为 chat/index.html（Agent 页）
├── portal/index.html        门户首页（URL `/`）
├── account/index.html       我的（URL `/account`）
├── leaderboard/index.html   排行榜（URL `/leaderboard`）
├── games/index.html         游戏大厅（URL `/games`）
├── games/<id>/index.html    单个游戏薄壳（URL `/games/<id>`）
├── js/                      现有模块 + shell.js / portal.js / account.js / leaderboard.js / games/*.js
└── css/                     现有 tokens/layout + portal.css / games.css
```

### 3.2 现状 → 目标 迁移映射
| 现状 | 目标 | 阶段 |
|---|---|---|
| `static/index.html`（聊天页） | `static/chat/index.html`，URL `/agent` | P0 |
| `/`（聊天页） | Portal 首页 `/`；聊天移到 `/agent` | P0（带灰度开关，见 §11.1） |
| `/api/history` 等一堆平铺 API | 新 `/api/agent/*` 同实现别名；旧路径保留 | P0 |
| `webui.Channel.tokens`（JSON） | `account` 包 + `auth_sessions` 表（JSON 仅作过渡期回退） | P0→P1 |
| 账号 = 名字字符串（会话主键） | `users.id`；名字只是 `username` 属性 | P1→P2 |
| `web:c2c:<name>[:<conv>]` | `web:user:<ID>[:<conv>]`（`--migrate-portal`） | ✅ 已上线 |
| `workspace/web-files/<name>/` | `workspace/web-files/u<id>/`（迁移脚本） | P2 |
| `internal/webui`（聊天 + 通道 + 页面） | `internal/web/agent`（纯机械改名） | P2 |

---

## 4. 数据模型（SQLite）

全部加在现有 `storage` 的 schema 里（`CREATE TABLE IF NOT EXISTS`，向前兼容）。

```sql
-- 4.1 用户：唯一身份。username 只是属性，主键用自增 id（改名/道具/成就都不动其它表）
CREATE TABLE IF NOT EXISTS users (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    username      TEXT    NOT NULL UNIQUE,     -- 登录名（当前唯一凭据；大小写敏感，与现有目录/会话键一致）
    display_name  TEXT    NOT NULL DEFAULT '', -- 展示名（v1 = username；为将来改名预留）
    created_at    INTEGER NOT NULL,
    last_seen_at  INTEGER NOT NULL,
    pin_hash      TEXT    NOT NULL DEFAULT '', -- 预留：可选 PIN（不填=免密）；v1 不实现校验
    disabled      INTEGER NOT NULL DEFAULT 0   -- 预留：封禁/停用
);

-- 4.2 登录会话：Portal 唯一鉴权来源（token 只存 hash）
CREATE TABLE IF NOT EXISTS auth_sessions (
    token_hash   TEXT    PRIMARY KEY,          -- sha256(token) hex
    user_id      INTEGER NOT NULL,
    created_at   INTEGER NOT NULL,
    expires_at   INTEGER NOT NULL,
    last_seen_at INTEGER NOT NULL,
    user_agent   TEXT    NOT NULL DEFAULT '',
    ip           TEXT    NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_auth_sessions_user ON auth_sessions(user_id);

-- 4.3 游戏记录（预留；字段含义待游戏类型确定后再定，先按"原始记录"设计）
CREATE TABLE IF NOT EXISTS game_runs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id     INTEGER NOT NULL,
    game_id     TEXT    NOT NULL,
    score       INTEGER NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    metadata    TEXT    NOT NULL DEFAULT '',   -- 预留回放/种子（JSON，≤2KB）
    created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_game_runs_rank ON game_runs(game_id, score DESC, created_at ASC);
CREATE INDEX IF NOT EXISTS idx_game_runs_user ON game_runs(user_id, game_id, created_at DESC);

-- 4.4 积分流水（预留；积分模型未定，实施时机见 §8/§10）
CREATE TABLE IF NOT EXISTS point_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL,
    amount     INTEGER NOT NULL,               -- 可正可负
    source     TEXT    NOT NULL,               -- game / daily / admin / redeem …
    ref_id     TEXT    NOT NULL DEFAULT '',    -- 例如 game_run:<id>
    created_at INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_point_events_user ON point_events(user_id, created_at DESC);
```

**与现有表的关系**
- `web_conversations.account`（名字）在 P2 迁移后改为 `user_id INTEGER`（保留 `account` 列做过渡，读时优先 `user_id`）。
- `messages.session_id` / `summaries.session_id` / `reminders.session_id` / `tool_audit.session_id` 的字串值已整体重命名（`web:c2c:<name>` → `web:user:<ID>`，2026-09-25），表结构不变。
- `identity_links` 完全不动（`/bind` 的 MC 身份绑定继续用名字维度）。

**清理策略（可选，待定）**：`game_runs` 保留每人每游戏最近若干条；`auth_sessions` 过期行按天清理；`point_events` 永久保留（量小）。

---

## 5. 认证与会话

### 5.1 登录流程（名字即账号，无密码）
```
用户输入名字
   → POST /api/auth/login {name}
   → ValidAccountName 校验（复用现有规则：1-24、字母/数字/中文/_-/.，字母数字开头）
   → portal.enabled && allowRegister ? users 里不存在则创建 : 不存在则拒绝
   → 生成 32 字节随机 token（hex）
   → INSERT auth_sessions(sha256(token), user_id, expires_at=now+portal.sessionDays)
   → Set-Cookie: mineportal_session=<token>; HttpOnly; SameSite=Lax; Path=/; Max-Age=...
   → 响应 {name, admin, userId}
```
- 登录后浏览器访问 `/`、`/agent`、`/api/agent/*`、`/api/games/*` 全部自动带 cookie。
- `GET /api/auth/me` → `{userId, name, admin, createdAt}`（`GET /api/me` 作为旧别名保留）。
- `POST /api/auth/logout`：删除当前 session 行 + 清 cookie（旧 `/api/logout` 同实现）。
- 多设备：同一账号允许多个 session（不互踢），保留清理上限（如 10 个，最旧淘汰）。

### 5.2 中间件
```go
RequireUser(next)   // 解析 cookie/Authorization → users 行 → 注入 ctx
RequireAdmin(next)  // 在上述基础上要求名字命中 cfg.Web.AdminUsers（v1 保持配置驱动，暂不加 users.role）
```
解析顺序（兼容过渡）：`mineportal_session` cookie → `Authorization: Bearer <token>`（新会话 token 或旧 tokens.json token）→ 旧 cookie `mineagent_token`。
旧 token 命中时走**惰性升级**：查 `users`（不存在则创建）→ 建 `auth_sessions` 行 → 回种新 cookie。

### 5.3 与现有 webui 的关系
- P0：`internal/account` 提供 `Login/Logout/CurrentUser`，`internal/webui` 的 token 逻辑改为**委托**给它（`tokens.json` 仍双写，保证旧页面/旧 token 可用）。
- P1：`tokens.json` 只读（用于一次性导入 `auth_sessions`），不再写入。
- P2：删除 `tokens.json` 相关代码路径。

### 5.4 信任模型与风险（必须在门户页/文档里写清）
- 无密码 = **知道名字即可登录该账号**（含 Agent 会话、游戏战绩、积分）。这是私人朋友服务器的合理取舍。
- 预留 `users.pin_hash`：将来可开"有 PIN 必须校验"，无需迁移。
- 管理权限仍由 `config.json` 的 `web.adminUsers` 决定（不进数据库），避免两处真相。

---

## 6. HTTP API

### 6.1 路由总表（新 → 旧别名 → 状态）

| 新路径 | 旧别名（保留） | 方法 | 说明 |
|---|---|---|---|
| `/api/auth/login` | `/api/login` | POST | 登录/注册（名字） |
| `/api/auth/logout` | `/api/logout` | POST | 注销当前 session |
| `/api/auth/me` | `/api/me` | GET | 当前用户 |
| `/api/account` | — | GET | 资料 + 统计（会话数、游戏数、总积分、各游戏最高分） |
| `/api/account/prefs` | `/api/prefs` | GET/POST | 模型/思考强度（Agent 偏好挂在账号下） |
| `/api/account/options` | `/api/options` | GET | 技能/模型目录（Agent 用） |
| `/api/agent/conversations` | `/api/conversations` | GET/POST | 会话列表/新建（**POST 保留但不推荐**，草稿语义） |
| `/api/agent/conversations/delete|rename` | 同名 | POST | 删除/重命名 |
| `/api/agent/history` | `/api/history` | GET | 历史（conv/before/after） |
| `/api/agent/send` | `/api/send` | POST | 发消息（不带 conv = 草稿新会话） |
| `/api/agent/clear` | `/api/clear` | POST | 清空会话 |
| `/api/agent/events` | `/api/events` | GET | SSE（progress/message/cleared/conversations） |
| `/api/agent/upload` | `/api/upload` | POST | 上传（落 `workspace/web-files/u<id>/`） |
| `/api/agent/msgfile` | `/api/msgfile` | GET | 附件下载（按消息归属校验） |
| `/api/agent/workspace` | `/api/workspace` | GET | 浏览 workspace（管理员） |
| `/api/agent/workspace/file` | 同名 | GET | 下载 workspace 文件（管理员） |
| `/api/usage` | 同名 | GET | 模型额度（门户卡片也用） |
| `/api/portal/apps` | — | GET | 应用注册表（导航/卡片数据源，新增应用只改这里） |
| `/api/portal/home` | — | GET | 首页聚合（欢迎语 + 各应用卡片数据） |
| `/api/games` | — | GET | **预留骨架**：游戏目录（+ 我的最近记录，规则字段待定） |
| `/api/games/{id}/runs` | — | POST | **预留骨架**：落一局原始记录 `{score?, durationMs?, metadata?}` |
| `/api/games/{id}/leaderboard`、`/api/games/{id}/me`、`/api/leaderboard` | — | GET | **待定**（积分/排行模型未定，见 §8） |
| `/api/version` | — | GET | 版本自检（保持免鉴权 + no-store） |

**约定**
- 统一错误体：`{"error":"..."}`；401 = 未登录（前端跳登录）、403 = 无权限、400 = 参数、429 = 限流。
- 所有 `/api/*`（除 `login`、`version`）都过 `RequireUser`。
- 旧路径保留至少一个发布周期；前端全部切换到新路径后，再在下一个版本删除（删除时更新本文档）。

### 6.2 Portal 聚合接口（预留扩展点）
```
GET /api/portal/apps
→ { apps:[{id,name,desc,icon,path,order,adminOnly,enabled,card:{type,...}}] }
   门户导航与首页卡片全部由它驱动；新增应用 = 注册一条 + 提供页面，
   前端不需要改（卡片类型见 §7.5）

GET /api/portal/home
→ { greeting, apps:[{id, card:{...}}], account:{name,totalPoints?} }
   服务端聚合各应用卡片数据（同一个应用只暴露一个 card 函数），
   避免前端逐应用发请求；某个应用失败不影响整页（该项返回 {error} ）
```

### 6.3 Games API（预留骨架，规则待定）
- v1 只实现：`GET /api/games`（目录）、`POST /api/games/{id}/runs`（落一条**原始记录**）。
- 提交体只约定"原样留档"型字段：`{score?:int, durationMs?:int, metadata?:object}`，
  服务端做**最小**校验（游戏存在、JSON 合法、metadata ≤2KB、基础节流）。
- **不做**：积分换算、PB 判定、排行榜聚合、上限/时长等反作弊规则 —— 规则层设计
  放在"游戏类型确定后"的独立设计里（见 §8）。

## 7. 页面与前端结构

### 7.1 页面路由（Go 侧映射，白名单，不做 SPA fallback）
> 下表左列是 **URL 路径**（浏览器地址），右列才是**内嵌文件位置**；二者不是同一层概念。
> 当前前端资源根目录是 `internal/webui/static/`（Go 里 `//go:embed static`）。

| URL（浏览器地址） | 文件（仓库内路径，相对 `internal/webui/static/`） | 说明 |
|---|---|---|
| `/` | `portal/index.html` | 门户首页（未登录也可看，卡片点击需登录） |
| `/login` | 复用门户内登录弹层 | 也可独立页 |
| `/agent` | `chat/index.html` | 现在的 ChatGPT 风格页（左栏会话 + 消息 + composer） |
| `/games` | `games/index.html` | 游戏大厅（卡片 = `/api/games` 数据） |
| `/games/<id>` | `games/<id>/index.html` | 游戏薄壳页（加载 `js/games/<id>.js`） |
| `/leaderboard` | `leaderboard/index.html` | 各游戏榜 + Portal 总积分榜 |
| `/account` | `account/index.html` | 资料、积分明细、游戏战绩、会话入口 |

### 7.2 Portal Shell（共享）
- `static/js/shell.js`：顶栏（品牌 / 导航 / 账号菜单 / 主题切换）、登录弹层、登录态守卫。
- Agent 页保留"应用内全屏"体验（进入 `/agent` 后不显示门户大导航；左上角 `← Mine` 回门户）。
- 主题/资源版本自检沿用现有机制（`__VER__`、`/api/version`、`no-store`）。

### 7.3 游戏前端 SDK（每个游戏一个 ES 模块）
```js
// static/js/games/<id>.js
export default {
  id: '2048',
  mount(container, api) { /* 初始化渲染、键盘/触摸输入 */ },
  dispose() { /* 清理计时器/监听 */ },
  // 平台封装（规则未定，只保证"记录 + 反馈"）：
  //   api.finish({ score?, durationMs?, metadata? })  一局结束调用一次
  //   - 自动带鉴权、失败给出 toast、最近记录展示
  //   - 是否给积分/是否进榜：待积分与排行榜模型确定后由平台统一加，不改游戏代码
};
```
- 大厅与游戏页只做"壳 + 记录提交"，不做游戏逻辑复用。
- 游戏内"本机最高分"可放 localStorage 提升手感；服务端 `game_runs` 只作为**原始记录**留存。

### 7.4 调试模式
- `?ui=1`（假数据渲染）扩展到门户/游戏：`?ui=1&page=portal|games|leaderboard|account`，用于无头截图验收。

### 7.5 门户扩展点与预留接口（重要）
门户还会加别的功能，**新增功能一律走"注册一个应用"，不改门户骨架**。

**1) 应用注册表（Go 侧唯一入口）**
```go
// internal/portal/apps.go
type App struct {
    ID        string // agent / games / minecraft / files / …
    Name      string
    Desc      string
    Icon      string
    Path      string // 应用页 URL
    Order     int
    AdminOnly bool
    Enabled   bool
    // Card 返回首页卡片数据；nil = 不在首页展示。
    // 约定：失败必须返回 error，由门户降级为该卡片显示"暂不可用"。
    Card func(ctx context.Context, user *account.User) (any, error)
}
func Register(app App)          // 各应用在 main 装配时自注册
func Apps() []App               // /api/portal/apps 的数据源
```
- 新应用接入清单：① 在 `main.go` 调用 `portal.Register(...)`；② 提供页面目录 `static/<app>/index.html`；
  ③ 自己的业务 API 挂 `/api/<app>/*`（统一过 `RequireUser`）。**前端门户无需改动**。

**2) 首页聚合（服务端拼装，前端只渲染）**
- `GET /api/portal/home` 由门户逐个调用 `App.Card`，单应用失败不影响整页。
- 卡片用统一迷你 schema（前端按 `type` 渲染，未知类型降级为纯文本）：
```json
{ "type": "stat|list|progress|text|link", "title": "...", "items": [...], "value": 42, "hint": "..." }
```

**3) 导航与账号页插槽**
- 顶栏导航由 `/api/portal/apps` 生成；账号菜单项固定（资料/外观/退出）。
- `/account` 页支持"区块插槽"：应用可导出 `AccountBlocks(user) []Block`（同样走注册表），
  例如游戏战绩、Agent 会话统计、MC 绑定状态。

**4) API 命名空间约定**
- 每个应用一个前缀：`/api/agent/*`、`/api/games/*`、`/api/<app>/*`；
  账号相关固定在 `/api/auth/*` 与 `/api/account/*`。
- 中间件、错误体、限流、访问日志全部复用 Portal 层，应用不重复实现。

**5) 预留但本期不做**
- `static/<app>/` 的**外部/第三方页面挂载**（iframe 或 JS 挂件）——预留 `App.Embed = true` 时的
  `frame-ancestors` 与 CORS 策略位，具体实现放到目标页面确定后。
- 应用级配置开关：`portal.apps.<id>.enabled`（先只支持全局 `portal.enabled`）。

---

## 8. Games 平台（先骨架，规则待定）

> **本期的边界**：只把"平台"搭起来——能注册游戏、能打开游戏页、能把一局结果
> 原样记下来。**积分模型、排行榜口径、反作弊策略、首批游戏清单，全部待游戏类型
> 确定后再定**（届时另出设计，文档 §8.3 只列选项，不做承诺）。

### 8.1 游戏注册表（Go，代码定义，不做后台管理）

> **实施偏差（2026-09-25）**：首个游戏是**在线对战的国际象棋**（用户选择），
> 所以最终是"服务端权威 + 房间"而不是本节的纯前端模块；注册表在
> `internal/games/games.go`（ID/名称/路径/启用/人数），前端模块契约（`mount/api.finish`）
> 这一层**没有实现**（不需要：规则和状态都在服务端）。下面的示例保留作将来
> "纯前端小游戏（如 2048/贪吃蛇）"接入时的参考。

```go
// internal/games/registry.go —— 平台唯一需要"定"的东西就是这层契约
type Game struct {
    ID          string // 2048 / snake / memory / …
    Name        string
    Description string
    Icon        string
    Path        string // /games/<id>
    Enabled     bool
    Order       int
    // 预留，不实现：积分/排行榜/反作弊相关字段（ScoreCap、PointsRule…）
    // 等游戏类型确定后按需再加，加字段不影响已注册的游戏。
}
var Games = []Game{ /* 先放 1 个：2048（只为验证链路） */ }
```

### 8.2 前端游戏模块契约（同样保持最小）
```js
// static/js/games/<id>.js
export default {
  id: '2048',
  mount(container, api) { /* 渲染 + 输入 */ },
  dispose() {},
  // api.finish({ score, durationMs, metadata })   一局结束调用；
  // 平台只做"记录 + 反馈"，不承诺积分/排行行为（规则未定）。
};
```
大厅与游戏页只做壳：加载模块、透传 `finish`、展示"最近记录"（原始分数列表）。

### 8.3 规则层选项（**不实现，仅供将来选型时讨论**）
| 维度 | 可选方案 | 说明 |
|---|---|---|
| 积分 | 无积分 / 每局固定 / 按分数换算 / 按名次给分 / ledger 流水 | ledger 可审计，扩展性最好 |
| 排行榜 | 每游戏独立榜 / 总积分榜 / 周期榜（日周月） | 与积分口径绑定 |
| 反作弊 | L0 上限+节流 / L1 回放重算 / L2 服务端权威 | 取决于游戏类型（棋类/休闲/多人差异大） |
| 首批游戏 | 2048 / Snake / Minesweeper / Memory / Tetris / 你画我猜 … | 建议先 1 个休闲类验证链路 |

### 8.4 数据（预留）
`game_runs` 先按"原始记录"落库（§4.3）；`point_events` **本期不建**，
等积分模型定了再加（新增表不影响已有数据）。

## 9. Agent 迁移方案

### 9.1 P0/P1：零破坏兼容
- `/agent` 指向现有聊天页；`/` 指向门户（`portal.enabled=false` 时 `/` 仍回聊天页 → 回滚开关）。
- 现有 `/api/*` 全部保留，新增 `/api/agent/*` 别名（同一 handler，不复制逻辑）。
- 会话键继续 `web:c2c:<name>[:<conv>]`；`webui.Channel` 的登录/token 委托给 `internal/account`，`tokens.json` 双写。

### 9.2 P2：`user_id` 化（一次性迁移，服务停机窗口）
新增 CLI：`mineagent --migrate-portal`（幂等；先自动备份 sqlite 到 `data/mineagent.db.bak-<ts>`）
1. 建 `users`：来源 = `auth_sessions` ∪ `web_conversations.account` ∪ `tokens.json` ∪ `identity_links(platform='web')`。
2. 重写会话键（事务内）：
   - `messages.session_id`：`web:c2c:<name>` → `web:user:<id>`；`web:c2c:<name>:<conv>` → `web:user:<id>:<conv>`
   - 同理 `summaries.session_id`、`reminders.session_id`
   - `web_conversations`：新增 `user_id` 列并回填（`account` 列保留只读）
3. 迁移文件目录：`workspace/web-files/<name>/` → `workspace/web-files/u<id>/`（`os.Rename`，跨设备则复制+校验+删除）。
4. 迁移后校验：随机抽查 session 读写、会话列表、附件下载、提醒调度。

**兼容窗口**：迁移后仍接受旧登录名（`username` 不变），只是内部键换成 `user:<id>`。不做"改名"。

### 9.3 迁移后 Agent 侧改动
- `webSessionKey(userID, conv)` 取代 `webSessionKey(name, conv)`；上传目录、workspace 浏览路径同步。
- 审批/审计里的 `requester=web:<name>` 保持名字（可读性优先），仅会话键用 id。

### 9.4 改名问题（明确取舍）
不做改名。理由：名字已渗透到会话键、文件目录、审计记录。`users.display_name` 已预留，将来若要做，只需改展示层；`username` 仍作为登录名与历史键的锚点。

---

## 10. 实施阶段与验收

| 阶段 | 交付 | 验收标准（可自动/手工） |
|---|---|---|
| **P0 基座** | `internal/portal`（路由/中间件/壳）、`internal/account`、`users`+`auth_sessions` 表、`/agent` 搬家、`/api/agent/*` 别名、登录委托 | `/` 门户可开；`/agent` 与旧聊天完全一致；旧 `/api/*` 仍工作；登录一次跨页有效；`portal.enabled=false` 时 `/` 回到聊天页；`tokens.json` 旧 token 仍能登录（惰性升级） |
| **P1 门户与账号** | 门户首页（Agent/Games/Leaderboard/Account 卡片）、`/account`、`/api/account`、主题/导航共享、`tokens.json` 停写 | 首页 4 卡片数据真实（积分/游戏数/会话数）；账号页展示资料+统计；无密码登录体验不变 |
| **P2 Portal 扩展点 + Games 骨架 + 2048** | `internal/portal` 应用注册表与 `/api/portal/*` 聚合接口、`internal/games`（registry + 原始记录）、大厅、`/games/2048` 可玩、`game_runs` 落原始记录 | 链路验证：登录→玩 2048→结束自动落一条 `game_runs`→`/api/games` 能看到我的最近记录；新增一个"应用卡片"只需注册、不改前端；`/api/portal/home` 聚合正常（某应用失败不影响整页） |
| **P3 规则层（待游戏类型确定后另立项）** | 积分模型 / 排行榜 / 反作弊 / 更多游戏 | 届时另出设计（§8.3 选项表）；文档与 README 同步 |
| **P4 可选** | `/agent/c/<conv>` 深链接、`?ui=1` 门户/游戏调试、PIN 登录（可选）、MC 信息卡、嵌入挂件（iframe/JS）、更多门户应用 | 按需立项 |

**每阶段都要求**：`go test ./...` 全绿；`make build` 通过；用无头浏览器（chrome-headless-shell）截图验收关键页面；改动同步 README 与本文档。

---

## 11. 兼容、回滚与运维

### 11.1 灰度与回滚
- 配置开关：`portal.enabled`（默认 `true`；置 `false` 时 `/` 仍回聊天页，其余新功能仍可按 URL 访问）。
- `auth_sessions` / `users` 为纯新增表，旧代码路径不读它们 → 回滚只需换回旧二进制（P0/P1 阶段）。
- P2 的 `user_id` 迁移**不可逆**（有备份），因此迁移独立成 CLI，仅在确认 P0/P1 稳定后执行。

### 11.2 数据备份
- 迁移/大改前：`sqlite3 data/mineagent.db ".backup data/mineagent.db.bak-<ts>"`；目录改动前 `tar` 一份 `workspace/web-files`。
- 部署脚本可加一步自动备份。

### 11.3 观测
- 访问日志（现有 `msgfile` 式轻日志）扩展到登录、分数提交（谁、哪个游戏、分数、是否 PB）。
- `GET /api/health`（可选）：进程存活 + sqlite 可读 + 各通道状态摘要（复用 `/api/usage`、`/status`）。

---

## 12. 资源与性能预算
| 项 | 预估 |
|---|---|
| 新增常驻内存 | < 5 MB（只是一些 map 与 handler） |
| 新增 CPU（空闲） | 0（游戏逻辑全在浏览器） |
| 磁盘 | 每局约 100 B；1000 局/周 ≈ 500 KB/月（可清理） |
| 单查询 | 排行榜 = 带索引聚合，几千行 <5 ms |
| 前端增量 | 门户/大厅/账号页共约 30 KB JS + 3 个游戏各 5–15 KB（无依赖） |

---

## 13. 已拍板（2026-09-25）

1. `/` 直接变门户（聊天移 `/agent`），带 `web.portal=false` 回滚开关 —— **是**。
2. 账号**不加密码**，`users.pin_hash` 只预留不校验 —— **是**。
3. `user_id` 迁移**本期做** —— **已完成**（`--migrate-portal`，先备份、幂等）。
4. 门户首批挂：**公告栏 + MC 状态卡** —— **已上线**。
4.1 小游戏首个做**国际象棋**，且是**在线房间对战**（用户拍板），实现走"最简可用"路线 —— **已上线**（§14 P2）。
5. 积分/排行榜/反作弊：**待游戏类型确定后再定**（§8.3 只列选项）。

## 14. 实施记录

| 阶段 | 状态 | 落地 |
|---|---|---|
| P0 账号基座 | ✅ 2026-09-25 | `internal/account`（users/auth_sessions，token 存 sha256，30 天/5 设备）、旧 `tokens.json` 惰性导入 |
| P0 会话键迁移 | ✅ 2026-09-25 | `web:c2c:<名字>` → `web:user:<ID>`（messages/summaries/reminders/tool_audit/sessions/conversations）、`web-files/<名字>` → `web-files/u<ID>`、`--migrate-portal`（VACUUM INTO 备份 + 幂等） |
| P1 门户壳 | ✅ 2026-09-25 | `internal/portal`（页面白名单路由、App 注册表、`/api/portal/apps`+`/home` 聚合、`/api/auth/*`、`/api/account/sessions`）、`static/{portal,account}/` + `shell.js`/`portal.js`/`account.js`/`portal.css` |
| P1 公告栏 + MC 卡 | ✅ 2026-09-25 | `announcements` 表 + 管理员发布/删除；MC 状态卡经网关 `minecraft_server_status` |
| P2 Games 骨架 + 首个游戏 | ✅ 2026-09-25 | **首个游戏改为国际象棋（用户拍板）**：`internal/games`（注册表 + 内存房间 + SSE + `/api/games/chess/*`）、`internal/games/chess` 服务端权威规则引擎（走子/将杀/逼和/易位/升变；v1 不含吃过路兵）、`/games` 大厅 + `/games/chess` 棋盘页、`game_runs` 原始记录 |
| P3 规则层 | ⏳ 待定 | 积分/排行榜/反作弊（§8.3）；chess 特殊规则补全（吃过路兵/50 回合/三次重复）可随时做 |
| P4 可选 | ⏳ 待定 | PIN、深链接、MC 信息卡、嵌入挂件、更多应用 |

**实施中的偏差（与设计稿不同之处，以代码为准）**：
- 会话键用 `web:user:<用户ID>`（不是 `web:<user_id>`），上传目录 `u<ID>`；
- 门户首页卡片由 `App.Card` 统一产出（服务端聚合），没有单独的 `/api/account/overview`；
- `AccountBlocks` 暂未实现（账号页目前是内置的资料/设备两块，够用再说）；
- `/leaderboard` 页面未建（游戏规则未定，先不占位）；
- webui 包暂未改名 `internal/web/agent`（P2 再机械搬迁，避免这次 diff 过大）。

---

## 附录 A：现有 API 清单（Portal 化参考）
`/`、`/api/{login,logout,version,me,history,clear,events,upload,send,msgfile,options,prefs,workspace,workspace/file,conversations,conversations/delete,conversations/rename,usage}`、`/static/*`；`/ws`(:8765) 与 `/wecom`(:80) 独立。

## 附录 B：配置样例（新增段）
```json
{
  "portal": {
    "enabled": true,
    "sessionDays": 30,
    "allowRegister": true,
    "games": {
      "enabled": true,
      "dailyRunLimit": 200,
      "minIntervalMs": 5000
    }
  }
}
```
（`web.*` 段保持不变；`web.adminUsers` 仍是管理员唯一来源。）
