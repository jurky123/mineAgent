//go:build !linux

package tools

import (
	"context"
	"fmt"
	"os/exec"
)

func newWorkspaceCommand(context.Context, string, string, bool) (*exec.Cmd, error) {
	return nil, fmt.Errorf("workspace 执行需要 Linux + Bubblewrap，当前平台不支持沙箱")
}
