package tools

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// RunInputParser is deliberately separate from model-controlled workspace_exec.
// Only a fixed parser and generated filenames are accepted. The isolated root
// contains this request's attachments, not the shared workspace or credentials.
func RunInputParser(ctx context.Context, root, parser string, args ...string) ([]byte, error) {
	if parser != "ffmpeg" && parser != "ffprobe" && parser != "pdftotext" {
		return nil, fmt.Errorf("不支持的输入解析器")
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	command := "ulimit -t 20; ulimit -v 786432; ulimit -f 16384; exec " + parser
	for _, arg := range args {
		command += " " + quote(arg)
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd, err := newWorkspaceCommand(ctx, root, command, false)
	if err != nil {
		return nil, fmt.Errorf("附件解析沙箱不可用")
	}
	stdout, stderr := &boundedOutput{limit: 2 << 20}, &boundedOutput{limit: 4096}
	cmd.Stdout, cmd.Stderr = stdout, stderr
	if err = cmd.Run(); err != nil {
		return nil, fmt.Errorf("附件解析失败（文件损坏、加密或解析器不可用）")
	}
	return stdout.buf.Bytes(), nil
}
