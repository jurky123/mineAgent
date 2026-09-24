package storage

import (
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// 用户上传的文件在消息文本里用标记表示：agent 能读懂路径，网页前端由服务端
// 转成结构化 files 数组。格式：[[file:<workspace相对路径>|<展示名>|<mime>|<字节数>]]
// 展示名/mime 里出现 "|" "]" 会被替换（见 sanitizeMarkerPart）。
// 放在 storage 包是为了让 agent（拼多模态消息）和 webui（前后端转换）
// 共用同一份格式定义，避免两边各写一套解析。
const fileMarkerPrefix = "[[file:"

var fileMarkerRe = regexp.MustCompile(`\[\[file:([^|\]\n]+)\|([^|\]\n]*)\|([^|\]\n]*)\|(\d+)\]\]`)

// UploadedFile 是网页端一次上传的结果，也是 /api/send 里 files 的元素。
type UploadedFile struct {
	Path string `json:"path"`
	Name string `json:"name"`
	Mime string `json:"mime"`
	Size int64  `json:"size"`
}

// BuildFileMarker 生成单个文件的标记文本。
func BuildFileMarker(f UploadedFile) string {
	return fmt.Sprintf("%s%s|%s|%s|%d]]",
		fileMarkerPrefix, f.Path, SanitizeMarkerPart(f.Name), SanitizeMarkerPart(f.Mime), f.Size)
}

// ParseFileMarkers 从消息文本里剥出全部文件标记，返回剩余文本与文件列表。
func ParseFileMarkers(text string) (string, []UploadedFile) {
	var files []UploadedFile
	clean := fileMarkerRe.ReplaceAllStringFunc(text, func(m string) string {
		sub := fileMarkerRe.FindStringSubmatch(m)
		var size int64
		_, _ = fmt.Sscanf(sub[4], "%d", &size)
		files = append(files, UploadedFile{Path: sub[1], Name: sub[2], Mime: sub[3], Size: size})
		return ""
	})
	clean = strings.TrimSpace(clean)
	return clean, files
}

// EscapeFileMarkers 防止用户自己拼出 [[file:...]] 标记：入库前先破坏前缀，
// 否则历史接口会把伪造标记当附件，甚至借 msgfile 读到别的文件。
func EscapeFileMarkers(text string) string {
	return strings.ReplaceAll(text, "[[file:", "[[ file:")
}

// IsImageMime 是否是图片 mime（视觉附件/内联显示的口径）。
func IsImageMime(m string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(m)), "image/")
}

// SafeWorkspaceRel 校验 workspace 相对路径：非空、非绝对、不逃逸目录。
// 反斜杠直接拒绝（Linux 上是文件名字符，容易在跨平台语义上出岔子）。
func SafeWorkspaceRel(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.ContainsAny(p, "\\:") {
		return false
	}
	clean := path.Clean(p)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return false
	}
	return true
}

// SanitizeMarkerPart 清掉会破坏标记分隔的字符。
func SanitizeMarkerPart(s string) string {
	s = strings.ReplaceAll(s, "|", "_")
	s = strings.ReplaceAll(s, "]", "_")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}
