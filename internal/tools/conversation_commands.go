package tools

import (
	"fmt"
	"mineagent/internal/storage"
	"strconv"
	"strings"
	"time"
)

// ConversationPage distinguishes listing/paging from an exact stable id.
func ConversationPage(arg string) (int, bool, error) {
	f := strings.Fields(arg)
	if len(f) == 0 {
		return 0, true, nil
	}
	if strings.EqualFold(f[0], "page") {
		if len(f) != 2 {
			return 0, true, fmt.Errorf("用法：/resume page <页码>")
		}
		page, err := strconv.Atoi(f[1])
		if err != nil || page < 1 || page > 100000 {
			return 0, true, fmt.Errorf("页码必须在 1–100000 之间")
		}
		return (page - 1) * 20, true, nil
	}
	if len(f) != 1 {
		return 0, false, fmt.Errorf("用法：/resume <会话编号>")
	}
	return 0, false, nil
}
func ConversationList(current string, list []storage.ChatConversation, offset int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "可恢复的对话（第 %d 页）：\n", offset/20+1)
	if len(list) == 0 {
		b.WriteString("这一页没有会话。\n")
	}
	for _, c := range list[:min(20, len(list))] {
		marker := ""
		if c.Conv == current {
			marker = " · 当前"
		}
		title := strings.TrimSpace(strings.ReplaceAll(c.Title, "\n", " "))
		if title == "" {
			title = "新对话"
		}
		r := []rune(title)
		if len(r) > 32 {
			title = string(r[:32]) + "…"
		}
		fmt.Fprintf(&b, "%s · %s%s · %s\n", c.Conv, title, marker, time.UnixMilli(c.UpdatedAt).Format("01-02 15:04"))
	}
	b.WriteString("/resume <编号> 恢复对话；default 表示旧默认对话。")
	if len(list) > 20 {
		fmt.Fprintf(&b, "\n/resume page %d 查看下一页。", offset/20+2)
	}
	return b.String()
}
