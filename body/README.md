# MineAgent Body 0.1.0

独立 Node 身体执行器：现有 Go agent 通过 `body_*` 工具控制一个真实 Minecraft 玩家，Node 不运行模型。管理员在门户 `/agent` 提出任务，在 `/body` 查看状态、审批、暂停、恢复或断开。默认关闭，首次连接、重连、死亡、传送和控制失联后保持暂停，需要管理员明确恢复并重新发起任务。

当前门户主流程为 **Minecraft → AI 玩家 → 对应玩家资料**：在 `/ai/<id>` 中查看身体状态、提交任务/读取回复、审批和控制，按稳定玩家 ID 校验。`/body` 独立保留为兼容入口，复用同一身体组件，即使独立资料服务不可用也能登录、处理 `#game-code=` 和急停。网页权限仍由主站密码/Passkey 会话验证；独立内容管理员和只读 profile 凭据不能操作身体。此次调整不改变 Node/Paper 协议或执行策略。

## 第一版范围

| 工具 | 行为 |
|---|---|
| `body_status` | 后端直接确认控制连接、握手、游戏在线、暂停、新鲜度与任务预算；不依赖游戏动作通道 |
| `body_observe` / `body_inventory` / `body_find_blocks` | 状态、附近实体/事件、自己的背包、已加载方块 |
| `body_goto` / `body_look_at` / `body_stop` | 有界步行、看向目标、立即停止并锁存暂停 |
| `body_dig` / `body_place` | 单方块挖掘/放置，需要交互距离、支撑和区域许可 |
| `body_interact` | 门、按钮、拉杆、床、重生锚；交互时清空主手，副手须空 |
| `body_use_item` | 安全食物；审批后可尝试水桶、岩浆桶、打火石、火焰弹 |
| `body_say` / `body_command` | 公开聊天；以身体玩家权限提交命令 |

寻路不会挖路、搭柱、自动开门或跑酷。不提供容器、攻击、骑乘、交易、实体交互、持续跟随或脱离管理员任务的自主行动。高危清单不表示其中每种物品都已支持使用：未知用途直接拒绝。食物、木门、木头/木板和未点燃 TNT 放置已在真实测试服验证；液体/点火/跨维度床锚仍需专门隔离验收。命令和聊天返回 `submitted`；副作用无法确认时返回 `unknown` 并暂停，不能自动重试。

高危审批绑定 Node 内部保存的不可变动作和一次性 `specId`，120 秒内由已验证网页管理员批准。执行前复核目标、支撑、双手、槽位、数量、邻近危险、朝向、世界和控制代次。原目标变化、会话撤销、预算超时或急停都会使旧动作失效。普通命令由服务器原生权限决定，身体不能是 OP；指令仍可能有区域外副作用，必须先限制其 LuckPerms 组。

## 持续任务与连接诊断

身体任务默认 `body.maxSteps=0`、`body.runTimeoutSec=0`，不设置任务总轮数和总时长上限，也不叠加普通聊天的 300 秒超时。任务复用现有 Go Agent 的“观察 → 行动 → 核验”循环，模型应持续推进到完成或真实阻塞，不因步骤多要求用户反复说“继续”。可显式设置正数预算；普通 MC、QQ 等通道预算保持原样。游戏内委托仍受已验证授权的有效期限制，长任务宜在网页提交。

单次动作仍有超时，准备动作/审批仍短期有效，WorldEdit 出生点保护和权限不变。动作配额耗尽时，后端等待配额恢复后继续，等待可被取消；失败预留不消耗其他类型配额。成功写入清零连续失败计数，连续三次执行失败停止；未知副作用不自动重试。

`body_status` 从后端直接返回 `controlConnected`、`ready`、`gameConnected`、`paused`、`stateStale`、控制代次和任务预算，游戏端离线也可查询。`minecraft_player_info` 只证明 Paper 看见玩家，不证明身体控制会话可用。后端拒绝动作会附真实连接/代次、任务代次及 `phase=backend_preflight`，不能把 `stale` 等同于断线。

暂停期间提交且尚未行动的任务可以跟随同一连接紧接着的一次“恢复”；重复恢复已在运行的身体不会递增代次。停止、断线或撤权会终止整个模型任务，包括思考与限速等待，旧任务不自动重放。只读观察与背包使用当前通道，不赋予旧任务行动权限。服务重启后仍保持暂停，需管理员恢复并提交新任务。

## 模型 API：复用 MineAgent

AI 玩家直接复用 Go 后端中同一个 Agent 和现有 `model.baseURL`、`model.name`、`model.apiKey`；也沿用对应的 `MINEAGENT_MODEL_*` 环境变量覆盖。游戏内身体面板使用默认模型；网页玩家资料中的任务沿用主站账号的模型/思考强度偏好，未选择时使用默认值。无需另配 body 模型 API，也不要向 Node 执行器注入模型 API Key。

Node 只接收身体工具动作。现有模型凭据失效会同时影响主助手与 AI 玩家；应在 MineAgent 的原私有配置中更新，并通过正常部署流程重新加载，不能靠重启 Node 或另建模型调用解决。

## AI 建造权限与出生点保护（Paper 0.3.2）

服主可用 LuckPerms 为身体玩家分配 `mineagent_builder` 专用组，精确节点见 [权限清单](permissions/builder.json)。该组开放自己的创造/生存切换、give/item、飞行开关、治疗/饱食、位置/物品查询，以及 WorldEdit 选区、填充/替换、基础形状、复制/剪切/粘贴、移动/堆叠、撤销/重做。不授予 OP、权限管理、停服、sudo、execute、原版 fill/clone/setblock、WorldEdit 脚本、区块删除、重生成或保护绕过。WorldEdit 不是无风险命令，只按已验证管理员的明确建造任务使用。

正式批量编辑授权须在保护插件生效后进行：MineAgent 通过 WorldEdit 公共 `EditSessionEvent` 和 Extent API，在 BEFORE_HISTORY 和 BEFORE_CHANGE 两层拒绝身体对主世界出生点四周各 512 格（含边界、全部高度）的实际写入。保护随出生点变更更新，其他玩家不受此 AI 专用规则影响；配置无法确认时拒绝该 AI 的 WorldEdit 写入。跨界操作可能已在区外产生部分改动，但不能改动区内；需核查结果，不能自动重试。复制粘贴、移动、撤销、实体与生物群系写入同样受检查。

在 Paper 的私有 MineAgent config.yml 启用（示例不含后端密钥）：

```yaml
worldedit-protection:
  enabled: true
  player: MineAgentBot
  world: world
  radius: 512
```

必须安装兼容的 WorldEdit 并重启 Paper 后再授予批量写入节点。启用保护却缺少 WorldEdit 时，MineAgent 游戏桥接拒绝启动，不能带着失效保护继续下发 AI 动作。0.3.2 将同一保护扩展到 AI 的真实破坏/放置事件（含多方块放置），并通过已认证 Paper 玩家查询上报实时边界。后端在单方块执行前复核该状态；旧插件未上报时，仅拒绝挖掘/放置，不影响移动与观察。这不是面向所有玩家的全局领地系统。

`body_command` 接受普通命令 `give ...` 或 `/give ...`；WorldEdit 按游戏完整写法传递，如 `//pos1 100,64,100`、`//set stone`、`//paste`、`//undo`。Go 和 Node 保留 WorldEdit 双斜杠，准备后的命令不可被后续参数替换。命令输出仍为不可信回显，`submitted` 不代表已修改成功。建造流程建议将 `body.limits.commandsPerMin` 设置为 30，支持连续选区/编辑/核查；普通动作预算与暂停/审批不变。

编译使用 WorldEdit 7.4.5 公共 core API（compileOnly，不打入 jar）；已安装同版本完整插件 jar 时，可使用 Gradle `-Pworldedit_jar=/path/to/WorldEdit.jar` 或 `ORG_GRADLE_PROJECT_worldedit_jar` 提供编译/测试依赖。

隔离实际链路验收：在原有 body-e2e 命令前设置 `MINEAGENT_WORLDEDIT_TEST_JAR=/path/to/WorldEdit.jar`。仅可使用已有隔离标记、回环端口与 m0-world 的测试目录；脚本会向该测试服复制 WorldEdit、写入测试专用 permissions.yml，并验证区外成功、区内/边界/跨界选区拒绝、带位移粘贴/移动/撤销保护，不读取正式权限数据或修改正式世界。

## 配置与启动

需要 Node ≥22、Java 25、已连接并认证的 MineAgent Paper 插件。已验证组合及资源测量见 [M0_RESULTS.md](M0_RESULTS.md)。26.2 暂用回环 ViaProxy 桥接；不改动生产服务器版本或安装服务端协议插件。

锁文件将传递依赖 `uuid` 覆盖为 11.1.1，以修复 [GHSA-w5hq-g745-h8pq](https://github.com/advisories/GHSA-w5hq-g745-h8pq)。已核对认证依赖调用的是兼容的 `v4` API；本版仍仅支持离线认证，不把离线 E2E 当作微软登录流程验证。2026-10-20 `npm ci` 审计为零告警，后续升级须重新检查。

1. 在独立测试环境先构建 Go：`make build BUILD_OUTPUT=/tmp/mineagent-body-v1`。本仓库 `bin/` 是正式运行目录，开发验证不要覆盖它。
2. `cd body && npm ci`；复制 `config.example.json` 到权限为 0600 的私有文件。后端和 MC 目标必须是显式回环地址。
3. 后端 `allowedRoles` 显式包含 `minecraft-body`；主 Paper 密钥与 `MINEAGENT_BODY_TOKEN` 都至少 32 字符且彼此独立，Paper 插件配置与主密钥一致。不要把实际密钥放入仓库、模型上下文或命令行参数。
4. 后端 `body.enabled=true`、先 `mode=observe`，设置 `name`、`worldMappings` 和 `zones.areas`。后端配置示例见下方。按维度唯一映射世界，动作前通过现有 Paper 查询核对实际版本、玩家世界、位置及非 OP 身份；无法确认则拒绝执行。
5. 启动固定版本 ViaProxy，仅监听回环。按其 CLI 参数将 `127.0.0.1:25566` 转到目标 Paper 回环端口，目标版本为 `26.2`，使用 `--log-ips false --chat-signing false`。给 Java 至少 `-Xmx320M`；关闭不需要的 ViaBedrock 资源包 HTTP 托管。不要复制整个生产配置作为测试夹具。
6. 通过私有环境文件向 Go 和 Node 注入相同的 `MINEAGENT_BODY_TOKEN`，运行 `node src/index.js --config /path/to/private-body.json`。网页管理员必须用密码/Passkey 验证登录，只有名字的旧会话不能控制。
7. 在 `/body` 检查状态，再验证急停；准备隔离区域和服务器备份后切 `mode=assist`，显式恢复，从 `/agent` 发起短任务。

后端片段（合并到测试配置，不覆盖其他字段；默认值由 Go 补齐）：

```json
{
  "allowedRoles": ["minecraft", "minecraft-body"],
  "body": {
    "enabled": true,
    "mode": "observe",
    "id": "main",
    "name": "MineAgentBot",
    "expectedMcVersion": "26.2",
    "worldMappings": [{"dimension": "minecraft:overworld", "worldId": "body-test-world"}],
    "zones": {"mode": "allowlist", "areas": [
      {"name": "yard", "worldId": "body-test-world", "min": [-32, -64, -32], "max": [32, 100, 32]}
    ]},
    "profile": {"enabled": false, "hideDimension": true}
  }
}
```

`zones` 现在只约束挖掘/放置的目标方块，不约束身体站位、路径、观察、聊天或命令。`zones.mode=unrestricted` 明确取消这层编辑白名单限制，仍遵守 Paper 出生点保护；`allowlist` 空列表仅拒绝单方块编辑。WorldEdit 根据服务端实际写入目标保护，不使用玩家当前位置判断。

`body` 的其他配置与实际默认值在 `internal/config/body.go`：任务总轮数与总时长默认不限（两个预算值均为 0），单动作最多 20 秒，grant 最长 5 秒；allowlist 空列表时没有允许的单方块编辑目标。防护、路径规则和 PvP/容器禁用为固定规则，没有通过未实现字段开启的后门。配置更改沿用现有重启加载流程，无动态编辑接口。

## 门户与只读资料

管理员 API：`GET /api/body/status|events|approvals|audit`、`POST /api/body/control`（`action`）、`POST /api/body/approvals/<id>`（`approved`）、`POST /api/body/drain`。写入须 JSON、同源 cookie 或请求头 Bearer；SSE 使用 cookie，不接受 URL token。控制接口绕过 agent 队列和动作锁，2 秒内等 Node ACK，未确认返回 `stop_unconfirmed`，不能当作已停。

`GET /api/body/profile` 是固定白名单快照，不含坐标、背包、聊天、审批理由。启用 `body.profile.enabled` 后，外部服务用独立只读 `MINEAGENT_BODY_PROFILE_TOKEN` 的 Bearer 请求；Go 加载时只保存其 SHA-256，也可直接配置 `body.profile.tokenHash`。令牌轮换通过私有环境文件和正常后端部署完成。管理员也只能从此接口拿到同一资料投影。论坛本体与写作工具不在本版实现。

## 游戏内 AI 玩家面板（Paper 0.3.0）

输入 `/agent` →「AI 玩家」，无需客户端 mod；`/agent ui` 可选 MineUI 增强。身体未启用或后端不支持时显示实际状态，不自动启动 Node、代理或修改区域。

1. 完整状态与控制需要 `mineagent.use`、`mineagent.body`（默认 OP），并关联已验证的网页管理员。游戏内 OP 本身不能控制身体。
2. 点击「关联管理员」获取 2 分钟一次性代码；打开网站 `/body`，密码/Passkey 登录管理员，核对玩家 UUID 与当前游戏连接后确认授权。链接中的代码仅在 URL fragment 中传递，页面读取后清除，不能自动授权。
3. 回到游戏点击「刷新身体状态」。授权最长 10 分钟；完整状态含生命、饥饿、手持、位置、当前任务；可下达短任务、看本人回复、查看并逐条审批身体动作、暂停/恢复/断开和急停。原版面板用刷新按钮获取新状态与回复。
4. 任务复用同一个 Agent 与 13 个 `body_*` 工具，使用独立 `bodyui:<网页用户ID>:<当前连接>` 会话，任务和回复不向公共 MC 聊天广播。面板保留本连接最近 12 段消息，重连/撤权清空；独立会话记录仍使用既有 SQLite 存储，不包含关联代码、密码或网页原始令牌。
5. 授权绑定已认证 Paper WS、玩家 UUID 和插件为本次连接生成的随机 nonce。离线、Paper/后端断连、权限撤销、网页登录退出、授权到期或主动解除都会撤销；正在运行的游戏面板任务被取消并请求暂停。重新关联不能复活旧任务/旧审批，恢复后仍须重新下达任务。

暂停/急停绕过模型队列，只有收到现有身体 ACK 才确认；`stop_unconfirmed` 明确显示结果未确认，不自动重试副作用。暂停/恢复会更新控制代次。高危动作仍通过原有不可变 spec/一次性 grant 审批，游戏身体审批和普通 MC 指令审批分开。面板任务不暴露 workspace、网页管理或其他通道工具。

需同时部署支持 `body.ui.request/response` 的 Go 后端和 Paper 插件。后端部署仍走 `scripts/deploy.sh` 的对局与 drain 检查，Paper 安装后重启需单独确认窗口；本功能不会自动启用正式身体，活动世界与区域须在启用时明确。

## 运行与部署

建议分别管理 Go、Node、ViaProxy 进程，Node 内存上限 512M，ViaProxy 上限至少 768M（实测 RSS 约 455MiB，原设计 384MB 估计不足）。这里不自动安装 systemd 单元，也不读取正式配置。WS 控制租约每 500ms 续期，Node 超过 1.5 秒未收到即停止主动输入；这不能撤回已发出的游戏包或消除液体、爆炸、重力后果。

后端正式部署必须使用 `scripts/deploy.sh`：先检查棋局，再调用认证 drain 阻止新动作并等待空闲，确认后才替换二进制。检查失败不替换、不重启；drain 后部署失败需管理员在 `/body` 恢复。`--force` 会跳过两项检查，只能在明确授权时使用。降级可切回 `mode=observe`；停用为 `enabled=false` 并停止专用 Node/代理进程。不要为验证本功能重启 Paper。

## 验证

```sh
go test ./...
go test -race ./internal/body ./internal/bodyui ./internal/ws ./internal/agent ./internal/tools ./internal/account ./internal/storage ./internal/webui ./internal/portal ./cmd/mineagent
python3 -m unittest discover -s scripts -p 'deploy_test.py'
cd body && npm test
```

真实 E2E 使用 `node scripts/body-e2e.mjs <disposable-paper-dir> /tmp/mineagent-body-v1 <ViaProxy-3.4.14.jar>`，需要根目录已有 Playwright 和当前版本 Paper 插件 jar；第四个参数会启动仅回环的测试 ViaProxy（25666 → 25665），不提供时须已有测试代理。测试目录必须有包含 `isolated test` 的 `.mineagent-body-test` 标记，且 Paper 为 `paper-26.2-124.jar`、全新平坦世界 `m0-world`、仅回环 25665、离线模式、关闭出生保护。脚本会更改这个测试世界、清空测试玩家背包和覆盖其中的测试插件配置；不要指向正式目录。它创建私有临时后端、测试账号、模拟模型和数据库，真实完成网页聊天到工具动作、审批恢复、陈旧目标拒绝、急停、资料权限、手机浏览器，以及原版面板授权→任务→真实身体执行→面板审批→急停→同名重连撤权验证，结束后只关闭自身子进程。

当前已完成隔离环境验证，24 小时长期运行、固定负载的 MSPT/CPU/换页对比及生产权限/保护验收仍是上线门槛，不能由 30 分钟兼容性测试代替。

## 后续客户端 mod

第一版无需玩家安装 mod。后续客户端可承载选中 AI、任务面板、状态 HUD、视角或手动接管。届时客户端只向后端提交经认证的意图，后端继续负责控制权与策略；手动接管必须先暂停 agent、增加控制代次并清掉旧动作，再授予短期控制权，断线默认暂停。MineUI 已有 UI 能力时优先复用其公开 API；仅在实际客户端需求确定后增加适配，不在本版引入第二套控制协议或修改其他项目。


## 2026-10-20 正式试运行

18:09 CST 已按服主授权启动：Paper 插件 0.3.0 与后端代码 `20cc29b` 握手成功，`MineAgentBot` 真实进入正式服。服主明确要求默认不设活动区域限制，正式配置使用 `assist` + `zones.mode=unrestricted`；首次连接依旧暂停，需已验证管理员恢复。未授予 OP。

身体由 `mineagent-body.service` 管理，本地 26.2 版本桥接由 `mineagent-body-proxy.service` 管理，桥接仅回环 25566；私有配置目录为 `/home/ubuntu/.local/share/mineagent-player-services/`，正文不记录凭据。长期运行与生产保护完整验收仍未完成，本次属于服主授权的生产试运行。

部署前原二进制、插件与两端配置已保存在 MC `backups/mineagent-players-20261020-180623/`（受限权限）。首次部署通过 `scripts/deploy.sh`，其 `CONFIG` 指向部署前备份以准确读取原运行实例的 body-disabled 状态；检查无棋局后替换。后续部署应使用当前正式配置的默认检查及 drain，不继续使用旧备份跳过 drain。


### 2026-10-20 20:07 CST 模型配置恢复

按服主提供的新凭据更新 MineAgent 原有私有 `model.apiKey`，未为身体建立另一套模型配置。原端点的模型目录、默认模型最小对话、函数工具调用均返回 HTTP 200，工具调用仅检查返回结构，未执行生产游戏动作。旧私有配置和后端二进制已备份到身体服务私有目录下的 `backups/model-key-20261020-200635/`。

使用 `scripts/deploy.sh` 检查无进行中棋局、确认身体 drain 后重新加载，运行代码版本 `93c003f`。后端、身体执行器、版本桥接和资料服务均 active，AI 玩家公开资料 online/idle 且非陈旧；首次重连仍暂停，不自动恢复任务。先前 UI 发布记录中的 401 expired 为此次配置恢复前的历史问题。凭据不输出、不入库、不提交 Git。

### 2026-10-20 21:07 CST 建造权限与出生点保护上线

服主授权后，后端通过 `scripts/deploy.sh` 的对局与 drain 检查部署 `e1c77d3`，Paper 经正常 `stop` 后重启，21:03:48 启动完成。插件 0.3.1 确认主世界出生点半径 512、全部高度保护 `ready=true` 后，才开放 WorldEdit 写入节点。当前出生点保护方形为 X -354～670、Z -553～471，含边界；后续随世界出生点更新。

已将 `permissions/builder.json` 的 62 个允许节点、23 个拒绝节点授予独立 `mineagent_builder` 组，并核对 LuckPerms 导出与清单完全一致、AI 继承该组且非 OP。有效权限检查确认建造、粘贴、撤销、give、创造模式可用，权限管理、脚本与 execute 不可用。重连后 AI 曾回到生存模式，已切回创造模式；公开资料 online/idle 且非陈旧，不自动恢复身体任务。

原配置、二进制、旧插件及权限变更前后导出保存在身体服务私有目录的 `backups/builder-permissions-20261020-204117/`。WorldEdit 跨界、粘贴、撤销/重做等真实写入已在隔离 Paper 中完成端到端验证；正式世界未用批量写入进行测试。长期运行和性能验收仍待后续完成。


### 2026-10-20 21:40 CST 持续任务修复上线

后端 `f7f7c3d` 经 `scripts/deploy.sh` 的无对局及身体 drain 检查部署，未重启正式 Paper，也未更改模型凭据、权限组或保护范围。旧二进制备份位于身体服务私有目录 `backups/continuous-tasks-20261020-213936/`。正式配置原本未显式填写任务预算，本次加载新默认值后为不限总轮数/总时长。

21:40–21:41 CST 线上核查：四个相关服务 active，身体控制连接/握手正常、状态新鲜，AI 保持创造模式。通过独立管理员自检会话调用真实模型，body_status、body_observe、body_inventory 均完成；观察和背包审计均为 confirmed/ok，无 stale，未发起任何世界写入。后端重连后保持暂停，需管理员明确恢复并提交新任务。

验证包括 96 轮真实 Eino 工具循环、40 轮隔离 Minecraft 观察、13 工具/75 场景真实端到端测试、相关包竞态检查、Go 全仓回归（跳过既有固定延迟性能用例）、静态检查和 10 项部署脚本测试。隔离测试和上线自检不能替代长期运行及规划质量验收。


### 2026-10-20 方块编辑保护与移动分离（Paper 0.3.2）

修复 Go `inZone` 漏掉 unrestricted 分支，导致正式配置不限制活动区域但所有动作仍被空白名单拒绝的问题。Go/Node 的区域检查仅用于 body_dig/body_place 的目标方块；移除路径、身体站位和速度预测中的区域围栏，也不把放置支撑面当成被改动目标。移动仍不自动挖路、搭柱或开门。

Paper 的 512 格保护共用实时出生点边界，在 Bukkit 实际破坏/放置事件及 WorldEdit Extent 拦截写入。后端读取 Paper 玩家信息中的 blockProtection，在派发前返回明确的 spawn_protected 或 protection_unavailable，避免让模型误报为缺少移动授权。配置/保护状态未知时仅拒绝编辑；连接、管理员认证和急停继续独立生效。
