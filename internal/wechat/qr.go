package wechat

import (
	"fmt"
	"os"
	"path/filepath"

	qrcode "github.com/skip2/go-qrcode"
)

// renderQRPNG 把登录内容渲染成二维码 PNG（写入 path），并同时在终端打一份 ASCII。
func renderQRPNG(content, path string) error {
	if path == "" {
		return fmt.Errorf("empty path")
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := qrcode.WriteFile(content, qrcode.Medium, 512, path); err != nil {
		return err
	}
	q, err := qrcode.New(content, qrcode.Low)
	if err == nil {
		fmt.Println(q.ToSmallString(false))
	}
	return nil
}
