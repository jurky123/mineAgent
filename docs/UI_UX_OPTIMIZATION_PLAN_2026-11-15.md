# zkun.art / mineAgent 视觉统一与交互体验优化实施指导计划

日期：2026-11-15  
目标仓库：`/home/administrator/.gemini/antigravity/scratch/mineAgent`  
设计基线：`DESIGN.md`（纸与墨 v2）、`PRODUCT.md`、`docs/ui-contract.json`

---

## 0. 核心约束与验收红线（必读）

在执行本计划的任何修改前，必须严格遵守仓库现有的自动化验收契约（见 `scripts/paper-ink-checks.mjs`、`scripts/navigation-rail-ui-test.mjs`、`scripts/community-ui-test.mjs`、`scripts/review-ui-test.mjs`）：

1. **禁止丢失 DOM 契约属性**：
   - `scripts/paper-ink-checks.mjs` 会比对 `internal/webui/static/**/*.html` 与 `main` 分支中所有的 `id`、`aria-label`、`aria-labelledby`、`aria-describedby`、`aria-live`、`aria-hidden`。
   - **原则**：只增不减、不改名现有 HTML 中的上述属性值。
2. **禁止同一 CSS 文件同一媒体作用域内出现重复选择器**：
   - `scripts/paper-ink-checks.mjs`（第 56-61 行）会遍历 `tokens`、`ds`、`community`、`portal`、`games`、`layout`、`chat`、`composer`、`overlays`、`prompts`、`downloads`、`screens`、`body` 共 13 个 CSS 文件，断言**同一 CSS 文件、同一 `@media` / base 作用域内不得出现重复的 `selectorText`**！
   - **原则**：修改 CSS 时必须在原选择器规则块内合并修改，严禁在文件末尾追加同名选择器覆盖。
3. **保持 `tokens.css` 与 `ds.css` 主站与 AI 玩家站字节级完全一致**：
   - `scripts/paper-ink-checks.mjs`（第 11 行）断言 `internal/webui/static/css/tokens.css` 与 `aiplayer/static/css/tokens.css`、`internal/webui/static/css/ds.css` 与 `aiplayer/static/css/ds.css` 完全相等。
   - **原则**：凡是修改 `internal/webui/static/css/tokens.css` 或 `ds.css`，必须同步复制覆盖到 `aiplayer/static/css/`。
4. **保持现有 E2E 测试断言通过**：
   - `scripts/navigation-rail-ui-test.mjs` 断言了 `.rail-home` 数量为 `0`，且普通用户直接访问 `/agent/library` 会看到 `需要管理员权限` 标题。
   - **原则**：通过 `hidden` 属性控制 `#rail-workspace` 对非管理员隐藏，但保留 DOM 节点与直接路由访问时的权限提示；移动端返回门户入口放在 `.section-page-head` 或侧栏抽屉顶部/底部（不使用 `.rail-home` 类名）。
5. **只本地修改与测试，不推送远端、不触碰生产服务**。

---

## 1. 视觉风格统一（Visual Unification）

### 任务 1.1：清除 `overlays.css` 对全局 `.btn` 的覆盖污染，统一「纸与墨」按钮与 Toast
- **问题定位**：
  - `internal/webui/static/css/overlays.css` 第 42-46 行及第 94-122 行重新定义了 `.btn`、`.btn:hover`、`.btn:active`、`.btn.primary`、`.btn.primary:hover`、`.btn.danger`、`.btn.danger:hover`、`.btn:disabled`。
  - 因为 `chat/index.html` 在 `ds.css` 之后加载 `overlays.css`，导致 `/agent` 下所有按钮丢失了 `ds.css` 的 `1.5px` 墨线描边和硬投影，且 `.btn.primary` 在深色模式下变成浅蓝 `#7C83FF` 配白字（违反 `DESIGN.md` 固定 `#5B63F0` 配白字的对比度要求）。
- **修改方案**：
  1. 在 `internal/webui/static/css/overlays.css` 中：
     - **删除**第 42-46 行的 `.btn { ... }` 块和第 94-122 行的 `.btn:hover` 至 `.btn:disabled` 块，让 `/agent` 全面使用 `ds.css` 的 `.btn` 体系。
     - 检查 `overlays.css` 中 `.modal` 与 `.modal-card`（第 2-31 行）：将其升级为符合「纸与墨」规范的样式：
       - `.modal` 增加 `backdrop-filter: blur(3px);`
       - `.modal-card` 使用 `background: var(--surface); border: var(--bw) solid var(--line); border-radius: var(--r-modal); box-shadow: var(--shadow-hard-lg);`
       - `.ws-head` 底部描边改为 `border-bottom: var(--bw) solid var(--line);`
  2. 修复 `/agent` 中的 `#toasts` 容器样式（`internal/webui/static/js/ui.js` 使用 `#toasts` 插入 `.toast.ok` / `.toast.err`）：
     - 在 `internal/webui/static/css/overlays.css` 中新增 `#toasts` 定位与状态样式（注意确保选择器唯一）：
       ```css
       #toasts { position: fixed; left: 50%; bottom: 26px; transform: translateX(-50%); z-index: 260; display: flex; flex-direction: column; gap: 8px; pointer-events: none; max-width: calc(100vw - 32px); }
       #toasts .toast.ok { border-color: var(--ok); }
       #toasts .toast.err { background: var(--danger-soft); color: var(--danger); border-color: var(--danger); }
       ```

### 任务 1.2：补齐 `/agent/library` 与 `/agent/tools`（`agent-sections.css`）的纸墨硬投影与粗墨线
- **问题定位**：
  - `internal/webui/static/css/agent-sections.css` 中的控件（`.section-button`、`.section-order`、`.section-icon-button`）使用 `1px solid var(--line-soft)` 无投影，卡片（`.capability-card`、`.resource-grid .resource-row`）无硬投影，在点阵纸背景上显得扁平；`.capability-card > .section-button` 硬编码了 `margin-left: 52px`。
- **修改方案**：
  1. 在 `internal/webui/static/css/agent-sections.css` 中：
     - `.section-button`、`.section-order`、`.section-icon-button`：将 `border: 1px solid var(--line-soft)` 升级为 `border: var(--bw) solid var(--line); box-shadow: var(--shadow-hard-sm);`，并添加 `:hover:not(:disabled)` 的轻微上浮与 `:active:not(:disabled)` 的下沉效果（或直接对齐 `.btn` 的物理反馈）。
     - `.capability-card` 与 `.resource-grid .resource-row`：增加 `box-shadow: var(--shadow-hard-sm); transition: transform var(--motion-fast) var(--ease-out), box-shadow var(--motion-fast) var(--ease-out);`，并在 `:hover` 时设置 `transform: translate(-1px, -1px); box-shadow: var(--shadow-hard);`。
     - `.resource-icon` 与 `.capability-status`：将 `border: 1px solid var(--line-soft)` 改为 `border: var(--bw) solid var(--line);`。
     - `.capability-card > .section-button`：移除生硬的 `margin-left: 52px;`，改为 `align-self: flex-start; margin-top: 2px;`（在窄屏下更自然）。

### 任务 1.3：深色模式（Dark Mode）高饱和色块与刺眼对比度打磨
- **问题定位**：
  1. `internal/webui/static/css/portal.css` 第 94 行 `.hero-agent .prompt-chip:hover` 使用了 `background: var(--paper-bright); color: var(--ink);`，在深色模式下 `--paper-bright` 是 `#201F29`（黑底），悬停在蓝卡上会变成突兀的黑块。
  2. `internal/webui/static/css/community.css` 第 163 行 `.download-card` 整卡背景使用了 `var(--tone-sun-soft)`，深色模式下整张大卡片变成暗黄褐色（`#3A3218`）。
  3. `internal/webui/static/css/community.css` 第 17 行 `.server-connect` 在深色模式下背景仍是刺眼的亮青柠 `#B9EC4F`，而里面的按钮 `.btn` 却是深色 `#201F29` 底配白字。
  4. 五子棋缩略图背景在 `portal.css`（第 118 行）、`community.css`（第 120 行）、`games.css`（第 191 行）硬编码了亮木色 `#E9C98F`，未像真实棋盘那样在深色模式下压暗为 `#B89B66`。
- **修改方案**：
  1. 在 `portal.css` 中，将 `.hero-agent .prompt-chip:hover` 的 `background: var(--paper-bright); color: var(--ink);` 改为 `background: #FFFDF9; color: #18171F; border-color: #18171F; box-shadow: 2px 2px 0 #18171F;`（始终保持亮纸贴片质感，且对比度充足）。
  2. 在 `community.css` 中：
     - 将 `.download-card` 的 `background: var(--tone-sun-soft);` 改为 `background: var(--surface);`，让卡片主体保持干净的纸面色（内部的 `.badge.warn` 依然是醒目的日光黄）。
     - 为 `.server-connect .btn:not(.primary)` 明确指定亮色纸底与墨字：`background: #FFFDF9; color: #18171F; border-color: #18171F; box-shadow: 2px 2px 0 #18171F;`，确保在青柠色票据背景上按钮干净协调。
  3. 在 `tokens.css` 中新增棋盘木色变量 `--board-wood: #E9C98F;`（亮色）与 `html[data-theme="dark"] { --board-wood: #B89B66; }`（并同步到 `aiplayer/static/css/tokens.css`），然后将 `portal.css`、`community.css`、`games.css` 中的 `#E9C98F` 替换为 `var(--board-wood)`。
  4. **注意**：不要修改 `tokens.css` 中 `--tone-mc`、`--tone-sun`、`--tone-games`、`--on-accent` 的根变量值，因为 `scripts/paper-ink-checks.mjs` 第 36 行会对它们与 `--on-accent` 做 `ratio >= 4.5` 断言。

### 任务 1.4：统一 AI 玩家站（`aiplayer/`）顶栏视觉与主题切换体验
- **问题定位**：
  - `aiplayer/static/index.html` 与 `aiplayer/static/css/portal.css` 的顶栏 Logo 过小（22px 无边框无硬投影）、导航无胶囊高亮、右侧「主题」按钮是简陋的文字按钮。
- **修改方案**：
  - 在 `aiplayer/static/css/portal.css` 中，将 `.topbar`、`.brand-logo`（`34px`、圆角 `10px`、`1.5px` 墨线描边、`2px 2px 0` 硬投影、悬停旋转 `-3deg`）、`.nav-item.active`（使用 `var(--surface)` 背景 + `1.5px` 墨线 + `var(--shadow-hard-sm)` 硬投影，对齐主站 `.nav-pill` 视觉）与主站 `ds.css` 对齐。

---

## 2. 交互逻辑与 UX 顺手度修复（UX & Logic Fixes）

### 任务 2.1：修复国际象棋大厅与回放页的两处「五子棋硬编码」Bug + 键盘方向键支持
- **问题定位**：
  1. `internal/webui/static/js/gameroom.js` 第 575 行：空房间列表下的「观看演示回放」按钮硬编码了 `replay.href = '/account/history?demo=gomoku';`，导致在国际象棋大厅点击后跳到了五子棋回放。
  2. `internal/webui/static/js/history.js` 第 262 行：演示回放提示条硬编码了 `'当前为只读演示对局，不计入个人战绩；终局连线表示五连。 <a href="/games/gomoku">退出演示</a>'`，导致访问 `/account/history?demo=chess` 时也显示五子棋规则且退出跳回五子棋。
  3. `internal/webui/static/games/chess/index.html` 第 49-82 行：`#lobby` 内残留了旧版静态 HTML，而 `gomoku/index.html` 是空的 `<div id="lobby" hidden></div>`。
- **修改方案**：
  1. 在 `internal/webui/static/js/gameroom.js` 第 575 行：改为 `replay.href = '/account/history?demo=' + gameId;`。
  2. 在 `internal/webui/static/js/history.js` 中：
     - 支持 `/account/history?demo=chess` 的演示数据回放（若 `DEMO_MODE` 时 URL 参数为 `demo=chess`，提供一局简短的国际象棋演示数据或根据 `run.gameId` 动态渲染提示文案：当 `run.gameId === 'chess'` 时显示 `'当前为只读演示对局，不计入个人战绩。 <a href="/games/chess">退出演示</a>'`，当 `run.gameId === 'gomoku'` 时保留原五子棋文案与链接，确保 `review-ui-test.mjs` 的 `demo=gomoku` 测试 100% 兼容）。
  3. 在 `internal/webui/static/games/chess/index.html` 中：清空 `#lobby` 内部的冗余静态节点（保留 `<div id="lobby" hidden></div>`，与 `gomoku/index.html` 一致；先检查 `paper-ink-checks.mjs` 是否有 `id`/`aria-*` 钩子依赖，若有则保留对应属性或确认无依赖后再精简——注：`chess/index.html` 的 `#lobby` 内部没有任何 `id` 或 `aria-*` 属性！）。

### 任务 2.2：解决移动端在 `/agent/library` 与 `/agent/tools` 无法返回门户首页的死胡同
- **问题定位**：
  - 在 `≤600px` 视口下，`#shell-top` 被隐藏（`layout.css` 第 269 行），而进入 `/agent/library` 或 `/agent/tools` 时聊天主头部（含 `#home` 返回门户按钮）也被隐藏（`agent-sections.css` 第 1 行），导致手机端用户在这两个页面完全找不到返回门户 `/` 的入口。
- **修改方案**：
  - 在 `internal/webui/static/js/agent-sections.js` 的 `ensureShell()`（第 42-48 行）中，在 `.section-page-head` 右侧（`#section-count` 旁边）或左侧增加一个简洁的「回门户」链接按钮 `<a class="section-button section-home-link" href="/" aria-label="回门户">回门户</a>`（桌面端可隐藏或常驻，移动端 `≤600px` 必定显示；注意不要使用 `.rail-home` 类名以免触发 `navigation-rail-ui-test.mjs` 的 `assert.equal(await page.locator('.rail-home').count(), 0)`）。

### 任务 2.3：优化 `/agent` 导航栏与重复入口、隐藏非管理员的无效「资料空间」图标、支持触屏管理会话
- **问题定位**：
  1. `internal/webui/static/js/app.js` 第 67 行：`if ($('rail-workspace')) $('rail-workspace').hidden = false;` 强制对非管理员也展示左侧导航栏的「资料空间」图标，普通用户点进去只能看到权限报错。
  2. `internal/webui/static/js/navigation-rail.js` 第 14-16 行：在桌面端 `/agent` 会话页点击已激活的 `#rail-conversations` 图标时，只能展开侧栏，不能再次点击收起侧栏。
  3. `internal/webui/static/css/chat.css` 第 92-95 行：`.conv-actions` 仅在 `:hover` / `:focus-within` 显示，触屏设备（`@media (hover: none)`）无法点击重命名/删除会话。
- **修改方案**：
  1. 在 `internal/webui/static/js/app.js` 第 67 行：改为 `if ($('rail-workspace')) $('rail-workspace').hidden = !S.admin;`（管理员可见，非管理员隐藏；注意检查 `navigation-rail-ui-test.mjs`：测试中 `isAdmin = true` 时点击 `#rail-workspace`，第 92 行切为 `isAdmin = false` 后是直接 `page.goto(base + '/agent/library')`，不依赖点击 `#rail-workspace`，因此完全兼容！）。
  2. 在 `internal/webui/static/js/navigation-rail.js` 第 14-16 行：当在 `/agent`（非独立 section 页）且桌面端（`window.innerWidth > 600`）点击 `#rail-conversations` 时：
     - 如果当前侧栏是收起状态（`document.body.classList.contains('collapsed')`），则展开并 `focus('#conv-search')`（保持 `navigation-rail-ui-test.mjs` 第 74-75 行测试通过！）；
     - 如果当前侧栏已经是展开状态且搜索框未聚焦，则聚焦 `#conv-search`；若再次点击或提供清晰的切换体验，保持测试契约100%兼容。
  3. 在 `internal/webui/static/css/chat.css` 中，增加 `@media (hover: none) { .conv-actions { opacity: 1; pointer-events: auto; } }`，让手机/平板用户可以直接看到并点击会话的重命名与删除按钮。

### 任务 2.4：优化对局房间 `<details class="game-more">` 外部点击关闭与游客登录后无缝建房
- **问题定位**：
  1. `internal/webui/static/js/gameroom.js` 第 403-405 行：`<details class="game-more">` 只绑定了 `Escape` 关闭，点击页面外部不会自动收起。
  2. `internal/webui/static/js/gameroom.js` 第 521 行与第 547 行：未登录游客点击「创建房间」或「加入」完成登录后，代码已经完成了 `await createRoom()` / `await joinRoom(...)` 并渲染了对局界面，随后却执行 `if (loggedInNow) location.reload();`，造成整页白屏二次刷新。
- **修改方案**：
  1. 在 `gameroom.js` 中添加全局 `document.addEventListener('click', (e) => { ... })`，当点击不在 `.game-more` 内部时，自动将所有 `details.game-more[open]` 设为 `open = false`。
  2. 在 `gameroom.js` 第 521 行与第 547 行：移除生硬的 `location.reload()`，改为调用 `shell.RefreshUser?.()`（或在 `shell.js` 中导出/触发顶栏用户状态刷新），让玩家登录后直接丝滑留在刚创建/加入的房间内（务必验证 `portal-refresh-e2e.mjs` 第 23-32 行的创建/加入房间测试通过）。

### 任务 2.5：首页与技能页带 `?q=` 跳转 `/agent` 时的输入框视觉引导 + 命令面板补全 + 二步登录新账号提醒
- **问题定位**：
  1. 从首页或 `/agent/tools` 点击示例指令跳转到 `/agent?new=1&q=...` 时，文字仅填入底部 `#text` 作为草稿，没有任何视觉反馈，新访客容易以为没反应。
  2. `internal/webui/static/js/shell.js` 第 335-343 行的 `Ctrl/Cmd+K` 命令面板漏掉了 `/minecraft`、`/ai`、`/agent/library`、`/agent/tools`。
  3. `internal/webui/static/js/passwords.js` 第 168-175 行：老用户输错账号名进入 `setup`（创建账号）步骤时，缺乏显眼的“未找到同名账号，将注册新账号”提醒。
- **修改方案**：
  1. 在 `internal/webui/static/js/app.js` 的 `consumeEntryParams()`（第 127-143 行）中，当从 URL 读取到 `q` 填入 `#text` 时，聚焦 `#text` 并为 `.composer-box` 添加一个短暂的高亮动画类 `.draft-flash`（在 `composer.css` 中定义 1.2s 的日光黄外发光过渡），引导用户视线到底部输入框。
  2. 在 `internal/webui/static/js/shell.js` 的 `openPalette()` 中补全 `Minecraft 加入指南 (/minecraft)`、`AI 玩家 (/ai)`、`插件与工具 (/agent/tools)` 等导航项。
  3. 在 `internal/webui/static/js/passwords.js` 的 `applyStatus()`（第 168-175 行）中，当 `mode === 'setup'` 且 `!status.exists` 时，将 `#login-step-hint` 文案优化为 `'未找到同名账号，首次登录将为「' + status.name + '」创建新账号（如已有账号请点“换个名字”检查拼写）。'`（注意保留 `portal-refresh-e2e.mjs` 第 28-29 行断言：不包含 `'原有对话和战绩'`，且标题保持 `'创建账号'`）。

---

## 3. 执行与验证流程（Verification Steps）

修改完成后，按顺序在 `/home/administrator/.gemini/antigravity/scratch/mineAgent` 下执行以下验证：

1. **同步镜像文件检查**：
   ```bash
   cp internal/webui/static/css/tokens.css aiplayer/static/css/tokens.css
   cp internal/webui/static/css/ds.css aiplayer/static/css/ds.css
   ```
2. **运行前端与 UI 自动化测试套件**：
   ```bash
   npm run test:paper-ink
   npm run test:navigation-rail
   npm run test:community
   npm run test:review
   npm run test:models
   npm run test:prompts
   npm run test:passwords
   ```
3. **运行 Go 单元测试**：
   ```bash
   go test ./internal/webui/... ./internal/portal/...
   ```
4. **汇总变更清单与测试结果**，确保所有测试 100% PASS，无新增浏览器报错、无横向溢出、无重复 CSS 选择器、无丢失 DOM ID/ARIA 属性。
