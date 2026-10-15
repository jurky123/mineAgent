# zkun 社区改造与 Passkey 迁移记录

实施日期：2026-10-15。依据用户提供的 2026-10-14 设计建议，并采纳用户明确选择的“Passkey 登录，配套账号恢复流程”。此文档描述仓库实现与验证，不代表已部署或整合包已发布。

## 本轮范围与完成情况

- 首页：公共访问，无需先登录；欢迎区与世界图片区采用非对称布局，Agent / 游戏 / Minecraft 三个入口明确分级；个人继续入口仅根据实际会话返回；无记录不伪造活动。低频屏幕工具收进账户菜单。
- 公共界面：沿用原生 ES 模块与设计变量，统一 zkun 字标文字、64px 顶栏、当前位置文字/形状、焦点、主要操作色、移动导航及减少动态效果。Agent 保留工作区结构，同时使用公共导航与同一身份。
- Minecraft：新增 `/minecraft` 安装指南，首页与指南读取同一个公开发布记录；发布未就绪时禁用下载。提供 PCL 官方步骤链接、升级备份说明、六类故障下一步、电脑端使用说明及复制失败的手动选择入口。
- Agent：共享 Passkey 登录，不再输入名字重复登录；示例与 URL 的 `q` 只填入草稿，需用户明确发送；退出成功后清除本地模型偏好缓存，失败保留状态并提示重试。
- 游戏：房间码有固定标签、就地错误、粘贴清理与防重复加入；空大厅提供演示回放；连接中断与重新同步有明确状态。五子棋仅在合法落子时提供预览。
- 回放：增加 A–O / 1–15 坐标、白子边界、末手及终局五连线；棋谱点击跳步，支持方向键、Home/End；输入框/范围滑块自行处理按键。明确标注历史回放与演示数据。
- 认证：见下节。未增加邮件、第三方登录、PIN 或自定义密码学。

不在本轮冒充完成的内容：真实世界照片、整合包文件与许可证清单、Windows/PCL 干净环境导入、真手机与安全密钥测试、正式域名下的 Passkey 验收、真实用户 Core Web Vitals、屏幕设备位置/别名采集。屏幕控制页主体沿用现有界面，服务端写操作继续检查管理员；后续单独改进信息层级。

## 认证核查与实现

原实现 `account.Service.Login` 仅收到名字即 `EnsureUser` 并发放会话；管理员由名字匹配 `web.adminUsers`。因此名字不是身份凭据，旧登录会话也不能用于证明旧账号归属。不能让持有旧 token 的人直接给账号绑定 Passkey。

新流程：

1. 管理员通过站外渠道核验本人身份，使用本机操作员 CLI 对指定账号发放 24 小时一次性激活码；名字/账号 ID 和既有对话、战绩保持不变。邀请本身不撤销现有 Passkey。
2. 激活码只以 sha256 存储；CLI 写入新建的 0600 文件，拒绝覆盖现有文件，不输出秘密。通过核验后的私密渠道交付本人，用户在本站激活表单输入。
3. `go-webauthn/webauthn v0.18.2` 验证 WebAuthn 注册与签名。RP ID / HTTPS origin 只来自配置；不信任请求 Host。要求 discoverable credential 与 user verification，使用随机 32 字节 user handle，保存完整凭据 JSON（包括 flags、计数器、extensions）。
4. 5 分钟挑战仅存在服务端内存，通过 HttpOnly / SameSite=Strict 的随机 cookie 绑定浏览器、操作类型与 origin；完成时一次性删除；最多 1024 条挂起挑战，begin 请求按实际连接来源限制为每分钟 64 次（不信任客户端转发头）；来源桶也有数量上限。重启后须重新开始验证。
5. 激活成功展示八条 128-bit 恢复码，仅显示一次、可下载文本；数据库只存哈希，不写 localStorage/URL/日志。用户确认已离线保存后，使用新 Passkey 登录。
6. 恢复码可用于注册替代 Passkey；验证成功后，在同一数据库事务中撤销旧 Passkey、全部旧恢复码、全部旧会话，并写入新凭据与恢复码。验证失败或数据库提交失败不会提前撤销旧凭据。恢复码本身不直接发放登录会话。
7. 丢失 Passkey 且没有恢复码时，管理员再次核验归属并通过 CLI 发放恢复激活码。没有基于昵称或旧 token 的自动恢复后门。

会话必须有与当前 Passkey 关联的验证记录。旧的名字登录、旧 token、旧 cookie 均不再被当作身份；`/api/login` 和 `/api/auth/login` 返回 410。管理员权限仍由服务端配置决定，客户端隐藏按钮不参与授权。退出、设备撤销、恢复操作会使会话验证失效。

目前不提供网站里的 Passkey 删除/增添管理；可使用设备同步的 Passkey，或通过已实现的恢复流程更换。后续若添加凭据管理，必须要求新的 Passkey 验证，并避免删除最后一枚有效凭据导致失联。

## 上线前必须执行的迁移准备

这些步骤尚未在生产环境运行。认证变更会让旧会话失去访问权，不能当成普通无感 UI 升级直接重启。

- 先备份 SQLite（包含一致的 WAL 状态，不能仅随手复制正在写入的 `.db`），确认可恢复；不要用测试库替换。
- 核验管理员与旧账号本人身份，确定交付渠道，并为管理员准备激活入口；不通过猜名字认领旧数据。
- 固定域名：默认 RP ID 为 `web.domain`，origin 为 `https://<web.domain>`。需要多源或开发时显式配置 `web.passkeyRPID` / `web.passkeyOrigins`；只允许 HTTPS，开发例外是 `http://localhost:<port>`。不支持随意变更 RP 域名后沿用旧凭据。
- 使用已构建的新二进制执行操作员命令；不能用当前旧二进制假设有这些参数。示例中的路径和账号须替换，秘密文件不要加入 Git：

```sh
/path/to/reviewed-mineagent --config /path/to/private-config.json --auth-invite USER --auth-invite-file /private/path/new-activation.txt
/path/to/reviewed-mineagent --config /path/to/private-config.json --auth-invite USER --auth-recovery --auth-invite-file /private/path/new-recovery.txt
```

- CLI 会新增认证表、为不存在的合法账号创建稳定用户记录、写入激活授权；它不重启服务。只有明确安排迁移时才对生产库执行。
- 使用正式 HTTPS origin 在测试账号上完成注册、登录、另一设备/真实安全密钥、取消、恢复、退出与权限验收。无恢复码的人工核验流程也需要演练。
- 旧的部署检查 token 同样不再有效；管理员通过 Passkey 登录后，需将新的管理员会话凭据安全保存到部署检查使用的 0600 token 文件（或 `TOKEN_FILE` 指向的文件）。不把 token 打印到日志，也不使用 `--force` 绕过认证失败。
- 通知用户旧会话不能继续使用，提供核验与激活方式。确认管理员可登录后，再按 `scripts/deploy.sh` 对局保护流程安排上线；本轮没有请求/执行线上重启。
- 不把回滚到“名字即账号”作为长期恢复手段。数据库新增表可保留；若回滚旧程序，会重新暴露旧认证风险，必须先隔离访问并制定恢复方案。

## Minecraft 发布记录与素材

公开信息唯一来源：`internal/webui/static/minecraft/release.json`。目前是 `preparing`，图片与正式发布包为空。2026-10-15 从本机向已记录的 `mc.zkun.art:25565` 完成 Minecraft 状态协议查询，返回 Paper 26.2（协议 776），因此填入了公开地址与服务端版本；这不代表外部网络可达性、完整进服或 Fabric/Forge 客户端组合已经验证。客户端版本和下载仍显示待确认。公开记录只放可以公开的地址和资料，不能放凭据或私有坐标。

- 根字段：`name`、`status`（`preparing` / `published`）、`serverAddress`、`serverVersion`、`cover`、`release`。
- `cover`：真实、获授权图片的 `/static/...` 路径与 `alt`。缺图用单色占位，不生成假服务器景观。补图时提供固定宽高比、合适尺寸 WebP/AVIF 与必要回退文件，遮掉聊天/玩家个人资料/私有坐标。
- `release` 必填：`version`、不可变且文件名带版本的 `url`、`format`（`mrpack` / `curseforge`）、整数 `bytes`、64 位十六进制 `sha256`、`minecraft`、`loader`（含精确版本）、`serverCompatibility`、`pcl`、`publishedAt`、`testedAt`、`verified: true`。
- 可选：`required`（必须更新或可选更新）、`changelog`。内存、磁盘推荐值必须经过实际测试后再加入，当前不猜数值。
- `verified` 仅在许可证核验、干净 Windows / 指定 PCL 导入、依赖安装、登录、启动与实际进服全部完成并留证后设置。Paper 插件与 Fabric/Forge 客户端模组必须区分。
- 页面没有“点击即安装/已安装”回调，不把下载点击当作安装成功。服务器离线本身不禁用已发布包下载。

发布前可运行：

```sh
python3 scripts/verify_minecraft_release.py path/to/release.json path/to/zkun-VERSION.mrpack
```

该脚本只核对本地文件名、版本、大小、SHA256、基本标准清单与明显的私密/危险路径，不执行安装、不上传文件、不证明许可证或 PCL 兼容；`.mrpack` 的依赖下载仍需要网络。发布文件与记录应一起审核、保持 URL 不可变。

## 自动化证据与人工边界

- `go test -p 1 ./...`：包括授权、私有文件、会话、游戏等既有测试，以及认证存储的原子恢复、一次性授权、过期和回滚测试。
- `go test -race -p 1 ./internal/account ./internal/storage ./internal/portal ./internal/webui`；`go vet -p 1 ./...`；临时路径构建，不覆盖线上 `bin/mineagent`。
- `npm run test:passkeys`：真实 Go 验证器 + 临时 SQLite + Chrome CDP 虚拟 CTAP2 认证器，验证注册/登录/恢复、管理员权限、旧会话拒绝、origin、伪造与重放拒绝、撤销及退出。不是把 `navigator.credentials` 替换成假返回值。测试只打印通过结果，不打印临时激活码/恢复码/token。
- `npm run test:community`：模拟 API 下检查公共首页/指南、发布与失败状态、跨页身份、草稿、回放、390/768/1280px 宽度、深色与减少动态效果。截图写到 `/tmp/zkun-community-ui`，不是已发布页面。
- `npm run test:models` / `npm run test:prompts`：已有模型与提示词交互回归。

本轮没有真实访问的 LCP/INP/CLS 数据，也没有宣称 WCAG 全站合规。后续应补真实设备、窄屏键盘/200% 缩放、慢网、全新实时对局、屏幕设备操作和性能基线记录。

## 资料依据

- [PCL 官方整合包安装帮助](https://github.com/Meloong-Git/PCLHelp/blob/master/Minecraft/整合包安装.xaml)：拖入文件、独立版本名、管理员运行导致禁止拖放的提示。
- [Modrinth 格式](https://support.modrinth.com/en/articles/8802351-modrinth-modpack-format-mrpack)：标准索引、精确依赖与校验。
- [go-webauthn 文档](https://pkg.go.dev/github.com/go-webauthn/webauthn@v0.18.2/webauthn)：服务器保存完整挑战、discoverable login、凭据校验。
- [W3C 文本对比度](https://www.w3.org/WAI/WCAG22/Understanding/contrast-minimum.html)：文字与背景逐对检查的依据。
