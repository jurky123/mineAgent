package tools

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudwego/eino/schema"
)

func (w *Workspace) colabTool() *wsTool {
	return &wsTool{w: w, name: "colab_run", fn: w.runColab,
		desc: "管理员工具。在独立 Google Colab 环境执行 workspace 中的 Python 或 Notebook。可先用 workspace_write 创建和修改 .ipynb。选择 CPU、GPU 型号和高内存；依赖配置写在 Notebook 首个代码单元。返回日志及执行后的 Notebook，结束自动释放运行环境。只适合明确要求在 Colab 执行的任务，会消耗账号算力。",
		params: map[string]*schema.ParameterInfo{
			"path":            {Type: schema.String, Required: true, Desc: "workspace 相对路径，.py 或 .ipynb"},
			"gpu":             {Type: schema.String, Enum: []string{"CPU", "T4", "L4", "G4", "A100", "H100"}, Desc: "缺省 CPU，按任务需要选择"},
			"high_mem":        {Type: schema.Boolean, Desc: "申请高内存，默认 false，L4 不支持"},
			"timeout_seconds": {Type: schema.Integer, Desc: "总超时 30–1800 秒，默认 600"},
		}}
}

const colabFileLimit = 16 << 20

func (w *Workspace) runColab(ctx context.Context, args map[string]any) (string, error) {
	if !w.colabMu.TryLock() {
		return "", fmt.Errorf("已有 Colab 任务运行中")
	}
	defer w.colabMu.Unlock()
	cfg := w.cfg.Colab
	if !filepath.IsAbs(cfg.Binary) || !filepath.IsAbs(cfg.StateDir) || !filepath.IsAbs(w.cfg.Root) {
		return "", fmt.Errorf("Colab 二进制、授权目录和 workspace 必须配置为绝对路径")
	}
	// The operator authorizes this separate CLI profile before exposing the tool.
	if info, err := os.Stat(cfg.StateDir); err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("Colab 授权目录不可用或权限不是私有")
	}
	credentials := filepath.Join(cfg.StateDir, ".config", "gcloud", "application_default_credentials.json")
	if info, err := os.Lstat(credentials); err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return "", fmt.Errorf("Colab 尚未授权，或授权文件权限不符合要求")
	}
	name, _ := args["path"].(string)
	ext := filepath.Ext(name)
	if ext != ".py" && ext != ".ipynb" {
		return "", fmt.Errorf("请选择 workspace 内的 .py 或 .ipynb")
	}
	gpu, _ := args["gpu"].(string)
	if gpu == "" {
		gpu = "CPU"
	}
	switch gpu {
	case "CPU", "T4", "L4", "G4", "A100", "H100":
	default:
		return "", fmt.Errorf("不支持的 GPU 型号")
	}
	high, _ := args["high_mem"].(bool)
	if high && gpu == "L4" {
		return "", fmt.Errorf("L4 不支持高内存选项")
	}
	seconds := 600
	if raw, exists := args["timeout_seconds"]; exists {
		n, ok := raw.(float64)
		if !ok || n < 30 || n > 1800 || n != float64(int(n)) {
			return "", fmt.Errorf("超时必须为 30–1800 秒的整数")
		}
		seconds = int(n)
	}
	if err := w.ensureRoot(); err != nil {
		return "", err
	}
	root, err := os.OpenRoot(w.cfg.Root)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(name)
	if err != nil || !info.Mode().IsRegular() || info.Size() > colabFileLimit {
		return "", fmt.Errorf("脚本不存在、不是普通文件或超过 16 MiB")
	}
	input, err := root.Open(name)
	if err != nil {
		return "", err
	}
	source, err := io.ReadAll(io.LimitReader(input, colabFileLimit+1))
	input.Close()
	if err != nil || len(source) > colabFileLimit {
		return "", fmt.Errorf("无法读取脚本或超过 16 MiB")
	}
	stage, err := os.MkdirTemp("", "mineagent-colab-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(stage)
	if ext == ".py" {
		// Run scripts as one-cell notebooks so Python exceptions are visible in
		// the result even when the CLI itself returns zero.
		source, err = json.Marshal(map[string]any{
			"nbformat": 4, "nbformat_minor": 5, "metadata": map[string]any{},
			"cells": []any{map[string]any{
				"cell_type": "code", "id": "script", "source": string(source),
				"metadata": map[string]any{}, "execution_count": nil, "outputs": []any{},
			}},
		})
		if err != nil || len(source) > colabFileLimit {
			return "", fmt.Errorf("脚本转换为 Notebook 后超过上限")
		}
	}
	file := "input.ipynb"
	if err := os.WriteFile(filepath.Join(stage, file), source, 0600); err != nil {
		return "", err
	}
	session := "mineagent-" + strings.ToLower(rand.Text())
	ctx, cancel := context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
	defer cancel()

	invoke := func(callCtx context.Context, argv ...string) (string, error) {
		flags := []string{"--fsize=16777216", "--", cfg.Binary, "--auth", "adc"}
		cmd := exec.CommandContext(callCtx, "/usr/bin/prlimit", append(flags, argv...)...)
		cmd.Dir = stage
		cmd.Env = []string{"GOOGLE_APPLICATION_CREDENTIALS=" + credentials, "HOME=" + cfg.StateDir, "XDG_CONFIG_HOME=" + filepath.Join(cfg.StateDir, ".config"), "PATH=/usr/bin:/bin", "LANG=C.UTF-8", "TERM=dumb"}
		cmd.WaitDelay = 2 * time.Second
		out := &colabOutput{}
		cmd.Stdout = out
		// CLI stderr can include authentication diagnostics. Do not forward it.
		err := cmd.Run()
		return out.String(), err
	}
	options := []string{"new", "-s", session}
	if gpu != "CPU" {
		options = append(options, "--gpu", gpu)
	}
	if high {
		options = append(options, "--high-mem")
	}
	result := map[string]any{"session": session, "accelerator": gpu, "high_mem": high}
	if _, err := invoke(ctx, options...); err != nil {
		result["error"] = "Colab 创建环境失败，请检查授权、算力额度和机型可用性"
	} else {
		log, err := invoke(ctx, "exec", "-s", session, "--timeout", "0", "-f", file)
		result["output"] = log
		if err != nil {
			result["error"] = "Colab 执行失败或超时"
		} else {
			// The CLI exports the executed notebook beside its staged input.
			output, err := os.ReadFile(filepath.Join(stage, "input_output.ipynb"))
			if err == nil && len(output) <= colabFileLimit {
				var notebook struct {
					Cells []struct {
						Outputs []struct {
							Type string `json:"output_type"`
						} `json:"outputs"`
					} `json:"cells"`
				}
				if json.Unmarshal(output, &notebook) != nil {
					result["error"] = "执行后的 Notebook 格式无效"
				}
				for _, cell := range notebook.Cells {
					for _, item := range cell.Outputs {
						if item.Type == "error" {
							result["error"] = "Notebook 有单元执行失败，请查看产物"
						}
					}
				}
				rel := session + ".ipynb"
				f, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err == nil {
					_, err = f.Write(output)
					if closeErr := f.Close(); err == nil {
						err = closeErr
					}
					if err == nil {
						result["notebook"] = rel
					} else {
						_ = root.Remove(rel)
					}
				}
			}
			if result["notebook"] == nil {
				result["artifact_error"] = "未取得执行后的 Notebook"
			}
		}
	}
	cleanup, cancelCleanup := context.WithTimeout(context.WithoutCancel(ctx), 25*time.Second)
	defer cancelCleanup()
	// Do not stop other sessions even when provisioning was interrupted.
	_, stopErr := invoke(cleanup, "stop", "-s", session)
	result["released"] = stopErr == nil
	result["ok"] = result["error"] == nil && result["artifact_error"] == nil && stopErr == nil
	if stopErr != nil {
		result["cleanup_error"] = "未确认运行环境释放，请检查 Colab 会话列表"
	}
	w.audit(context.WithoutCancel(ctx), "colab_runtime", session, "finished", "", fmt.Sprintf("accelerator=%s released=%t", gpu, stopErr == nil))
	encoded, err := json.Marshal(result)
	return string(encoded), err
}

type colabOutput struct {
	bytes.Buffer
	truncated bool
}

func (o *colabOutput) Write(p []byte) (int, error) {
	n := min(len(p), max(0, 16<<10-o.Len()))
	_, _ = o.Buffer.Write(p[:n])
	o.truncated = o.truncated || n < len(p)
	return len(p), nil
}
func (o *colabOutput) String() string {
	s := strings.ToValidUTF8(o.Buffer.String(), "�")
	if o.truncated {
		return s + "\n[输出已截断]"
	}
	return s
}
