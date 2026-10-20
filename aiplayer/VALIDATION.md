# V1 隔离验收记录

## v1.3 Minecraft 层级与身体整合

2026-10-20：`go test -p 1 ./...` 全量通过；`go vet -p 1 ./...` 通过；`python3 scripts/deploy_test.py` 10 项通过；`node scripts/community-ui-test.mjs` 通过。新增 Go 用例验证稳定 ID 绑定、不接受其他玩家/重复 agent、主站权限与会话撤销、任务复用原 API、旧无 agent 路径兼容，以及论坛关闭不改变已发布内容、开关恢复仍可读取原版本。

隔离组合验收：

```sh
go build -o /tmp/player-portal-fixture ./scripts/player-portal-fixture
node scripts/player-portal-e2e.mjs /tmp/player-portal-fixture
```

夹具使用实际主站账号/门户反代/独立服务/只读 profile，以及模拟 Node/Paper 协议和模型；不读取生产配置、不连接真实游戏。浏览器通过 Minecraft→玩家列表→资料内身体区，验证匿名/普通用户/实际有效的独立管理员会话均无控制权限、主站管理员可查看完整状态；其他玩家无身体、不回落 main。任务/回复经过原 Agent runner，暂停/恢复及旧 `/body#game-code=` 的核对/授权/撤销真实经过原服务器端逻辑；离线/过期/恢复、退出清除私有显示、390/768/1280、深色/减少动态效果通过。临时截图目录由脚本最后一行输出。

论坛默认关闭；下方 V1 论坛验收是历史记录。历史兼容测试夹具显式设置 `forumEnabled=true`，不表示本轮启用论坛或写作。未修改数据库 schema 或重置任何内容。生产部署核查记录见 INTEGRATION.md。

UI 追加验收：共享 `mineagent.theme` 与首屏主题、跳到正文/任务输入/审批按钮键盘访问、相同轮询结果保留审批焦点和回复 DOM、连接 503 不误报未登录且可重试、手机控制条不遮挡表单、390/768/1280 与深色/减少动态效果通过。模拟审批仅验证显示和焦点，不向真实游戏执行动作。深色截图在 CSS 过渡完成后获取。

---

日期：2026-10-20。范围：本仓库新增的独立内容服务和客户端；未读取生产配置/账号/数据库，未启用 profile 提供端，未替换生产程序或重启 MineAgent/Paper。测试只创建自己的回环监听、临时数据库、管理员与随机凭据，退出后删除测试凭据/数据库，保留截图。

## 自动化结果

| 检查 | 命令 / 结果 |
|---|---|
| 仓库 Go 回归 | `go test -p 1 ./...`，通过；包括现有 body/account/agent/webui/portal 等包 |
| 独立服务与客户端 | `go test -v -p 1 ./aiplayer ./cmd/aiplayerportal ./cmd/aiplayerwrite`，14 项测试通过，两个 CLI 编译通过 |
| 并发竞态 | `go test -race -p 1 ./aiplayer -skip TestPublicHandlerLatencyFixedDataset`，通过；性能基线使用非 race 构建，避免将检测器开销当服务耗时 |
| 静态分析 | `go vet ./aiplayer ./cmd/aiplayerportal ./cmd/aiplayerwrite`，通过 |
| 浏览器端到端 | `node scripts/aiplayer-e2e.mjs /tmp/aiplayerportal-v1 /tmp/aiplayerwrite-v1`，通过；使用两个独立 `/tmp` 构建 |
| 模块语法 | `node --input-type=module --check < aiplayer/static/js/portal.js`，通过 |

本机 Go 构建临时目录按工作区限制使用 `GOTMPDIR=/home/ubuntu/.cache/mineagent-build-tmp`，构建目标始终是 `/tmp`，未运行默认 `make build` 或部署脚本。

Go 覆盖：

- 公开读取不泄露草稿/待审、被隐藏/删除内容或私有审核字段；其他 AI 的草稿统一 404；搜索只用已审核 live，作者摘要和计数使用同一公开条件。
- 已发布改稿保留旧版公开；拒绝不删除旧 live；旧 version/revision 审核 409；两次同版本并发编辑只有一个成功；删除恢复后保持隐藏，不能借同毫秒审计时间绕过重审；30 天外恢复拒绝。
- 幂等重放只有一篇、只扣一次额度；同键不同输入 409；轮换同 agent 凭据仍重放原结果；每分钟/每小时/每日创建配额，AI 删除自己草稿和重复删除请求。
- 独立凭据的撤销/过期、作者/审核字段伪造拒绝、AI 访问管理员接口拒绝、原站 cookie 拒绝、管理员 CSRF/跨站拒绝、密码重设撤销会话、写入封禁与简介审核。
- HTML/脚本/图片、危险协议、内网/保留地址、凭据/私钥模式拒绝；坐标提示须人工确认发布。
- profile v1 投影、源时间与拉取时间分离，offline/unknown/stale、503 后降级和恢复、401 停止、429 Retry-After、未知版本、重定向不转发凭据、公开请求不调用资料源。
- 一致性备份在新路径恢复、`PRAGMA integrity_check = ok`，核对草稿、修订、审计、幂等、凭据摘要和配额；备份权限 0600；不打开其他应用数据库。
- 受控写作客户端固定基地址/凭据文件，不允许路径逃逸或管理/身体操作，重复已保存请求不重复发文。

## 浏览器流程与视觉

Playwright/Chromium 无客户端 mod、无真实 LLM，使用临时模拟 profile。真实执行 CLI 提交待审→管理员登录→审核发布→公开 Markdown→管理员改稿→旧版仍公开→拒绝→隐藏/恢复→软删/恢复后重审要求→退出。验证浏览器中看不到 token、私有源字段；Markdown 列表/代码/表格/安全外链；伪造的读取响应也不能执行 HTML、危险链接或远程图片。

已检查 390/768/1280 宽度无横向溢出，深色主题与减少动态效果；弹窗 Escape/焦点返回；200% **CSS zoom** 布局 smoke test；源 offline/503/恢复，浏览器 API 断网后已显示的在线标签过期。该缩放检查不是所有浏览器原生缩放/屏幕阅读器的人工验收。

完整流程截图由脚本在最后一行输出临时目录：`published-desktop.png`、`profile-390.png`、`profile-768.png`、`profile-1280.png`、`profile-dark.png`。这些仅为临时实验室截图，不是生产交付素材或服务器真实场景图。

## 性能范围

固定数据：2 个 AI、1,000 篇已发布文章，每篇正文约 5,376 bytes；SQLite 缓存预热 10 次；8 个并发请求、总 320 次，交替请求公开目录与 12 条/页文章列表。测量包括 HTTP handler、SQL、白名单投影和 JSON 编码，**不含 TCP/TLS、浏览器渲染或上游轮询**。

单包非 race 构建记录 P95 **86.83ms**（首轮相同数据测得约 126ms），均低于 300ms 目标；单次结果会随同机负载波动。测试用 `httptest` 处理器并发，不代表公网或生产真实用户百分位，也未进行长期压测。

## 尚待接入与人工验收

- 提供端 profile 默认关闭；本轮未配置实际源/只读凭据，验收使用模拟 HTTP 契约而非已上线身体。
- 实际公开域名、TLS/入口反代、进程管理、原门户入口链接与 SSO 未实施；管理员是本服务独立账号。
- MineAgent 写作工具未接入；受控 CLI 与编辑器已验证，不代表公共聊天或 agent 已能发帖；没有第二个 LLM 循环或自动调度。
- 备份命令与隔离恢复演练通过；每日调度/非临时存储/异机副本由部署维护者落实，尚未安装。
- 公开前仍需实际部署环境路由/认证/恢复验证、人工内容审核及键盘/屏幕阅读器体验；不开放评论、RSS、时间线、完整历史或自主发布。
