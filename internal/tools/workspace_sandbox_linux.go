//go:build linux

package tools

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// newWorkspaceCommand 必须成功进入沙箱；缺少 bwrap/命名空间权限时不回退到宿主 Bash。
func newWorkspaceCommand(ctx context.Context, root, command string, network bool) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("workspace 执行需要 Bubblewrap (bwrap)，未安装时拒绝执行: %w", err)
	}
	root, err = filepath.Abs(root)
	if err != nil {
		return nil, err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return nil, err
	}
	if root == "/" {
		return nil, fmt.Errorf("workspace 根目录不能是宿主根目录")
	}
	args := []string{"--unshare-all", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	if network {
		// 仅静态约束与语义审查都通过的下载/装包命令使用宿主网络。
		args = append(args, "--share-net")
	}
	// 不挂整个 /usr：/usr/local/etc 等可能包含服务凭据。仅暴露只读运行时。
	for _, path := range []string{
		"/usr/bin", "/usr/sbin", "/usr/lib", "/usr/lib64", "/usr/share",
		"/usr/local/bin", "/usr/local/lib", "/usr/local/go",
		"/etc/resolv.conf", "/etc/nsswitch.conf", "/etc/ssl/certs", "/etc/localtime",
	} {
		if root == path || strings.HasPrefix(path, root+"/") || strings.HasPrefix(root, path+"/") {
			return nil, fmt.Errorf("workspace 根目录不能覆盖系统运行时: %s", root)
		}
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		} else if !os.IsNotExist(err) {
			return nil, err
		}
	}
	for _, path := range []string{"/bin", "/sbin", "/lib", "/lib64"} {
		info, err := os.Lstat(path)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return nil, err
			}
			args = append(args, "--symlink", target, path)
		} else {
			args = append(args, "--ro-bind", path, path)
		}
	}
	args = append(args, "--proc", "/proc", "--dev", "/dev", "--tmpfs", "/tmp",
		"--bind", root, root, "--chdir", root, "--", "/bin/bash", "-c", command)
	cmd := exec.CommandContext(ctx, bwrap, args...)
	cmd.Dir = root
	cmd.Env = []string{
		"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=" + root, "LANG=C.UTF-8",
		"NO_COLOR=1", "GIT_PAGER=cat", "PIP_CONFIG_FILE=/dev/null",
		"VENV_BIN=" + filepath.Join(root, ".venv", "bin"),
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		// 杀掉 launcher 的进程组；PID 命名空间随之销毁，后台子进程也不能存活。
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = time.Second
	return cmd, nil
}
