package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// blockedCommands 是 workspace_exec 的黑名单：按"首词"拦截。
// 目标是防手滑/防恶意破坏服务器，不是防顶级黑客——真想搞事的人总有办法，
// 所以 exec 默认只给 QQ 管理员用，且全程审计。
// 规则：
//   - rm/shutdown/reboot/poweroff/halt：删文件/关机，直接禁。
//   - mkfs/dd/fdisk/parted：分区/裸盘操作，禁。
//   - systemctl/service/init：动系统服务，禁（要重启 mineagent 走服务器命令行）。
//   - iptables/ufw/firewall-cmd：动防火墙，禁。
//   - chmod/chown/setfacl：改权限，禁（容易把自己锁死或放开敏感文件）。
//   - su/sudo/doas/passwd/useradd/usermod：提权/改账号，禁。
//   - curl/wget/ssh/scp：出网/连别的机器，v1 先禁（要装依赖先在服务器上手动装）。
//   - docker/podman：容器逃逸面大，禁。
//   - kill/killall/pkill：杀进程，禁（容易误杀 Paper/java）。
//   - crontab/at：定时任务，禁（持久化后门面）。
//   - shell 反弹关键字（nc -e、/dev/tcp、base64 -d 管道 sh 等）：禁。
// 允许的：python3、go、java、javac、mvn、gradle、git、ls/cat/echo/grep 等常规命令，
// 以及 && / || / ; 连接的组合命令（逐段检查，每段首词都要过白名单逻辑=不在黑名单）。
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
	"pip": "装 Python 包请联系管理员手动装（防投毒）",
	"pip3": "装 Python 包请联系管理员手动装（防投毒）",
	"npm": "装 npm 包请联系管理员手动装",
	"yarn": "装包请联系管理员手动装",
	"go": "",
}

var blockedSubstrings = []string{
	"/dev/tcp/", // bash 反弹 shell
	"base64 -d", "base64 --decode",
	"| sh", "|sh", "| bash", "|bash", "| dash",
	"eval ", "exec(",
	"~/.ssh", "/etc/passwd", "/etc/shadow", "/etc/sudoers",
}

func checkCommand(cmd string) error {
	lowered := strings.ToLower(strings.TrimSpace(cmd))
	if lowered == "" {
		return fmt.Errorf("命令为空")
	}
	for _, sub := range blockedSubstrings {
		if strings.Contains(lowered, sub) {
			return fmt.Errorf("命令包含被禁止的模式（%s）", sub)
		}
	}
	// 按 && || ; | 切段，每段首词过黑名单。管道符切开后 sh -c 这类也会被首词拦住。
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
		if reason, blocked := blockedCommands[first]; blocked {
			if first == "go" {
				// go build/test/vet/fmt 允许，go install/run 不允许（写 GOPATH/pkg）。
				if len(fields) > 1 && (fields[1] == "install" || fields[1] == "run") {
					return fmt.Errorf("go install/run 被禁止（会写外部目录），用 go build 把产物放 workspace 里")
				}
				continue
			}
			if reason == "" {
				reason = "该命令被禁止"
			}
			return fmt.Errorf("%s：%s", first, reason)
		}
	}
	return nil
}

func (w *Workspace) exec(ctx context.Context, args map[string]any) (string, error) {
	cmdStr, _ := args["command"].(string)
	if strings.TrimSpace(cmdStr) == "" {
		return "", fmt.Errorf("缺少参数: command")
	}
	if err := checkCommand(cmdStr); err != nil {
		return "", err
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
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin",
		"HOME=" + w.cfg.Root,
		"LANG=C.UTF-8",
		"NO_COLOR=1",
		"GIT_PAGER=cat",
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
