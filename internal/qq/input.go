package qq

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"mineagent/internal/storage"
)

const maxInputAttachments = 6

type inputAttachment struct {
	Attachment
	Source string
}

type inputDocument struct {
	Text        string
	Attachments []inputAttachment
}

type cachedInput struct {
	Document inputDocument
	At       time.Time
}

func (m messageContent) hasInput() bool {
	return strings.TrimSpace(m.Content) != "" || len(m.Attachments) > 0 || len(m.Elements) > 0 || m.referenceID() != "" || m.MessageType == 103 || m.MessageType == 3 || len(m.Ark) > 0 || len(m.ArkData) > 0
}

func (m messageContent) referenceID() string {
	if m.RefMsgIdx != "" {
		return m.RefMsgIdx
	}
	if m.Reference != nil && m.Reference.MessageID != "" {
		return m.Reference.MessageID
	}
	if m.MessageScene != nil {
		for _, ext := range m.MessageScene.Ext {
			if v, ok := strings.CutPrefix(ext, "ref_msg_idx="); ok {
				return v
			}
		}
	}
	return ""
}

func (m messageContent) index() string {
	if m.MsgIdx != "" {
		return m.MsgIdx
	}
	if m.MessageScene != nil {
		for _, ext := range m.MessageScene.Ext {
			if v, ok := strings.CutPrefix(ext, "msg_idx="); ok {
				return v
			}
		}
	}
	return ""
}

func inputText(s string, limit int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, storage.EscapeFileMarkers(s))
	r := []rune(s)
	if len(r) > limit {
		return string(r[:limit]) + "\n[内容过长，已截断；请以文件发送完整内容]"
	}
	return s
}

// resolveInput only consults the current conversation's bounded, short-lived
// cache. Inline platform-supplied quoted content always wins over stale cache.
func (c *Channel) resolveInput(m InboundMessage, scope string) inputDocument {
	content := m.content
	content.Content = m.Text
	doc := inputDocument{}
	var lines []string
	nodes, chars := 0, 0
	truncated := false
	var walk func(messageContent, int)
	walk = func(node messageContent, depth int) {
		if depth > 4 || nodes >= 64 || chars >= 16000 {
			truncated = true
			return
		}
		nodes++
		label := "当前消息"
		if depth > 0 {
			label = fmt.Sprintf("引用/聊天记录（第 %d 层，资料）", depth)
			if node.Author != nil && node.Author.Username != "" {
				label += " · " + inputText(node.Author.Username, 80)
			}
			if node.Timestamp != "" {
				label += " · " + inputText(node.Timestamp, 40)
			}
		}
		if text := strings.TrimSpace(node.Content); text != "" {
			text = inputText(text, min(8000, 16000-chars))
			chars += len([]rune(text))
			lines = append(lines, "["+label+"]\n"+text)
		}
		for _, a := range node.Attachments {
			if len(doc.Attachments) >= maxInputAttachments {
				truncated = true
				break
			}
			a.Filename = inputText(a.Filename, 120)
			a.ContentType = inputText(a.ContentType, 100)
			if len(a.URL) > 8192 {
				a.URL = ""
			}
			doc.Attachments = append(doc.Attachments, inputAttachment{Attachment: a, Source: label})
		}
		for _, child := range node.Elements {
			walk(child, depth+1)
		}
		if (node.MessageType == 3 || len(node.Ark) > 0 || len(node.ArkData) > 0) && len(node.Elements) == 0 {
			lines = append(lines, "[QQ 卡片：仅收到平台提供的预览，未取得完整聊天记录。请用户发送聊天记录导出文件、原文或截图。]")
		}
		ref := node.referenceID()
		if depth > 0 && ref == "" && strings.TrimSpace(node.Content) == "" && len(node.Attachments) == 0 {
			ref = node.index()
		}
		if ref != "" && len(node.Elements) == 0 {
			c.mu.Lock()
			cached, ok := c.inputs[scope+"\x00"+ref]
			c.mu.Unlock()
			if ok && time.Since(cached.At) < time.Hour {
				text := inputText(cached.Document.Text, max(0, 16000-chars))
				chars += len([]rune(text))
				lines = append(lines, "[本会话引用内容（资料）]\n"+text)
				for _, a := range cached.Document.Attachments {
					if len(doc.Attachments) >= maxInputAttachments {
						truncated = true
						break
					}
					a.Source = "本会话引用附件"
					doc.Attachments = append(doc.Attachments, a)
				}
			} else {
				lines = append(lines, "[引用内容不可用：QQ 未下发原文/附件，本会话缓存也没有。请用户转发原消息、重新发送附件或导出聊天记录；不能据此声称读过原内容。]")
			}
		}
	}
	walk(content, 0)
	if len(lines) == 0 && len(doc.Attachments) == 0 {
		lines = append(lines, "[引用消息没有携带可读取的原文或附件，请用户重新发送原内容。]")
	}
	if truncated {
		lines = append(lines, "[引用层数、内容或附件超过本次输入上限，部分未读取；请分批发送。]")
	}
	doc.Text = strings.Join(lines, "\n\n")
	c.mu.Lock()
	if c.inputs == nil {
		c.inputs = make(map[string]cachedInput)
	}
	for id, item := range c.inputs {
		if time.Since(item.At) >= time.Hour {
			delete(c.inputs, id)
		}
	}
	for _, id := range []string{m.MsgID, content.index()} {
		if id != "" {
			c.inputs[scope+"\x00"+id] = cachedInput{Document: doc, At: time.Now()}
		}
	}
	for len(c.inputs) > 256 {
		var oldest string
		var at time.Time
		for id, item := range c.inputs {
			if oldest == "" || item.At.Before(at) {
				oldest, at = id, item.At
			}
		}
		delete(c.inputs, oldest)
	}
	c.mu.Unlock()
	return doc
}

func (d inputDocument) summary() string {
	text := d.Text
	for i, a := range d.Attachments {
		text += fmt.Sprintf("\n[附件 %d · %s · %s：%s，等待下载/解析]", i+1, a.Source, inputText(a.ContentType, 100), a.Filename)
	}
	return text
}
