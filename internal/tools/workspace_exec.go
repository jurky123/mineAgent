package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// blockedCommands 是 workspace_exec 的硬拦截黑名单：命中直接拒绝，不送审。
// 目标是防手滑/防恶意破坏服务器。联网与装包类（curl/wget/pip）不在这里，
// 而是走 reviewGated + 约束校验 + LLM 语义审查。
// 规则：
//   - rm/shutdown/reboot/poweroff/halt：删文件/关机，直接禁。
//   - mkfs/dd/fdisk/parted：分区/裸盘操作，禁。
//   - systemctl/service/init：动系统服务，禁（要重启 mineagent 走服务器命令行）。
//   - iptables/ufw/firewall-cmd：动防火墙，禁。
//   - chmod/chown/setfacl：改权限，禁（容易把自己锁死或放开敏感文件）。
//   - su/sudo/doas/passwd/useradd/usermod：提权/改账号，禁。
//   - ssh/scp/sftp/rsync：连别的机器/传文件，禁（curl/wget 的受控下载走审查）。
//   - docker/podman：容器逃逸面大，禁。
//   - kill/killall/pkill：杀进程，禁（容易误杀 Paper/java）。
//   - crontab/at：定时任务，禁（持久化后门面）。
//   - npm/yarn：本机没装 node，装包无意义，一律禁（要装先手动装 node）。
//   - go install/run：会写外部目录，禁；go build/test/vet/fmt 放行（见下）。
//   - shell 反弹关键字（nc、/dev/tcp、base64 -d 管道 sh、fork 炸弹等）：禁。
// 允许（静态层）：python3、venv 内 python/pip、go build 等、git、常规命令，
// 以及 && / || / ; 连接的组合命令（逐段检查）。
// 注意：首词匹配用 basename（/usr/bin/curl 按 curl 算），防绝对路径绕过。
var blockedCommands = map[string]string{
	"rm": "删除文件请用服务器命令行手动操作，workspace 里删文件用 write 空内容覆盖或联系管理员",
	"rmdir": "删除目录请手动操作",
	"shutdown": "关机命令被禁止",
	"reboot": "重启命令被禁止",
	"poweroff": "关机命令被禁止",
	"halt": "关机命令被禁止",
	"mkfs": "磁盘操作被禁止",
	"dd": "裸盘操作被禁止",
	"fdisk": "分区操作被禁止",
	"parted": "分区操作被禁止",
	"systemctl": "系统服务请在服务器命令行管理",
	"service": "系统服务请在服务器命令行管理",
	"init": "系统命令被禁止",
	"iptables": "防火墙请手动管理",
	"ufw": "防火墙请手动管理",
	"firewall-cmd": "防火墙请手动管理",
	"chmod": "改权限请手动操作",
	"chown": "改属主请手动操作",
	"setfacl": "改 ACL 请手动操作",
	"su": "提权命令被禁止",
	"sudo": "提权命令被禁止",
	"doas": "提权命令被禁止",
	"passwd": "账号命令被禁止",
	"useradd": "账号命令被禁止",
	"usermod": "账号命令被禁止",
	"userdel": "账号命令被禁止",
	"curl": "出网下载 v1 暂不允许，需要装依赖请联系管理员手动装",
	"wget": "出网下载 v1 暂不允许，需要装依赖请联系管理员手动装",
	"ssh": "连别的机器被禁止",
	"scp": "传文件到别的机器被禁止",
	"sftp": "传文件被禁止",
	"rsync": "同步文件到别处被禁止",
	"docker": "容器命令被禁止",
	"podman": "容器命令被禁止",
	"kill": "杀进程请手动操作（容易误杀服务器进程）",
	"killall": "杀进程请手动操作",
	"pkill": "杀进程请手动操作",
	"crontab": "定时任务请手动配置",
	"at": "定时任务请手动配置",
	"nc": "网络命令被禁止",
	"ncat": "网络命令被禁止",
	"socat": "网络命令被禁止",
	"npm": "本机没装 node，npm 一律禁；要装先手动装 node",
	"yarn": "本机没装 node，yarn 一律禁；要装先手动装 node",
	"go": "",
}

// reviewGated 是"静态放行但需 LLM 复核"的一组：curl / wget / pip / pip3。
// 这类命令本身是装依赖/拉数据的正当工具，但参数稍变就变投毒/外传通道，
// 黑名单写不全，所以统一送 LLM 审查。npm/yarn 不在此列（本机无 node，直接禁）。
// 约束（静态先验，LLM 只做语义兜底）：
//   - curl/wget：只能 http(s) URL，且禁止 --upload-file/-T/--data/--form 等外传 flag，
//     禁止输出到 workspace 之外（-o 必须是相对路径），禁止管道给 shell
//     （toolchain 已有 |sh 拦截，这里再强调一次）。
//   - pip/pip3：必须用 venv 里的 pip（$VENV_BIN/pip），只允许 install，
//     且包名纯 [A-Za-z0-9_.-]（可带 ==版本号等），
//     禁止 -e/--index-url/--extra-index-url/--trusted-host/-r/-c/本地路径。
var reviewGated = map[string]bool{
	"curl": true, "wget": true, "pip": true, "pip3": true,
}

var blockedSubstrings = []string{
	"/dev/tcp/", // bash 反弹 shell
	"base64 -d", "base64 --decode",
	"| sh", "|sh", "| bash", "|bash", "| dash",
	"eval ", "exec(",
	"~/.ssh", "/etc/passwd", "/etc/shadow", "/etc/sudoers",
}

// static Verdict 是静态检查的结果：
//   - deny：硬拦截（黑名单/危险模式），直接拒绝，不送审。
//   - review：命中 reviewGated（curl/wget/pip），静态约束通过，需 LLM 复核。
//   - allow：普通命令，直接执行（仍受超时/输出上限/单并发/审计约束）。
type staticVerdict int

const (
	staticAllow staticVerdict = iota
	staticReview
	staticDeny
)

func (w *Workspace) checkStatic(cmd string) (staticVerdict, string, error) {
	lowered := strings.ToLower(strings.TrimSpace(cmd))
	if lowered == "" {
		return staticDeny, "", fmt.Errorf("命令为空")
	}
	for _, sub := range blockedSubstrings {
		if strings.Contains(lowered, sub) {
			return staticDeny, "", fmt.Errorf("命令包含被禁止的模式（%s）", sub)
		}
	}
	// 按 && || ; | 切段，每段首词过黑名单。管道符切开后 sh -c 这类也会被首词拦住。
	needsReview := false
	segs := strings.FieldsFunc(lowered, func(r rune) bool {
		return r == '&' || r == '|' || r == ';'
	})
	for _, seg := range segs {
		fields := strings.Fields(seg)
		if len(fields) == 0 {
			continue
		}
		first := fields[0]
		// 环境变量前缀（FOO=bar cmd）跳过。
		for strings.Contains(first, "=") && len(fields) > 1 {
			fields = fields[1:]
			first = fields[0]
		}
		first = strings.TrimPrefix(first, "sudo ")
		// basename：/usr/bin/curl 按 curl 算，防绝对路径绕过。
		base := first
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if reviewGated[base] {
			if err := checkGatedConstraints(w.cfg.Root, base, fields); err != nil {
				return staticDeny, "", err
			}
			needsReview = true
			continue
		}
		if reason, blocked := blockedCommands[base]; blocked {
			if base == "go" {
				// go build/test/vet/fmt 允许，go install/run 不允许（写 GOPATH/pkg）。
				if len(fields) > 1 && (fields[1] == "install" || fields[1] == "run") {
					return staticDeny, "", fmt.Errorf("go install/run 被禁止（会写外部目录），用 go build 把产物放 workspace 里")
				}
				continue
			}
			if reason == "" {
				reason = "该命令被禁止"
			}
			return staticDeny, "", fmt.Errorf("%s：%s", base, reason)
		}
		if base == "python" || base == "python3" {
			// 系统 python 只读：禁止 -m pip（走 venv 的 pip 去装）。
			for _, f := range fields[1:] {
				if f == "pip" {
					return staticDeny, "", fmt.Errorf("用 $VENV_BIN/pip install 装包（只能装进 workspace/.venv），不要用系统 python -m pip")
				}
			}
		}
	}
	if needsReview {
		return staticReview, gatedSummary(cmd), nil
	}
	return staticAllow, "", nil
}

// checkOneCommand 保留给单测/兼容：等价于 checkStatic 后只关心 allow 与否。
// review 视为"静态通过"（调用方决定是否送审），deny 返回错误。
func (w *Workspace) checkOneCommand(cmd string) error {
	_, _, err := w.checkStatic(cmd)
	return err
}

// gatedSummary 给 LLM 审查的输入做摘要：命令原文截断，防止 prompt 膨胀。
func gatedSummary(cmd string) string {
	if len(cmd) > 1000 {
		return cmd[:1000] + "…（已截断）"
	}
	return cmd
}

// checkGatedConstraints 是 reviewGated 命令的静态先验约束：
// curl/wget 只能下载（禁外传 flag、禁写 workspace 外、禁管道给 shell 由 blockedSubstrings 兜底）；
// pip 必须走 venv 且只 install 纯包名。LLM 只做语义兜底（钓鱼域名、可疑 URL 等）。
func checkGatedConstraints(root, base string, fields []string) error {
	switch base {
	case "curl", "wget":
		return checkDownloadFlags(base, fields)
	case "pip", "pip3":
		return checkVenvPipInstall(root, fields)
	default:
		return fmt.Errorf("未知受审命令: %s", base)
	}
}

// checkDownloadFlags 约束 curl/wget：只允许"从 http(s) 下载到 workspace 内"。
// 禁止：-T/--upload-file（上传文件）、--data* / --form*（POST 外传）、
// -o/-O 写绝对路径或 ..（必须落在 workspace 内相对路径）、
// 非 http(s) URL（ftp/file/dict/gopher 等一律禁）。
func checkDownloadFlags(base string, fields []string) error {
	// 注意 fields 是小写过的（checkStatic 里 lower），比较 flag 用小写没问题；
	// URL 大小写敏感，但这里只判 scheme 前缀，小写不影响。
	uploadFlags := map[string]bool{
		"-t": true, "--upload-file": true,
		"-d": true, "--data": true, "--data-raw": true, "--data-ascii": true,
		"--data-binary": true, "--data-urlencode": true,
		"-f": true, "--form": true, "--form-string": true,
		"-F": true, // wget --post-file 等价外传
		"--post-data": true, "--post-file": true,
		"--method": true, "-X": true, "--request": true, // 改 POST/PUT/DELETE
		"--proxy": true, "-x": true, "--preproxy": true, // 走代理外传
	}
	_ = base
	seenURL := false
	for i := 1; i < len(fields); i++ {
		f := fields[i]
		if uploadFlags[f] {
			return fmt.Errorf("%s 外传参数 %q 被禁止（只允许下载）", base, f)
		}
		// --data=xxx / --form=xxx 粘连形态。
		lf := strings.ToLower(f)
		for _, prefix := range []string{"--data", "--form", "--post-", "--upload", "--request", "--method", "-x="} {
			if strings.HasPrefix(lf, prefix) && (len(lf) == len(prefix) || lf[len(prefix)] == '=' || lf[len(prefix)] == '-') {
				return fmt.Errorf("%s 外传参数 %q 被禁止（只允许下载）", base, f)
			}
		}
		// -o/-O 输出路径约束。
		if f == "-o" || f == "-O" || f == "--output-document" {
			if base == "curl" && f == "-O" {
				// curl -O 按远端文件名落盘，落在 workspace 内，允许。
				continue
			}
			if i+1 >= len(fields) {
				return fmt.Errorf("%s 输出参数缺路径", base)
			}
			out := fields[i+1]
			i++
			if filepath.IsAbs(out) || out == ".." || strings.HasPrefix(out, "../") || strings.HasPrefix(out, "/") {
				return fmt.Errorf("%s 输出路径 %q 被禁止（只能写 workspace 内相对路径）", base, out)
			}
			continue
		}
		// URL 形态检查：看起来像 URL 的必须 http(s) 开头。
		// agent 传的 URL 常带引号（"https://..."），先剥一层引号再判。
		if u := strings.Trim(f, `"'`); strings.Contains(u, "://") {
			if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
				return fmt.Errorf("%s 禁止非 http(s) URL: %q", base, f)
			}
			seenURL = true
		}
	}
	_ = seenURL
	return nil
}

// checkVenvPipInstall 约束 pip：必须走 venv 且只 install 纯包名。
// 接受三种写法（exec 的工作目录就是 root，相对路径按 root 解）：
//  1. $VENV_BIN/pip install ...（exec 环境里 VENV_BIN=<root>/.venv/bin，推荐写法）
//  2. <root>/.venv/bin/pip install ...（相对或绝对路径）
//  3. 裸 pip install ...（PATH 里没有系统 pip？有也不怕——装包目标仍是 venv？
//     不，裸 pip 会装到系统 site-packages，所以裸写法只在"看起来像 venv 上下文"时放行：
//     实际上我们要求 agent 写全路径或 $VENV_BIN，裸 pip 直接拒绝，逼它写明。）
//
// 结论：只放行 1 和 2；裸 pip/pip3 一律拒绝并提示正确写法。
func checkVenvPipInstall(root string, fields []string) error {
	first := fields[0]
	// $VENV_BIN 是 exec 环境变量，静态检查时按字面匹配（大小写已 lower，
	// $venv_bin/pip 即原文 $VENV_BIN/pip）。
	if first == "$venv_bin/pip" || first == "$venv_bin/pip3" {
		// ok，进入下面的 install/包名检查
	} else if strings.HasSuffix(first, "/pip") || strings.HasSuffix(first, "/pip3") {
		// 路径写法：必须指向 <root>/.venv/bin/pip（防 /usr/bin/pip 冒充）。
		// root 相对路径时，agent 可能写 workspace/.venv/bin/pip 或绝对路径。
		want := filepath.Join(root, ".venv", "bin", "pip")
		want3 := filepath.Join(root, ".venv", "bin", "pip3")
		abs, _ := filepath.Abs(root)
		absWant := filepath.Join(abs, ".venv", "bin", "pip")
		absWant3 := filepath.Join(abs, ".venv", "bin", "pip3")
		if first != want && first != want3 && first != absWant && first != absWant3 {
			// 相对 root 的简写 .venv/bin/pip 也放行（工作目录就是 root）。
			if first != ".venv/bin/pip" && first != ".venv/bin/pip3" {
				return fmt.Errorf("pip 只能用 workspace/.venv 里的（$VENV_BIN/pip），不许用系统 pip：%q", fields[0])
			}
		}
	} else if first == "pip" || first == "pip3" {
		return fmt.Errorf("裸 pip 会装到系统目录，已拒绝；用 $VENV_BIN/pip install（只能装进 workspace/.venv）")
	} else {
		return fmt.Errorf("pip 只能用 workspace/.venv 里的（$VENV_BIN/pip），不许用系统 pip")
	}
	rest := fields[1:]
	if len(rest) == 0 || rest[0] != "install" {
		return fmt.Errorf("pip 只允许 install（用 $VENV_BIN/pip install 包名），其他子命令禁")
	}
	for _, arg := range rest[1:] {
		if strings.HasPrefix(arg, "-") || strings.HasPrefix(arg, "/") || strings.HasPrefix(arg, ".") {
			return fmt.Errorf("pip install 参数 %q 被禁止（只允许纯包名，可带 ==版本号）", arg)
		}
		// 纯包名，可带 ==版本号 / >= / ~= 修饰。
		name := arg
		for _, sep := range []string{"==", ">=", "<=", "~=", "!=", ">", "<"} {
			if i := strings.Index(name, sep); i >= 0 {
				name = name[:i]
				break
			}
		}
		// [extras] 去掉再验。
		if i := strings.Index(name, "["); i >= 0 {
			name = name[:i]
		}
		if name == "" {
			return fmt.Errorf("pip install 参数 %q 非法", arg)
		}
		for _, r := range name {
			if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
				return fmt.Errorf("pip install 参数 %q 非法（包名只能是字母数字_.-）", arg)
			}
		}
	}
	return nil
}

func (w *Workspace) exec(ctx context.Context, args map[string]any) (string, error) {
	cmdStr, _ := args["command"].(string)
	if strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("缺少参数: command")
	}
	verdict, summary, err := w.checkStatic(cmdStr)
	if err != nil {
		w.audit(ctx, "workspace_exec", truncate(cmdStr, 500), "static_denied", "", err.Error())
		return "", err
	}
	if verdict == staticReview {
		reviewer := reviewerFromContext(ctx)
		if reviewer == nil {
			reviewer = w.reviewer
		}
		if reviewer == nil {
			// 没配审查器 = fail-closed：review 类命令一律拒绝。
			msg := "该命令需语义审查后才能执行，但审查器未配置，已拒绝"
			w.audit(ctx, "workspace_exec", truncate(cmdStr, 500), "review_unavailable", "", msg+" | "+summary)
			return "", fmt.Errorf("%s", msg)
		}
		decision, reviewErr := reviewer.Review(ctx, ReviewRequest{
			Command:   cmdStr,
			Summary:   summary,
			Requester: RequesterFromContext(ctx),
			SessionID: sessionFromContext(ctx),
		})
		if reviewErr != nil {
			// 审查器出错（超时/解析失败）= fail-closed，拒绝执行。
			msg := "语义审查失败，已拒绝执行：" + reviewErr.Error()
			w.audit(ctx, "workspace_exec", truncate(cmdStr, 500), "review_error", "", msg)
			return "", fmt.Errorf("%s", msg)
		}
		if !decision.Allow {
			msg := "语义审查未通过，已拒绝执行"
			if decision.Reason != "" {
				msg += "：" + decision.Reason
			}
			w.audit(ctx, "workspace_exec", truncate(cmdStr, 500), "review_denied", "", msg+" | 审查意见: "+decision.Reason)
			return "", fmt.Errorf("%s", msg)
		}
	}
	if err := w.ensureRoot(); err != nil {
		return "", err
	}

	// 全局单并发：防两个 exec 同时跑把 2 核小机器打满。
	w.execMu.Lock()
	defer w.execMu.Unlock()

	timeout := time.Duration(w.cfg.ExecTimeoutSec) * time.Second
	ectx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ectx, "bash", "-c", cmdStr)
	cmd.Dir = w.cfg.Root
	// 最小环境：防代理/密钥泄露到子进程，也防子进程读到奇怪的 env。
	// VENV_BIN 让 agent 知道 venv 的 python/pip 在哪（pip 只能装进这个 venv）。
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + w.cfg.Root,
		"LANG=C.UTF-8",
		"NO_COLOR=1",
		"GIT_PAGER=cat",
		"PIP_CONFIG_FILE=/dev/null",
		"VENV_BIN=" + filepath.Join(w.cfg.Root, ".venv", "bin"),
	}
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	runErr := cmd.Run()

	text := out.String()
	truncated := false
	if len(text) > w.cfg.MaxOutputBytes {
		// 按字节截断可能砍半个 UTF-8，用 rune 重切。
		r := []rune(text)
		limit := w.cfg.MaxOutputBytes
		if len(r) > limit {
			r = r[:limit]
		}
		text = string(r) + fmt.Sprintf("\n…（输出超限，已截断，全长约 %d 字节）", len(out.String()))
		truncated = true
	}
	code := 0
	errStr := ""
	if runErr != nil {
		if ectx.Err() == context.DeadlineExceeded {
			errStr = fmt.Sprintf("执行超时（%d 秒），进程已杀掉", w.cfg.ExecTimeoutSec)
			code = -1
		} else {
			code = 1
			if ee, ok := runErr.(*exec.ExitError); ok {
				code = ee.ExitCode()
			}
			errStr = runErr.Error()
		}
	}
	w.log.Info("workspace exec", "command", truncate(cmdStr, 200), "code", code, "truncated", truncated)
	payload, _ := json.Marshal(map[string]any{
		"exitCode": code,
		"output":   text,
		"error":    errStr,
	})
	if code != 0 && errStr != "" && text == "" {
		return "", fmt.Errorf("%s", errStr)
	}
	return string(payload), nil
}
