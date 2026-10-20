package aiplayer

import (
	"html"
	"net"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Fault struct {
	Code         string `json:"code"`
	Message      string `json:"error"`
	Status       int    `json:"-"`
	RetryAfterMS int64  `json:"retryAfterMs,omitempty"`
}

func (e *Fault) Error() string { return e.Code }
func fault(status int, code, message string) *Fault {
	return &Fault{Status: status, Code: code, Message: message}
}

var secretPattern = regexp.MustCompile(`(?i)(-----BEGIN[^\r\n]*PRIVATE KEY-----|\bsk-[a-z0-9_-]{16,}|\bBearer\s+[a-z0-9._-]{16,}|\b(?:api[_-]?key|access[_-]?token|password|secret)\s*["']?\s*[:=]\s*["']?[a-z0-9_./+-]{12,})`)
var htmlPattern = regexp.MustCompile(`(?i)<\s*[!/?a-z][^>]*>`)
var urlPattern = regexp.MustCompile(`(?i)https?://[^\s<>"'\x60]+`)
var coordinatePattern = regexp.MustCompile(`(?i)(\b[xyz]\s*[:=]\s*-?\d|\(-?\d{1,7}\s*,\s*-?\d{1,4}\s*,\s*-?\d{1,7}\))`)
var forbiddenScheme = regexp.MustCompile(`(?i)(javascript|vbscript|data|file|ftp):`)
var addressPattern = regexp.MustCompile(`\b(?:[0-9]{1,3}\.){3}[0-9]{1,3}\b`)

func publicHost(host string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "localhost" || !strings.Contains(host, ".") || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.HasSuffix(host, ".internal") || strings.HasSuffix(host, ".lan") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() {
			return false
		}
		for _, cidr := range []string{"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32"} {
			_, network, _ := net.ParseCIDR(cidr)
			if network.Contains(ip) {
				return false
			}
		}
	} else {
		// Reject abbreviated/encoded numeric hosts that browsers normalize to private IPs.
		numeric := true
		for _, part := range strings.Split(host, ".") {
			if _, err := strconv.ParseUint(part, 0, 32); err != nil {
				numeric = false
			}
		}
		if numeric {
			return false
		}
		for _, r := range host {
			if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-') {
				return false
			}
		}
	}
	return true
}
func validateText(text string, max int) ([]string, *Fault) {
	if !utf8.ValidString(text) || len([]rune(text)) > max {
		return nil, fault(422, "invalid_content", "内容长度或编码无效")
	}
	scan := html.UnescapeString(text)
	if decoded, err := url.PathUnescape(scan); err == nil {
		scan = decoded
	}
	if secretPattern.MatchString(scan) {
		return nil, fault(422, "invalid_content", "内容疑似包含凭据或私钥，请删除后重试")
	}
	if htmlPattern.MatchString(text) || strings.Contains(text, "![") || forbiddenScheme.MatchString(scan) {
		return nil, fault(422, "invalid_content", "不支持原始 HTML、图片或危险链接协议")
	}
	for _, ip := range addressPattern.FindAllString(scan, -1) {
		if net.ParseIP(ip) != nil && !publicHost(ip) {
			return nil, fault(422, "invalid_content", "内容不能包含私有或保留网络地址")
		}
	}
	for _, raw := range urlPattern.FindAllString(scan, -1) {
		raw = strings.TrimRight(raw, ".,;，。；)）]}")
		u, err := url.Parse(raw)
		if err != nil || u.User != nil || !publicHost(u.Hostname()) {
			return nil, fault(422, "invalid_content", "链接不能包含凭据或指向内网地址")
		}
	}
	flags := []string{}
	if coordinatePattern.MatchString(text) {
		flags = append(flags, "possible_coordinates")
	}
	if strings.Contains(text, "聊天记录") || strings.Contains(text, "[玩家") {
		flags = append(flags, "possible_player_chat")
	}
	return flags, nil
}
func normalizeTags(tags []string) ([]string, *Fault) {
	if len(tags) > 10 {
		return nil, fault(422, "invalid_content", "最多 10 个标签")
	}
	out := []string{}
	seen := map[string]bool{}
	for _, tag := range tags {
		tag = strings.ToLower(strings.TrimSpace(tag))
		if len([]rune(tag)) == 0 || len([]rune(tag)) > 32 {
			return nil, fault(422, "invalid_content", "标签长度须为 1–32 字符")
		}
		for _, r := range tag {
			if !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '-' && r != '_' {
				return nil, fault(422, "invalid_content", "标签仅支持文字、数字、短横线与下划线")
			}
		}
		if !seen[tag] {
			out = append(out, tag)
			seen[tag] = true
		}
	}
	return out, nil
}

type PostInput struct {
	Title              string   `json:"title"`
	Body               string   `json:"body"`
	Tags               []string `json:"tags"`
	Lang               string   `json:"lang"`
	Status             string   `json:"status"`
	ExpectedVersion    int64    `json:"expectedVersion,omitempty"`
	ExpectedRevisionID int64    `json:"expectedRevisionId,omitempty"`
}

var languagePattern = regexp.MustCompile(`^[a-zA-Z]{2,8}(-[a-zA-Z0-9]{2,8})*$`)

func (p *PostInput) Validate() ([]string, *Fault) {
	p.Title = strings.TrimSpace(p.Title)
	if p.Title == "" || strings.TrimSpace(p.Body) == "" {
		return nil, fault(422, "invalid_content", "标题和正文不能为空")
	}
	if strings.ContainsAny(p.Title, "\r\n") {
		return nil, fault(422, "invalid_content", "标题不能换行")
	}
	if p.Status == "" {
		p.Status = "draft"
	}
	if p.Status != "draft" && p.Status != "pending" {
		return nil, fault(422, "needs_review", "AI 只能保存草稿或提交待审，不能发布")
	}
	if p.Lang == "" {
		p.Lang = "zh-CN"
	}
	if len(p.Lang) > 24 || !languagePattern.MatchString(p.Lang) {
		return nil, fault(422, "invalid_content", "语言标识无效")
	}
	flags, err := validateText(p.Title, 120)
	if err != nil {
		return nil, err
	}
	more, err := validateText(p.Body, 20000)
	if err != nil {
		return nil, err
	}
	p.Tags, err = normalizeTags(p.Tags)
	return append(flags, more...), err
}
