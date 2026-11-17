package qq

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type observedMember struct {
	alias, openID, name, avatar, role string
	seen                              time.Time
}
type memberContextKey struct{}
type memberContext struct {
	channel             *Channel
	scope, target, self string
	mu                  sync.Mutex
	sent                map[string]bool
}

func memberAlias(id string) string {
	if id == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(id))
	return fmt.Sprintf("user-%x", sum[:8])
}
func safeAvatar(raw string) string {
	if len(raw) > 4096 {
		return ""
	}
	u, e := url.Parse(raw)
	if e != nil || u.User != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return ""
	}
	return u.String()
}
func (c *Channel) observeMembers(scope string, m InboundMessage) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.members == nil {
		c.members = make(map[string]map[string]observedMember)
	}
	now := time.Now()
	for key, people := range c.members {
		for id, p := range people {
			if now.Sub(p.seen) > 24*time.Hour {
				delete(people, id)
			}
		}
		if len(people) == 0 {
			delete(c.members, key)
		}
	}
	if c.members[scope] == nil {
		if len(c.members) >= 128 {
			return
		}
		c.members[scope] = make(map[string]observedMember)
	}
	add := func(id, openid, name, avatar, role string) {
		alias := memberAlias(id)
		if alias == "" || openid == "" || strings.ContainsAny(openid, "<>! @\r\n\t") {
			return
		}
		people := c.members[scope]
		old, exists := people[alias]
		if !exists && len(people) >= 128 {
			var oldest string
			var at time.Time
			for key, p := range people {
				if oldest == "" || p.seen.Before(at) {
					oldest, at = key, p.seen
				}
			}
			delete(people, oldest)
		}
		old.alias, old.openID, old.seen = alias, openid, now
		if name != "" {
			old.name = inputText(name, 80)
		}
		if avatar = safeAvatar(avatar); avatar != "" {
			old.avatar = avatar
		}
		if role != "" {
			old.role = inputText(role, 40)
		}
		people[alias] = old
	}
	add(speakerID(m), firstNonEmpty(m.MemberOpenID, m.UserOpenID), m.Username, m.Avatar, m.MemberRole)
	if strings.HasPrefix(scope, "qq:group:") {
		for _, u := range m.content.Mentions {
			if !u.Bot && !u.IsYou {
				add(quotedSpeakerID(scope, u), firstNonEmpty(u.MemberOpenID, u.ID), firstNonEmpty(u.Username, u.Nickname), u.Avatar, u.MemberRole)
			}
		}
	}
}
func (mc *memberContext) lookup(alias string) (observedMember, bool) {
	if alias == "" || alias == "self" {
		alias = mc.self
	}
	mc.channel.mu.Lock()
	defer mc.channel.mu.Unlock()
	p, ok := mc.channel.members[mc.scope][alias]
	return p, ok && time.Since(p.seen) <= 24*time.Hour
}
func memberJSON(p observedMember) map[string]any {
	return map[string]any{"id": p.alias, "nickname": p.name, "avatar": p.avatar, "groupRole": p.role, "observedAt": p.seen.UTC().Format(time.RFC3339), "source": "QQ平台消息已提供字段；空值表示未提供，非实时个人主页查询"}
}

type memberTool struct{}

func (*memberTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "qq_member_profile", Desc: "查询当前群/私聊实际收到的成员资料。省略user列出近期成员，self为本次发言人，user-编号可选人；只返回平台给出的昵称/头像/群角色，未提供的其他个人资料不可查询，不猜QQ号。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"user": {Type: schema.String, Desc: "self或当前群内user-编号，省略列出近期成员"}, "query": {Type: schema.String, Desc: "列表按昵称筛选"}, "offset": {Type: schema.Integer, Desc: "列表起始位置，每页最多20人"}})}, nil
}
func (*memberTool) InvokableRun(ctx context.Context, raw string, _ ...tool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mc, _ := ctx.Value(memberContextKey{}).(*memberContext)
	var a struct {
		User   string `json:"user"`
		Query  string `json:"query"`
		Offset int    `json:"offset"`
	}
	if mc == nil || json.Unmarshal([]byte(raw), &a) != nil || a.Offset < 0 || a.Offset > 128 {
		return `{"error":"仅限当前QQ任务"}`, nil
	}
	var data any
	if a.User != "" {
		p, ok := mc.lookup(a.User)
		if !ok {
			return `{"error":"该成员未在当前群近期消息中出现"}`, nil
		}
		data = memberJSON(p)
	} else {
		mc.channel.mu.Lock()
		people := mc.channel.members[mc.scope]
		list := make([]map[string]any, 0, len(people))
		for _, p := range people {
			if time.Since(p.seen) <= 24*time.Hour && (a.Query == "" || strings.Contains(strings.ToLower(p.name), strings.ToLower(a.Query))) {
				list = append(list, map[string]any{"id": p.alias, "nickname": p.name, "groupRole": p.role, "avatarAvailable": p.avatar != "", "observedAt": p.seen.UTC().Format(time.RFC3339)})
			}
		}
		mc.channel.mu.Unlock()
		sort.Slice(list, func(i, j int) bool {
			if list[i]["observedAt"] == list[j]["observedAt"] {
				return list[i]["id"].(string) < list[j]["id"].(string)
			}
			return list[i]["observedAt"].(string) > list[j]["observedAt"].(string)
		})
		total := len(list)
		start := min(a.Offset, total)
		end := min(start+20, total)
		data = map[string]any{"self": mc.self, "members": list[start:end], "nextOffset": end, "more": end < total, "completeGroupRoster": false, "unavailable": "QQ号、年龄、签名等平台未提供的资料不可查询"}
	}
	b, _ := json.Marshal(data)
	return string(b), nil
}

type mentionTool struct{}

func (*mentionTool) Info(context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "qq_mention", Desc: "在当前QQ群发送包含@一位已观察成员的文字回复。先用qq_member_profile选user-编号；不能跨群或@全体，发送成功后不要重复同一回复。", ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{"user": {Type: schema.String, Required: true, Desc: "当前群的user-编号或self"}, "text": {Type: schema.String, Required: true, Desc: "与@一起发送的回复正文，最多800字"}})}, nil
}
func (*mentionTool) InvokableRun(ctx context.Context, raw string, _ ...tool.Option) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	mc, _ := ctx.Value(memberContextKey{}).(*memberContext)
	var a struct{ User, Text string }
	if mc == nil || json.Unmarshal([]byte(raw), &a) != nil || !strings.HasPrefix(mc.scope, "qq:group:") {
		return `{"error":"仅支持当前QQ群任务"}`, nil
	}
	p, ok := mc.lookup(a.User)
	if !ok {
		return `{"error":"成员不属于当前群近期记录，请先查询qq_member_profile"}`, nil
	}
	if strings.TrimSpace(a.Text) == "" || len([]rune(a.Text)) > 800 || strings.Contains(a.Text, "<@") || strings.Contains(a.Text, "@everyone") {
		return `{"error":"回复正文无效；不可嵌入额外@或@全体"}`, nil
	}
	mc.mu.Lock()
	defer mc.mu.Unlock()
	key := p.alias + "\n" + a.Text
	if mc.sent[key] {
		return `{"error":"此回复已提交过，未重复发送；如状态不明请核查"}`, nil
	}
	mc.sent[key] = true
	if err := mc.channel.sendText(ctx, mc.target, "<@!"+p.openID+"> "+a.Text); err != nil {
		return `{"error":"发送未确认，未自动重试，请核查QQ端"}`, nil
	}
	return `{"ok":true,"status":"平台已接受@文字回复；实际提醒受QQ权限与客户端影响"}`, nil
}
