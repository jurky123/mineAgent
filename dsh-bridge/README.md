# MineAgent → DSH bridge

把 MineAgent 的 Agent 循环交给**已经运行的 DSH Web 宿主**，不再另起 sdk-minimal。MineAgent 继续负责账号、会话历史、FIFO、附件解析、权限、操作审批、执行沙箱与渠道发送。DSH 收到的工具均为 MineAgent 提供的代理；不继承 DSH 的宿主 bash、文件、子 Agent 等工具。

本包针对 DSH **0.2.1-rc.2** 验证。使用其公开的 `agents.create`、Agent preset、作用域 tools、systemPrompt 与 session 事件 API；DSH 升级后先重新跑隔离验收。插件使用 Node 自带 HTTP，不引入服务框架。

## 路由和会话

默认仍为 `agent.backend="eino"`（空值同义），配置不变时不会连接 DSH。设成 `dsh` 后，默认模型选择器及 `model.name` 路由到 `agent.dsh.provider/model`；`agent.dsh.models` 可进一步把明确的模型选择器映射到 DSH 已注册的适配器。其余显式选择仍走原有 Eino 提供商，包括未映射的 OpenRouter 模型。**这是调用前的路由选择**；DSH 已开始的任务出错后不会自动换引擎或重放工具。

启用前须核对默认模型的页面名称与实际 DSH 路由对应；桥接不会自动导入 DSH 模型目录，也不会把 OpenAI 模型名当作 DeepSeek 模型。DSH 对应适配器的凭据仍由 DSH 自己管理，不放进会话、工具参数或 MineAgent 提示词。原 `model` 配置仍供未映射模型和既有代码联网审查使用，不能简单删除。

每个任务创建独立的 DSH Agent，导入 MineAgent 按当前会话和触发消息取得的历史，保留 user/assistant 角色；图片经 DSH attachment store 转为视觉输入。任务结束释放 Agent，不续接原生 DSH Web 会话。MineAgent 的 `historyLimit` 和既有摘要读取生效，DSH preset 在当前任务内支持自动上下文压缩；DSH 的压缩结果目前不回写为 MineAgent 的跨任务摘要。DSH 本机会话/附件存储也会保存导入内容，因此两个服务属于同一受信管理边界。

QQ 排队和队满提示作为 `system` 通知保存，不进入模型上下文；兼容过滤旧版保存为助手消息的同类提示。QQ 请求历史纳入前一轮晚到的实际回答，但始终以本轮触发用户消息结尾，排除本轮通知、回复和后续输入。桥接发送前也校验最后一条为用户消息，避免把不合法上下文交给 DSH。修复不修改或删除既有聊天记录。

普通聊天默认思考强度 `high`，任务无额外总时限；非 MC 轮次沿用 `qqMaxIterations=0` 无限，MC 公共聊天保留 8 次模型步骤。已授权身体任务优先使用 `body.maxSteps`（默认12，显式0不限）与 `body.reasoningEffort`（默认medium），沿用每轮连续只读3次的工具层限制。DeepSeek 官方适配器只有 off/low/high/max 档：none→off、minimal→low、xhigh→max；普通聊天 medium→high，身体任务 medium→low，显式身体 high 仍为high。其他适配器直接接收所选值，不支持时明确失败。

MineAgent 保留原队列；桥接客户端和 DSH 插件额外保证桥接任务一次一个。审批直接送到等待中的工具，不排入被占用的工作队列；等待审批期间该桥接任务占住 FIFO，状态、审批与停止入口不受阻。此队列只约束 MineAgent 桥接任务，用户在 DSH Web 原生发起的任务仍由 DSH 自己调度。

## 后续部署配置

下列步骤修改宿主配置，应在决定上线时执行。仅提交代码和测试不会安装插件、修改活动 profile 或重启服务。

1. 在运行 DSH 的同一用户下安装本地包：`dsh plugin --profile web add /absolute/path/to/mineAgent/dsh-bridge`。插件包使用当前宿主同版本的 peer dependencies；不要给每个会话安装 DSH。
2. 生成双方可读、其他用户不可读的随机令牌文件（至少 32 个字符，0600），预建一个独立的 bridge cwd；不得用模型可写 workspace 存令牌。
3. 在 Web profile 的 patch 加入下面的行，或准备单独 patch 于下次启动通过 `--patch` 加载。`cwd` 是桥接会话元数据目录，不授予 shell 权限；不要指向服务配置目录。

```yaml
- insert:
    - id: mineagent-dsh-bridge
      name: mineagent-dsh-bridge
      config:
        host: 127.0.0.1
        port: 18770
        tokenFile: /absolute/path/to/bridge.token
        cwd: /absolute/path/to/bridge-state
```

4. MineAgent 配置示例：

```json
{
  "agent": {
    "backend": "dsh",
    "dsh": {
      "url": "http://127.0.0.1:18770",
      "tokenFile": "/absolute/path/to/bridge.token",
      "provider": "deepseek-official",
      "model": "deepseek-flash",
      "models": {}
    },
    "maxConcurrentRuns": 1,
    "qqMaxIterations": 0,
    "runTimeoutSec": 0
  }
}
```

也可通过 `MINEAGENT_AGENT_BACKEND`、`MINEAGENT_DSH_URL`、`MINEAGENT_DSH_TOKEN_FILE`、`MINEAGENT_DSH_PROVIDER`、`MINEAGENT_DSH_MODEL` 设置。不把 token 本身写进命令行或配置。开启前确认 DSH `/v1/health` 返回 `protocol:1`，然后按 MineAgent 原部署流程核查对局和活动任务、drain、切换，保留旧配置以便恢复。

只支持 HTTP 字面量回环地址和独立端口，禁止代理、重定向、浏览器 Origin/CORS 调用；不要通过公网反向代理暴露此端口。健康检查同样需要 `Authorization: Bearer <令牌文件内容>`。不会改变已有 Web 监听、DNS 或防火墙。

停止会先取消 Go 工具上下文，再请求 DSH 取消并等待 Agent 释放。执行工具时仍持续读取 DSH 传输；DSH 主动取消或连接断开也会取消本地工具，并等它退出后才释放队列。15 秒内不能确认停止时，该客户端拒绝后续 DSH 任务，需维护者核查宿主后重启恢复，避免不确定的任务重叠。已发生的外部动作不回滚。请求和工具调用 ID 在活动任务中防重放；有上限的已处理 ID 缓存用于拒绝近期请求重放，进程重启不具备持久去重语义，调用方不得自动重试已执行的请求。

共享技能使用 MineAgent 的 `skill` 工具与只读沙箱挂载；不要在 bridge preset 另装一份技能或开放 DSH 本机文件工具。原生 DSH Web 的 standard preset 可通过 `~/.agents/skills` 读取同一资料，minimal preset 本身不带 skill 工具。

## 验证

```sh
node --test dsh-bridge/server.test.mjs
go test ./internal/dshbridge ./internal/agent ./internal/qq ./internal/storage ./internal/tools ./internal/body ./internal/config
python3 scripts/dsh-bridge-e2e.py
```

最后一项要求 PATH 有 DSH；安装位置非标准时设 `DSH_TEST_MODULE_ROOT` 为它的 node_modules。测试复制插件到临时目录，启动独立 Web profile、随机回环端口和假模型，无真实 API key、不读取活动 DSH profile、不发送真实 QQ 消息。覆盖历史角色、high 参数、真实 PNG 输入、Go 工具回调、原生工具拒绝、停止后继续任务及轮次预算。它不验证真实提供商的额度、模型质量、QQ 上传权限或上线配置。

身体兼容回归：`go test -race ./internal/agent -run TestDSHBodyMergePreservesBudgetsAndPublicScope` 验证真实 Submit/桥接/工具路径上的12步与显式无限、身体思考强度、只读预算及普通MC公开工具隔离；审批取消/拒绝/过期由 `internal/body/external_approval_test.go` 验证。

QQ 排队回归 `TestDSHQueuedQQContextEndsAtTrigger` 通过临时数据库和假桥接覆盖同会话、跨会话与 `/new` 后排队，检查全局串行、上下文尾部、前轮回答和身份隔离；不连接真实 QQ/模型。
