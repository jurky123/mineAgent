package qq

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
)

func testGateway() *Gateway {
	return &Gateway{log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func mustRaw(t *testing.T, v string) json.RawMessage {
	t.Helper()
	var m json.RawMessage
	if err := json.Unmarshal([]byte(v), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestDispatchC2C(t *testing.T) {
	g := testGateway()
	var got *InboundMessage
	g.onMessage = func(m InboundMessage) { got = &m }
	g.dispatch("C2C_MESSAGE_CREATE", "evt-1", mustRaw(t, `{
		"id": "ROBOT1.0_abc",
		"author": {"id": "U1", "user_openid": "U1", "union_openid": "UN1", "username": "小明", "bot": false},
		"content": "服务器卡不卡",
		"message_type": 0
	}`))
	if got == nil {
		t.Fatal("no message emitted")
	}
	if got.Kind != "c2c" || got.UserOpenID != "U1" || got.UnionOpenID != "UN1" {
		t.Fatalf("got %+v", got)
	}
	if got.Text != "服务器卡不卡" || got.MsgID != "ROBOT1.0_abc" || got.EventID != "evt-1" {
		t.Fatalf("got %+v", got)
	}
}

func TestDispatchC2CIgnoresBotAndNonText(t *testing.T) {
	g := testGateway()
	n := 0
	g.onMessage = func(m InboundMessage) { n++ }
	// 机器人自己发的回声不处理。
	g.dispatch("C2C_MESSAGE_CREATE", "e", mustRaw(t, `{
		"id": "x", "author": {"id": "B", "bot": true}, "content": "hi", "message_type": 0}`))
	// 非文本（卡片/附件）v1 跳过。
	g.dispatch("C2C_MESSAGE_CREATE", "e", mustRaw(t, `{
		"id": "x", "author": {"id": "U", "bot": false}, "content": "[卡片]", "message_type": 3}`))
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}

func TestDispatchGroupAt(t *testing.T) {
	g := testGateway()
	var got *InboundMessage
	g.onMessage = func(m InboundMessage) { got = &m }
	g.dispatch("GROUP_AT_MESSAGE_CREATE", "evt-2", mustRaw(t, `{
		"id": "ROBOT1.0_def",
		"author": {"id": "M1", "member_openid": "M1", "member_role": "owner", "username": "小华", "bot": false},
		"content": " 在线有谁 ",
		"group_openid": "G1",
		"message_type": 0
	}`))
	if got == nil {
		t.Fatal("no message emitted")
	}
	if got.Kind != "group_at" || got.GroupOpenID != "G1" || got.MemberOpenID != "M1" {
		t.Fatalf("got %+v", got)
	}
	if got.MemberRole != "owner" {
		t.Fatalf("role = %q", got.MemberRole)
	}
}

func TestDispatchIgnoresOthers(t *testing.T) {
	g := testGateway()
	n := 0
	g.onMessage = func(m InboundMessage) { n++ }
	for _, ev := range []string{"FRIEND_ADD", "GROUP_ADD_ROBOT", "C2C_MSG_REJECT", "INTERACTION_CREATE"} {
		g.dispatch(ev, "e", mustRaw(t, `{}`))
	}
	if n != 0 {
		t.Fatalf("n = %d, want 0", n)
	}
}

func TestSplitTarget(t *testing.T) {
	kind, id, msgID := splitTarget("c2c:U1:ROBOT1.0_x")
	if kind != "c2c" || id != "U1" || msgID != "ROBOT1.0_x" {
		t.Fatalf("got %q %q %q", kind, id, msgID)
	}
	kind, _, _ = splitTarget("bad")
	if kind != "" {
		t.Fatalf("bad target should give empty kind, got %q", kind)
	}
}

func TestIsFatalGatewayErr(t *testing.T) {
	if !isFatalGatewayErr(fmt.Errorf("gateway url: qq gateway: http 401 {\"code\":11298,\"err_code\":40023002}")) {
		t.Fatal("whitelist error should be fatal")
	}
	if !isFatalGatewayErr(fmt.Errorf("接口访问源IP不在白名单")) {
		t.Fatal("whitelist message should be fatal")
	}
	if isFatalGatewayErr(fmt.Errorf("gateway url: qq gateway: http 400 {\"code\":100017}")) {
		t.Fatal("rate limit should not be fatal")
	}
	if isFatalGatewayErr(fmt.Errorf("dial: connection refused")) {
		t.Fatal("dial error should not be fatal")
	}
	if isFatalGatewayErr(nil) {
		t.Fatal("nil should not be fatal")
	}
}

func TestSplitImageTarget(t *testing.T) {
	// 真实 msgID 含冒号：ROBOT1.0_xxx:xxx:...!
	kind, id, msgID, path := splitImageTarget("c2c:U1:ROBOT1.0_abc:def.ghi!:weather_card.png")
	if kind != "c2c" || id != "U1" || msgID != "ROBOT1.0_abc:def.ghi!" || path != "weather_card.png" {
		t.Fatalf("got %q %q %q %q", kind, id, msgID, path)
	}
	kind, id, msgID, path = splitImageTarget("group:G1:ROBOT1.0_x:y!:sub/plot.png")
	if kind != "group" || id != "G1" || msgID != "ROBOT1.0_x:y!" || path != "sub/plot.png" {
		t.Fatalf("got %q %q %q %q", kind, id, msgID, path)
	}
	// 非法：段数不对 / 非 ROBOT / path 含冒号 / kind 非法。
	for _, bad := range []string{
		"c2c:U1:MSG1",
		"c2c:U1:MSG1:xxx:a:b",
		"c2c:U1:NOTROBOT:x:p.png",
		"dm:U1:ROBOT1.0_a:b:p.png",
		"c2c:U1:ROBOT1.0_a:b:",
	} {
		if k, _, _, _ := splitImageTarget(bad); k != "" {
			t.Fatalf("%q should fail", bad)
		}
	}
}
