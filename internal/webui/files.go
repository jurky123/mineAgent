package webui

import (
	"fmt"
	"mime"
	"path"
	"path/filepath"
	"strings"
	"unicode"

	"mineagent/internal/storage"
)

// 文件标记/路径校验的格式定义在 storage 包（agent 拼多模态消息也要用），
// 这里留别名，webui 内部调用不用带包名。
type UploadedFile = storage.UploadedFile

func BuildFileMarker(f UploadedFile) string { return storage.BuildFileMarker(f) }

func ParseFileMarkers(text string) (string, []UploadedFile) { return storage.ParseFileMarkers(text) }

func EscapeFileMarkers(text string) string { return storage.EscapeFileMarkers(text) }

func SafeWorkspaceRel(p string) bool { return storage.SafeWorkspaceRel(p) }

func sanitizeMarkerPart(s string) string { return storage.SanitizeMarkerPart(s) }

// ValidAccountName 校验网页账号名：1-24 个字符，允许各语言字母数字与 _ - . ，
// 必须字母数字开头（防 ".."、"."、隐藏目录），且不含路径分隔符。
func ValidAccountName(name string) bool {
	if name == "" || len([]rune(name)) > 24 {
		return false
	}
	for i, r := range name {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r):
		case r == '_' || r == '-' || r == '.':
			if i == 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// IsAdminName 判名字是否命中管理员名单（web.adminUsers）。
func IsAdminName(name string, admins []string) bool {
	for _, a := range admins {
		if a != "" && a == name {
			return true
		}
	}
	return false
}

// SafeUploadName 把上传的文件名净化成安全的展示名/磁盘名：去掉路径与分隔符，
// 保留中文等可读字符，控制在 80 字符内。
func SafeUploadName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f:
			continue
		case r == '/' || r == '\\' || r == ':' || r == '|' || r == ']':
			continue
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(b.String(), ". ")
	if name == "" {
		name = "file"
	}
	r := []rune(name)
	if len(r) > 80 {
		name = string(r[:80])
	}
	return name
}

// IsImagePath 按扩展名判断是否图片（网页端内联显示用）。
func IsImagePath(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".avif":
		return true
	}
	return false
}

// MimeByPath 按扩展名猜 mime，猜不出给 application/octet-stream。
func MimeByPath(p string) string {
	if m := mime.TypeByExtension(strings.ToLower(filepath.Ext(p))); m != "" {
		return m
	}
	if IsImagePath(p) {
		return "image/" + strings.TrimPrefix(strings.ToLower(filepath.Ext(p)), ".")
	}
	return "application/octet-stream"
}

// WireFile / WireMessage 是给网页前端的消息结构。
type WireFile struct {
	Name  string `json:"name"`
	Mime  string `json:"mime,omitempty"`
	Size  int64  `json:"size,omitempty"`
	URL   string `json:"url"`
	Image bool   `json:"image"`
}

type WireMessage struct {
	ID    int64      `json:"id"`
	Role  string     `json:"role"` // "user" | "agent"
	Name  string     `json:"name"`
	Text  string     `json:"text"`
	Files []WireFile `json:"files,omitempty"`
	At    int64      `json:"at"`
}

// msgFileURL 指向 /api/msgfile；下载接口按消息归属校验，别人的会话拿不到。
func msgFileURL(msgID int64, index int) string {
	if index > 0 {
		return fmt.Sprintf("/api/msgfile?m=%d&i=%d", msgID, index)
	}
	return fmt.Sprintf("/api/msgfile?m=%d", msgID)
}

// ToWire 把库里的消息转成前端结构：
//   - agent 发的文件消息（Target 前缀 file:）还原成文件卡片；
//   - 用户消息里的 [[file:...]] 标记剥成 files 数组。
func ToWire(m storage.Message) WireMessage {
	w := WireMessage{
		ID:   m.ID,
		Role: "user",
		Name: m.AuthorName,
		Text: m.Text,
		At:   m.CreatedAt,
	}
	if m.AuthorKind == "agent" {
		w.Role = "agent"
		w.Name = "MineAgent"
	}
	if strings.HasPrefix(m.Target, storage.KindFile) {
		rest := strings.TrimPrefix(m.Target, storage.KindFile)
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) == 3 && parts[2] != "" {
			name := strings.TrimSpace(m.Text)
			if name == "" {
				name = path.Base(parts[2])
			}
			w.Text = ""
			w.Files = []WireFile{{
				Name:  name,
				Mime:  MimeByPath(parts[2]),
				URL:   msgFileURL(m.ID, 0),
				Image: IsImagePath(parts[2]),
			}}
		}
		return w
	}
	if strings.HasPrefix(m.Target, storage.KindMarkdown) {
		return w
	}
	text, files := ParseFileMarkers(m.Text)
	w.Text = text
	for i, f := range files {
		w.Files = append(w.Files, WireFile{
			Name:  f.Name,
			Mime:  f.Mime,
			Size:  f.Size,
			URL:   msgFileURL(m.ID, i),
			Image: strings.HasPrefix(f.Mime, "image/") || IsImagePath(f.Path),
		})
	}
	return w
}

// MessageFileName 取附件展示名（下载时的文件名）：
// agent 发的文件用消息 Text（caption），空则用磁盘名；用户上传用标记里的原名。
func MessageFileName(m storage.Message, index int) string {
	if strings.HasPrefix(m.Target, storage.KindFile) {
		if name := strings.TrimSpace(m.Text); name != "" {
			return name
		}
		if rel := MessageRelPath(m, 0); rel != "" {
			return path.Base(rel)
		}
		return "file"
	}
	_, files := ParseFileMarkers(m.Text)
	if index >= 0 && index < len(files) && files[index].Name != "" {
		return files[index].Name
	}
	if rel := MessageRelPath(m, index); rel != "" {
		return path.Base(rel)
	}
	return "file"
}

// MimeForServing 下载响应的 Content-Type：webp/avif 这类在部分系统 mime 表里
// 没有，会被当成 octet-stream 直接下载（而不是内联显示），这里显式补齐。
func MimeForServing(rel string) string {
	ext := strings.ToLower(filepath.Ext(rel))
	switch ext {
	case ".webp":
		return "image/webp"
	case ".avif":
		return "image/avif"
	case ".svg":
		return "image/svg+xml"
	}
	if ct := mime.TypeByExtension(ext); ct != "" {
		return ct
	}
	if IsImagePath(rel) {
		return "image/" + strings.TrimPrefix(ext, ".")
	}
	return "application/octet-stream"
}

// MessageRelPath 取消息关联的文件相对路径：agent 文件消息取 Target，
// 用户上传消息从文本标记里取第 i 个。校验不过返回 ""。
func MessageRelPath(m storage.Message, index int) string {
	if strings.HasPrefix(m.Target, storage.KindFile) {
		if index != 0 {
			return ""
		}
		rest := strings.TrimPrefix(m.Target, storage.KindFile)
		parts := strings.SplitN(rest, ":", 3)
		if len(parts) != 3 || !SafeWorkspaceRel(parts[2]) {
			return ""
		}
		return parts[2]
	}
	_, files := ParseFileMarkers(m.Text)
	if index < 0 || index >= len(files) || !SafeWorkspaceRel(files[index].Path) {
		return ""
	}
	return files[index].Path
}
