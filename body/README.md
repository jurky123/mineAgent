# MineAgent Body 0.1.0

独立 Node 身体执行器：现有 Go agent 通过 `body_*` 工具控制一个真实 Minecraft 玩家，Node 不运行模型。管理员在门户 `/agent` 提出任务，在 `/body` 查看状态、审批、暂停、恢复或断开。默认关闭，首次连接、重连、死亡、传送和控制失联后保持暂停，需要管理员明确恢复并重新发起任务。

## 第一版范围

| 工具 | 行为 |
|---|---|
| `body_observe` / `body_inventory` / `body_find_blocks` | 状态、附近实体/事件、自己的背包、已加载方块 |
| `body_goto` / `body_look_at` / `body_stop` | 有界步行、看向目标、立即停止并锁存暂停 |
| `body_dig` / `body_place` | 单方块挖掘/放置，需要交互距离、支撑和区域许可 |
| `body_interact` | 门、按钮、拉杆、床、重生锚；交互时清空主手，副手须空 |
| `body_use_item` | 安全食物；审批后可尝试水桶、岩浆桶、打火石、火焰弹 |
| `body_say` / `body_command` | 公开聊天；以身体玩家权限提交命令 |

寻路不会挖路、搭柱、自动开门或跑酷。不提供容器、攻击、骑乘、交易、实体交互、持续跟随或自主长任务。高危清单不表示其中每种物品都已支持使用：未知用途直接拒绝。食物、木门、木头/木板和未点燃 TNT 放置已在真实测试服验证；液体/点火/跨维度床锚仍需专门隔离验收。命令和聊天返回 `submitted`；副作用无法确认时返回 `unknown` 并暂停，不能自动重试。

高危审批绑定 Node 内部保存的不可变动作和一次性 `specId`，120 秒内由已验证网页管理员批准。执行前复核目标、支撑、双手、槽位、数量、邻近危险、朝向、世界和控制代次。原目标变化、会话撤销、预算超时或急停都会使旧动作失效。普通命令由服务器原生权限决定，身体不能是 OP；指令仍可能有区域外副作用，必须先限制其 LuckPerms 组。

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

`body` 的其他配置与实际默认值在 `internal/config/body.go`：每次运行最多 12 步/240 秒，单动作最多 20 秒，grant 最长 5 秒；区域空列表时没有可操作区域。防护、路径规则和 PvP/容器禁用为固定规则，没有通过未实现字段开启的后门。配置更改沿用现有重启加载流程，无动态编辑接口。

## 门户与只读资料

管理员 API：`GET /api/body/status|events|approvals|audit`、`POST /api/body/control`（`action`）、`POST /api/body/approvals/<id>`（`approved`）、`POST /api/body/drain`。写入须 JSON、同源 cookie 或请求头 Bearer；SSE 使用 cookie，不接受 URL token。控制接口绕过 agent 队列和动作锁，2 秒内等 Node ACK，未确认返回 `stop_unconfirmed`，不能当作已停。

`GET /api/body/profile` 是固定白名单快照，不含坐标、背包、聊天、审批理由。启用 `body.profile.enabled` 后，外部服务用独立只读 `MINEAGENT_BODY_PROFILE_TOKEN` 的 Bearer 请求；Go 加载时只保存其 SHA-256，也可直接配置 `body.profile.tokenHash`。令牌轮换通过私有环境文件和正常后端部署完成。管理员也只能从此接口拿到同一资料投影。论坛本体与写作工具不在本版实现。

## 运行与部署

建议分别管理 Go、Node、ViaProxy 进程，Node 内存上限 512M，ViaProxy 上限至少 768M（实测 RSS 约 455MiB，原设计 384MB 估计不足）。这里不自动安装 systemd 单元，也不读取正式配置。WS 控制租约每 500ms 续期，Node 超过 1.5 秒未收到即停止主动输入；这不能撤回已发出的游戏包或消除液体、爆炸、重力后果。

后端正式部署必须使用 `scripts/deploy.sh`：先检查棋局，再调用认证 drain 阻止新动作并等待空闲，确认后才替换二进制。检查失败不替换、不重启；drain 后部署失败需管理员在 `/body` 恢复。`--force` 会跳过两项检查，只能在明确授权时使用。降级可切回 `mode=observe`；停用为 `enabled=false` 并停止专用 Node/代理进程。不要为验证本功能重启 Paper。

## 验证

```sh
go test ./...
go test -race ./internal/body ./internal/ws ./internal/agent ./internal/tools ./internal/account ./internal/storage ./internal/webui ./internal/portal ./cmd/mineagent
python3 -m unittest discover -s scripts -p 'deploy_test.py'
cd body && npm test
```

真实 E2E 使用 `scripts/body-e2e.mjs <disposable-paper-dir> /tmp/mineagent-body-v1`，需要根目录已有 Playwright、已构建 Paper 插件 jar，以及运行中的测试 ViaProxy（回环 25666 → 25665）。测试目录必须有包含 `isolated test` 的 `.mineagent-body-test` 标记，且 Paper 为 `paper-26.2-124.jar`、全新平坦世界 `m0-world`、仅回环 25665、离线模式、关闭出生保护。脚本会更改这个测试世界、清空测试玩家背包和覆盖其中的测试插件配置；不要指向正式目录。它创建私有临时后端、测试账号、模拟模型和数据库，真实完成网页聊天到工具动作、审批恢复、陈旧目标拒绝、急停、资料权限和手机浏览器验证，结束后只关闭自身子进程。

当前已完成隔离环境验证，24 小时长期运行、固定负载的 MSPT/CPU/换页对比及生产权限/保护验收仍是上线门槛，不能由 30 分钟兼容性测试代替。

## 后续客户端 mod

第一版无需玩家安装 mod。后续客户端可承载选中 AI、任务面板、状态 HUD、视角或手动接管。届时客户端只向后端提交经认证的意图，后端继续负责控制权与策略；手动接管必须先暂停 agent、增加控制代次并清掉旧动作，再授予短期控制权，断线默认暂停。MineUI 已有 UI 能力时优先复用其公开 API；仅在实际客户端需求确定后增加适配，不在本版引入第二套控制协议或修改其他项目。
