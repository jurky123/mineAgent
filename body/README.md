# MineAgent Body 0.1.0

独立 Node 身体执行器：现有 Go agent 通过 `body_*` 工具控制一个真实 Minecraft 玩家，Node 不运行模型。管理员在门户 `/agent` 提出任务，在 `/body` 查看状态、审批、暂停、恢复或断开。默认关闭，首次连接、重连、死亡、传送和控制失联后保持暂停，需要管理员明确恢复并重新发起任务。

当前门户主流程为 **Minecraft → AI 玩家 → 对应玩家资料**：在 `/ai/<id>` 中查看身体状态、提交任务/读取回复、审批和控制，按稳定玩家 ID 校验。`/body` 独立保留为兼容入口，复用同一身体组件，即使独立资料服务不可用也能登录、处理 `#game-code=` 和急停。网页权限仍由主站密码/Passkey 会话验证；独立内容管理员和只读 profile 凭据不能操作身体。此次调整不改变 Node/Paper 协议或执行策略。

## 第一版范围

| 工具 | 行为 |
|---|---|
| `body_status` | 后端直接确认控制连接、握手、游戏在线、暂停、新鲜度与任务预算；不依赖游戏动作通道 |
| `body_observe` / `body_inventory` / `body_find_blocks` | 状态、附近实体/事件、自己的背包、已加载方块 |
| `body_goto` / `body_look_at` / `body_stop` | 有界步行/显式水面游泳、看向目标、取消当前动作但不锁存暂停 |
| `body_dig` / `body_place` | 单方块挖掘/放置，需要交互距离、支撑和区域许可 |
| `body_interact` | 门、按钮、拉杆、床、重生锚；交互时清空主手，副手须空 |
| `body_use_item` | 安全食物；审批后可尝试水桶、岩浆桶、打火石、火焰弹 |
| `body_say` / `body_command` | 公开聊天；以身体玩家权限提交命令 |

寻路不会挖路、搭柱或跑酷；允许空手开木质栅栏门，普通木门需显式body_interact。不提供容器、攻击、骑乘、交易、实体交互、持续跟随或脱离管理员任务的自主行动。高危清单不表示其中每种物品都已支持使用：未知用途直接拒绝。食物、木门、木头/木板和未点燃 TNT 放置已在真实测试服验证；液体/点火/跨维度床锚仍需专门隔离验收。命令和聊天返回 `submitted`；副作用无法确认时返回 `unknown` 并暂停，不能自动重试。

高危审批绑定 Node 内部保存的不可变动作和一次性 `specId`，120 秒内由已验证网页管理员批准。执行前复核目标、支撑、双手、槽位、数量、邻近危险、朝向、世界和控制代次。原目标变化、会话撤销、预算超时或急停都会使旧动作失效。普通命令由服务器原生权限决定，身体不能是 OP；指令仍可能有区域外副作用，必须先限制其 LuckPerms 组。

## 持续任务与连接诊断

身体任务默认 `body.maxSteps=12`、`body.maxConsecutiveReads=3`、`body.reasoningEffort=medium`、`body.runTimeoutSec=0`；不设置任务总时长上限，轮数有界。显式设置 `maxSteps=0` 才不限总轮数，也不叠加普通聊天的 300 秒超时。任务复用现有 Go Agent 的“观察 → 行动 → 核验”循环，模型应持续推进到完成或真实阻塞，不因步骤多要求用户反复说“继续”。可显式设置正数预算；普通 MC、QQ 等通道预算保持原样。游戏内委托仍受已验证授权的有效期限制，长任务宜在网页提交。

单次动作仍有超时，准备动作/审批仍短期有效，WorldEdit 出生点保护和权限不变。动作配额耗尽时，后端等待配额恢复后继续，等待可被取消；失败预留不消耗其他类型配额。成功动作或朝目标净前进至少 0.5 格的部分寻路清零连续失败计数；连续三次无明显进展的执行失败停止；未知副作用不自动重试。

`body_status` 从后端直接返回 `controlConnected`、`ready`、`gameConnected`、`paused`、`stateStale`、控制代次和任务预算，游戏端离线也可查询。`minecraft_player_info` 只证明 Paper 看见玩家，不证明身体控制会话可用。后端拒绝动作会附真实连接/代次、任务代次及 `phase=backend_preflight`，不能把 `stale` 等同于断线。

暂停期间提交且尚未行动的任务可以跟随同一连接紧接着的一次“恢复”；重复恢复已在运行的身体不会递增代次。停止、断线或撤权会终止整个模型任务，包括思考与限速等待，旧任务不自动重放。只读观察与背包使用当前通道，不赋予旧任务行动权限。服务重启后仍保持暂停，需管理员恢复并提交新任务。

## 模型 API：复用 MineAgent

AI 玩家直接复用 Go 后端中同一个 Agent 和现有 `model.baseURL`、`model.name`、`model.apiKey`；也沿用对应的 `MINEAGENT_MODEL_*` 环境变量覆盖。游戏内身体面板使用默认模型；网页玩家资料中的任务沿用主站账号的模型选择；身体任务的思考强度使用 body.reasoningEffort（默认 medium），避免继承网页 high 偏好。无需另配 body 模型 API，也不要向 Node 执行器注入模型 API Key。

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

`body` 的其他配置与实际默认值在 `internal/config/body.go`：任务默认12轮、总时长不限（maxSteps=12、runTimeoutSec=0），单动作最多 20 秒，grant 最长 5 秒；allowlist 空列表时没有允许的单方块编辑目标。防护、路径规则和 PvP/容器禁用为固定规则，没有通过未实现字段开启的后门。配置更改沿用现有重启加载流程，无动态编辑接口。

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

需同时部署支持 `body.ui.request/response` 的 Go 后端和 Paper 插件。后端部署仍走 `scripts/deploy.sh` 的对局与 drain 检查，Paper 安装后按工作区维护授权核查在线人数与特殊任务，无特殊任务时先发全服通告再部署重启，不逐次询问；本功能不会自动启用正式身体，活动世界与区域须在启用时明确。

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


21:58 CST 交付状态：代码 `43a4f15` 已构建为 Paper 0.3.2 并安装到 plugins/MineAgent.jar，尚待服主确认重启窗口，运行中的插件仍为 0.3.1；配套后端/Node 尚未重载。本次原后端、插件和 Node 文件备份在私有目录 `backups/edit-zones-20261020-215816/`。

验证：Go 相关包竞态与全仓回归（跳过既有固定延迟性能用例）、Node 11 项安全用例、插件单元测试通过；隔离 Paper E2E 通过 13 工具/79 场景，实际验证编辑白名单外与出生点保护区内行走、区内单方块预检拒绝、WorldEdit 全边界保护。另用相同 AI 身份的真实非 OP 游戏客户端绕过 Go/Node：区内破坏/放置在 Paper 拒绝，区外真实破坏/放置成功，验证了服务器事件防线。测试产物在 `/tmp/mineagent-body-e2e-ONrDGx`，未在正式世界执行写入验收。


### 2026-10-20 22:16 CST 编辑保护正式生效

服主确认重启，并将工作区维护规则更新为“核查在线人数和特殊任务，无特殊任务则先发全服通告，再部署重启，不逐次询问”。此次核查真人在线 0、门户进行中对局 0，身体空闲且无待审批；全服维护通告后使用 scripts/deploy.sh 完成对局/drain 检查，部署后端 `970c427`，正常 stop/启动 Paper 并重载 Node 执行器。插件 0.3.2 于 22:16:08 启动完成，保护日志 radius=512/allHeights=true/ready=true。

维护后恢复原先已授权的创造模式与运行状态。独立管理员自检会话通过实际模型调用 body_status、观察、body_goto、再次观察；移动审计 confirmed/ok，身体由 (158,75,-41) 实际移动到约 (159.37,75,-40.47)，位于保护区内且没有 out_of_zone。连接新鲜、未暂停，Go/Node/桥接/资料服务均 active。正式世界未执行挖掘/放置测试；相应写入保护已在隔离服按后端工具和绕过后端的真实游戏包两层验收。工作区规则文件仅留在服务器，不提交 Git。


### 寻路失败与部分进展（2026-10-20）

body_goto 仍是受限步行，不因创造模式自动飞行，不挖路/搭桥/开门。NoPath、寻路计算超时、目标变化和被停止分别返回 path_not_found、path_search_timeout、goal_changed、path_stopped，不再都折叠为 failed。未到达时返回 partial/reached、真实位置、剩余/初始距离、净进展、实际位移和最后一次路径搜索状态/节点统计；后续 observe 可查询最近一次 navigation。审计仅保存这些结构化导航字段，不保存原始命令回显或聊天内容。

后端按保存的起点/目标及返回的真实位置计算净进展，不能仅凭 partial=true 或横向晃动免除失败计数。净前进至少 0.5 格可继续调整路线；连续三次没有明显进展仍终止当前任务，停止文案带最近动作及错误码。unknown、停止/代次失效等不因此恢复。应根据地形选择可达的中间地面点，不反复改成更远的固定高度目标盲试。


### 2026-10-20 23:00 CST 导航诊断修复上线

生产原任务最后三次 goto 失败均被旧日志记录为 failed，触发当前任务的连续失败终止。核查连接正常、创造模式生效且无 stale/out_of_zone。无真人/模型任务/门户对局/身体动作或审批后，已发布全服 AI 维护通告，使用 scripts/deploy.sh 部署 `362a896` 并重载 Node，未重启 Paper。原二进制与 Node 源码备份于私有目录 `backups/navigation-20261020-225932/`。

线上只做有限移动复查，不执行世界写入：首次同目标返回 path_not_found/noPath，实际移动 4.41 格、净前进 4.04 格，未误触卡死计数。服务器既有 spawn-on-join 设置使维护重连回到出生点，随后已恢复维护前的位置，再从原起点约 (179.50,75,-49.14) 到原目标 (190,75,-50) 复查一次：路径搜索 402ms、406 个节点后 noPath，剩余距离 10.15 格、实际位移 1.37 格、净前进 0.38 格。确认当前步行策略无法规划完整路线，不是控制通道或区域拒绝；未据此宣称已定位具体地形阻挡，也未增加自动飞行/穿墙或修改全服登录设置。

Node 12 项安全/导航测试、Go 相关包竞态/静态检查与全仓回归（跳过既有固定延迟性能用例）通过。隔离 Paper E2E 覆盖真实封闭地形 noPath 与位置/搜索统计、既有 13 工具/80 场景和写入保护；产物 `/tmp/mineagent-body-e2e-NvjXi0`。

## 2026-10-20 暂停与 workspace 故障诊断

本次在线故障是两条独立链路：身体在收到 `forcedMove` 后锁存暂停；workspace 子进程继承网站服务的 `CAP_NET_BIND_SERVICE`，Bubblewrap 在启动阶段直接拒绝。临时 systemd 单元使用相同 ambient capability 可复现原错误，先通过 util-linux `setpriv` 清除子进程 inheritable/ambient capabilities 并设置 no-new-privs 后，原隔离命令正常执行。网站进程的权限与监听配置不变；缺少 `setpriv` 或 `bwrap` 仍拒绝执行，不降级成宿主 shell。

`body.state` 和 `body_status` 新增 `pauseReason`；强制位置包另记录 `from/to/distance/tool`，仅用于诊断。原事件没有修正前的位置，无法追溯证明是碰撞纠正还是管理员传送。协议本身也不能可靠区分两者，因此小位移、零位移、心跳到达均不会自动解除暂停；认证管理员显式恢复后才接受新动作。暂停会清理准备好的动作并中止当前输入，恢复不重放旧任务。

回归：在与正式服务相同的 `AmbientCapabilities=CAP_NET_BIND_SERVICE` 临时单元运行 workspace 测试，验证沙箱子进程 CapInh/CapPrm/CapEff/CapAmb 均为零、NoNewPrivs 为 1，父进程能力不变；宿主文件/符号链接逃逸、普通命令禁网、缺少隔离依赖、输出上限和超时子进程清理均通过。Go tools/body/bodyui 竞态测试、相关 vet 与 Node 13 项测试通过。

隔离真实链路验收通过：13 工具、80 次模型调用，包含完整世界身份、暂停/恢复、审批、原生面板、连续观察、WorldEdit 和原始客户端挖掘/放置守卫。工件位于 `/tmp/mineagent-body-e2e-2MlY7Q/`，不包含正式世界。

23:55 CST 发布前核查仅机器人在线 1 人，对局、模型任务、身体动作和待审批均为零；发送全服通告后经 `scripts/deploy.sh` 的对局/drain 检查部署后端 `dcf23aa`，重载 Node，未重启 Paper。旧二进制与 Node 源码保存在私有目录 `backups/cap-pause-20261020-235521/`。恢复维护前坐标和创造模式后，通过认证控制接口显式解除暂停，未恢复旧任务。

正式管理员工具自检：`workspace_exec pwd` exitCode=0；审查现有 `cabin_plan.py` 后，用 `python3 -B -c` 导入并调用 `build(0,64,0)` 返回 348 个方块（仅内存计算，未覆盖施工文件）。一次附近 `body_goto` 成功、实际位移约 1.81 格，随后独立 `body_observe` 确认位置改变、创造模式且 paused=false；未挖掘、放置或执行建造命令。恢复位置时的明确传送被新诊断记录为约 23.46 格，说明 from/to/distance 采集已在正式协议链路生效。这次短距验证没有复现原位置包原因，不能据此断言所有位置纠正问题已经消失。


## 身体体验修订（2026-10-21，已部署）

- MC `@agent` 的普通会话只注册 `body_profile`，提供启用、名字、在线、活动与暂停状态，不含坐标、背包或事件。关联已验证网页管理员后，服务端生成的当前游戏 nonce、在线权限与实时管理员会话均有效，才进入独立私密身体会话；OP 或名字不构成授权。断线、撤权、过期后降回公开能力。
- 活动动作中 ≤0.75 格的位置修正不暂停；2秒内累计修正超过0.75格、超过40包、较大位移、无活动动作的位移仍暂停（登录头2秒沿用原有出生保护）。准备快照立即作废；不篡改动作起点或把修正当成进展。管理员 pause/forced_move 保留原任务，明确 resume 后继续规划；急停 stop/disconnect、失联、未知副作用与撤权仍废弃旧写入授权，不能自动重放。
- LLM 的 `body_stop` 使用带 callId 的 cancel 控制帧，只取消当时派发的动作，ACK 后可继续，不改变暂停或 epoch。延迟取消不影响新动作。无法确认清理仍走 fail-closed 急停；挖掘/放置结果未知时也继续要求人工核查。管理员控制 API 的 stop 仍是急停。
- observe 返回 7×7 已加载列摘要和最近3个可站立点；find_blocks 包含 distance/surfaceY/standable 并排序。goto 的坐标形式要求 x,z，y 可省略，只在身体高度上下8格已加载地形中选安全地面；显式高度偏差超过2格返回 target_above_ground/target_below_ground 与 suggestedY。它不是全世界最高点查询，也不保证选中点存在通路。
- 寻路允许冲刺、空手开木质栅栏门（固定的寻路依赖不自动开普通木门，需 body_interact），关闭挖路、搭柱和跑酷；副手非空时拒绝自动开门导航，主手先清空，避免携带危险物品触发交互。搜索半径64、计算5秒，动作总超时不变。
- 每任务连续只读预算默认3，执行动作后重置；相同 run/tool/args 的观察指纹相同时返回 unchanged，继续使用该任务前次数据。不会跨用户缓存完整观察；状态仍随结果返回，缓存最多64项。
- builder 权限清单补 essentials.help/list；发布时需按清单同步现有 LuckPerms 组，源码清单变化不等于正式组已更新。批量建造继续使用原有 body_command + WorldEdit、明确区外选区与验证/undo，不增加保护绕过或任意传送。
- 协议仍为v1，新增可选 runId/cancel.callId/inspect.data/chat.session；后端与 Node、Paper 应配套更新。旧执行器不能确认 cancel 时会安全失败，不能将其当作成功。

验证记录与遗留项见本轮 BODY_UX_FOLLOWUP_2026-10-21.md。历史发布条目保留当时语义，以上修订优先。

Node 身体配置新增 `minecraft.viewDistance`，默认4，整数范围2–8；限制该客户端请求和缓存的区块距离，不修改正式服务器或其他玩家视距。Mineflayer 及26.1注册表在尚未连接时已有约100MiB RSS基础开销，不能把区块视距优化当作移除 Node/ViaProxy。固定版本仍需要 ViaProxy 完成26.1→26.2协议兼容；普通木门仍须显式交互。

完整 body-e2e 测试会额外启动 Paper/ViaProxy/Node/浏览器，不能按 JVM 的 -Xmx 估算总峰值。脚本启动前要求 Linux MemAvailable 至少4GiB；若实际Linux cgroup（含父级）具有≤2GiB硬上限，则要求该上限加1GiB余量。不足时在创建测试进程之前拒绝，无环境变量绕过。示例本轮测试1850MiB硬上限、禁止swap、独立OOM域；不得仅调高限额来绕过资源不足。低成本前置检查：`node --test scripts/body-test-resources.test.mjs`。

`/ai/main` 的私有任务面板复用已有会话 `runEvents`，显示最近20项工具名称、状态与耗时；不显示参数或原始结果。相同轮询不替换节点，切换新会话清空，迟到的旧会话响应被忽略。运行中按1秒刷新，空闲按5秒刷新，请求不重叠；任务忙碌时禁用发送与新会话。返回事件只证明工具返回，实际执行结果仍以身体状态和回复为准。

本轮真实体验补充：建造规划明确采用以实际出生点为中心的X/Z各±512矩形（闭区间、所有高度），整个编辑范围含屋檐必须不相交，不能以中心点或圆形半径判断；未知命令先help，被拒即停，不以清空当回滚。模型仍可能给未经核实的示例坐标和命令，执行前必须核对真实出生点、地面、选区与权限，服务器守卫继续强制保护。真实体验、失败样本、候选提交和未完成项见上述实测文档。

DSH 后端也沿用上述工具身份、审批、暂停恢复和只读预算；身体默认12步/medium，显式 `body.maxSteps=0` 仍不限。官方 DeepSeek 无medium档，身体medium映射到low以保留延迟预算，显式high不降档；普通聊天映射不变。面板直接输入 `/help` 显示网页会话指令并标记为“系统消息”；查询游戏命令请明确要求“以身体执行 /help”，仍由 body_command 检查权限和审批。


### 2026-10-21 配套版本

身体UX修订已发布，后端代码a90cafc、Paper插件0.3.3与Node执行器需配套使用。新增essentials.help/list精确权限节点；审批、非OP/operator gate和出生点/WorldEdit保护保持。重连仍保留暂停，不自动恢复旧任务。默认身体预算12步、连续只读3次、medium（官方DeepSeek映射low），显式maxSteps=0仍不限，Node默认视距4。

隔离验收覆盖Go/vet/race、Node、DSH、浏览器和完整Paper链路。实际发布后的服务检查通过，但不替代真实模型503/429、建造计划坐标/权限核实、24h与同负载性能等余项。完整运维证据保留本机，不提交配置、凭据或运行日志。


## 下一版导航设计（未实现）

[Navigation V2](../MINE_BODY_NAVIGATION_DESIGN.md)定义目标区域解析、分段A*、路线修复、模式切换与验收矩阵。地面优先复用锁定Pathfinder，新增受控创造飞行与水面导航；先交付目标/地面修复，再验证新增运动能力。它不代表当前已会飞、已支持任意木门或复杂跑酷，也不改变现有暂停、审批、写入保护与部署流程。

网页任务思考参数兼容修订：OpenCode Go的DeepSeek V4.1 Flash不接收medium，身体默认medium在发送前映射low；普通聊天medium映射high，显式low/high保留。与官方适配器分别做端到端请求回归，其他模型不套用这一映射。不支持的档位显示明确原因，不能因参数错误自动重放已执行动作。


## 导航首轮修订（2026-10-22）

`body_goto`按整个到达半径检查落点，目标所在列是墙不再直接拒绝；省略y时在已加载的上下32格中解析附近楼面，显式y仍保留，不把悬空目标自动改到水面。支持可选`movement=ground|swim`：默认ground避开水；明确swim允许已知水面游泳和上岸，不接受深水终点、无限落水或未经检查的飞行。水面与未知地形分别返回water_only/terrain_unknown，可操作高度错误保留suggestedY。

导航仍禁挖路、搭柱，最大正常下落3格，不再因为下方是水就忽略下落上限。审批、非OP、operator gate、出生点和WorldEdit写入保护不变。新参数要求Go/Node配套更新；没有新增客户端mod或自动改变游戏模式。完整Navigation V2仍是分阶段提案，本轮不宣称已完成飞行、跟随或任意地形导航。

水面首轮实测迭代：动作清理仍停止全部输入，静止时身体会受正常物理下沉。下一次明确swim移动会先检查当前列及身体宽度内16格上方的已加载开放水面，最多8秒只按跳跃上浮，然后沿水面寻路；不穿顶、不规划水下水平通道、不写坐标。上浮也在同一个动作/租约/取消信号下，停止、暂停、撤权或失联后清空输入；没有任务外的自动悬浮/后台游泳定时器。navigation.surfaceRecovery返回起始高度、水面高度和上浮耗时；四格深真实水池中停手沉底后上浮上岸已验收。

显式swim不使用Pathfinder默认的每步液体惩罚，避免为降低代价而贴岸反复上下水；受控上浮先到水方块Y+0.5再横移，减少岸角卡碰。无进展的失败应换侧向航点，不原样重复同一目标；持续任务预算取body_status.taskLimits，maxSteps=0的显式配置保持不限轮数。

浸水台阶实测修订：碰撞修正采用2秒累计0.75格与40包的双上限，不再仅按第4包暂停；单次大位移、未知起点、无active仍暂停，准备快照仍作废。forced_move事件和body_observe提供onGround/inWater/velocity用于诊断。真实隔离水浸半格坡道登台、往返、大位移暂停和全部保护回归通过。若机器人重连被原生入服规则送回出生点，可为专用身体账号单独配置essentials.spawn-on-join.exempt及目标世界的mv.bypass.gamemode.<world>，保留位置/已获准模式；不改全服规则，不授予传送、OP或写入保护豁免。

中途停在浸水半砖后的起步会识别脚下低于脚面的含水支撑，不把整块半砖当作水柱障碍，并只调整搜索起点的量化高度。空闲时仅已知实心碰撞面、单轴≤0.01格、0.3/0.7碰撞边界夹紧且无按键的舍入修正不触发新暂停；普通空闲移动、未知接触仍暂停，已锁存暂停绝不自行解除；编辑/命令/物品/审批准备项作废，低风险移动仍须执行前重新描述和复核。高台返回水面须沿已知坡道分段，不从侧面直接跳入深水再切岸；该复杂组合仍可能被安全暂停。

当前26.1客户端经代理接26.2的swim动作使用保守预测碰撞裕量（半宽0.32，结束/失败/取消后恢复原值）；若正贴着已知实体面，先用真实按键在当前格内约0.22格退让，最多1秒，再寻路，不写实体坐标。精度包只保留goto/look_at/command准备项，执行前仍完整复核；命令、编辑、物品及审批准备项继续作废。隔离验收使用minecraft:tp精确复现含水半砖停步，准备响应延迟150ms覆盖竞态；原地重新起步与经坡道往返、全部保护回归通过。

水边精度修正不再作废普通命令准备；命令执行仍重新描述世界、位置、双手并校验 epoch/有效期，随后走原生权限和 WorldEdit 写入保护。方块、物品及需审批的动作仍作废，不恢复已暂停身体。新增真实含水台阶命令回归，避免修复自己入口时连续 stale。
