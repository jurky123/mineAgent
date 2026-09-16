# MineAgent

Paper 服务器的 AI 聊天助手：Go 后端（CloudWeGo Eino）+ Paper 插件，通过本机 WebSocket 通信。

```text
Paper 插件 ──ws://127.0.0.1:8765── MineAgent(Go)
                                    ├── Eino ChatModelAgent（opencode-go / OpenAI 兼容）
                                    ├── Session Hub + SQLite
                                    ├── 工具网关（只读 + 高权限审批）
                                    └── 审批 / 审计 / 上下文摘要
```

## 目录

- `cmd/mineagent` —— 后端入口（配置、日志、优雅退出、`/healthz`）
- `internal/protocol` —— WS v1 协议（hello/chat/tool/approval/ping）
- `internal/ws` —— WebSocket 服务（握手鉴权、心跳、连接管理、钩子）
- `internal/session` —— 多人 Session、通道注册与 fan-out
- `internal/storage` —— SQLite（messages/summaries/identity_links/tool_audit/agent_checkpoints）
- `internal/agent` —— Eino Runner、审批中断/恢复、摘要与工具压缩中间件
- `internal/tools` —— 工具网关、只读工具、高权限工具与审批服务
- `internal/channels/minecraft` —— 与 Paper 插件通信的通道
- `paper-plugin` —— Paper 26.2 插件（Gradle，JDK Http WebSocket 客户端）

## 构建与运行

```bash
make build          # 产出 bin/mineagent
make test           # Go 单测（含 mock 模型端到端）
make plugin         # 构建 Paper 插件 jar
scripts/build.sh --install   # 构建并安装插件到 /home/ubuntu/minecraft/plugins
./bin/mineagent --config config.json
```

配置见 `config.example.json`（`config.json` 已被 gitignore）：模型走任意 OpenAI 兼容端点，
敏感值可用 `MINEAGENT_TOKEN`、`MINEAGENT_MODEL_API_KEY`、`MINEAGENT_MODEL_BASE_URL`、
`MINEAGENT_MODEL_NAME` 环境变量覆盖。

## 高权限操作

`minecraft_teleport` / `minecraft_give` / `minecraft_run_command` 会通过 Eino 的
interrupt/resume 暂停 Agent，向游戏内广播 **[批准] [拒绝]**；批准后命令以请求者本人身份执行，
权限硬限制由 LuckPerms 决定。所有决定写入 `tool_audit`。

## 部署

生产环境由 systemd 管理：

```bash
sudo systemctl status mineagent
sudo systemctl restart mineagent
journalctl -u mineagent -f
```
