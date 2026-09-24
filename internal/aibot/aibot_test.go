package aibot

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSplitTarget(t *testing.T) {
	kind, id, req := splitTarget("c2c:JiangZhiKun:req-abc123")
	if kind != "c2c" || id != "JiangZhiKun" || req != "req-abc123" {
		t.Fatalf("got %q %q %q", kind, id, req)
	}
	kind, id, req = splitTarget("group:wrOgAAA")
	if kind != "group" || id != "wrOgAAA" || req != "" {
		t.Fatalf("got %q %q %q", kind, id, req)
	}
	if kind, _, _ := splitTarget("bad"); kind != "" {
		t.Fatal("short should fail")
	}
}

func TestRespondEnvelopeTransmitsReqID(t *testing.T) {
	raw, err := json.Marshal(respondEnvelope("req-callback-1", "# 标题\n正文"))
	if err != nil {
		t.Fatal(err)
	}
	var env map[string]any
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if env["cmd"] != cmdRespondMsg {
		t.Fatalf("cmd = %v", env["cmd"])
	}
	hd, _ := env["headers"].(map[string]any)
	if hd["req_id"] != "req-callback-1" {
		t.Fatalf("req_id = %v（被动回复必须透传回调 req_id）", hd["req_id"])
	}
	body, _ := env["body"].(map[string]any)
	if body["msgtype"] != "markdown" {
		t.Fatalf("msgtype = %v", body["msgtype"])
	}
	md, _ := body["markdown"].(map[string]any)
	if !strings.Contains(md["content"].(string), "标题") {
		t.Fatalf("content = %v", md["content"])
	}
}

func TestSendEnvelopeChatType(t *testing.T) {
	raw, _ := json.Marshal(sendEnvelope("JiangZhiKun", chatTypeSingle, "hi"))
	var env map[string]any
	_ = json.Unmarshal(raw, &env)
	if env["cmd"] != cmdSendMsg {
		t.Fatalf("cmd = %v", env["cmd"])
	}
	body, _ := env["body"].(map[string]any)
	if body["chatid"] != "JiangZhiKun" || body["chat_type"] != float64(1) {
		t.Fatalf("body = %+v", body)
	}
	hd, _ := env["headers"].(map[string]any)
	if hd["req_id"] == "" {
		t.Fatal("主动推送要自己生成 req_id")
	}
}

func TestNewReqIDUnique(t *testing.T) {
	a, b := newReqID(), newReqID()
	if a == b || a == "" {
		t.Fatalf("req ids not unique: %q %q", a, b)
	}
}
