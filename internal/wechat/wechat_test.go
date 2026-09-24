package wechat

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHeadersAndSendText(t *testing.T) {
	var gotPath, gotAuth, gotAuthType, gotUIN, gotAppID string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		gotAuthType = r.Header.Get("AuthorizationType")
		gotUIN = r.Header.Get("X-WECHAT-UIN")
		gotAppID = r.Header.Get("iLink-App-Id")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"message_id":"m1","ret":0}`))
	}))
	t.Cleanup(srv.Close)

	cli := NewClient(srv.URL, "tok-123", "MineAgent/0.1.0 (test)")
	if err := cli.SendText(context.Background(), "user1", "你好", "ctx-token"); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/ilink/bot/sendmessage" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotAuth != "Bearer tok-123" || gotAuthType != "ilink_bot_token" {
		t.Fatalf("auth = %q type = %q", gotAuth, gotAuthType)
	}
	if gotUIN == "" || gotAppID != "bot" {
		t.Fatalf("uin/appid = %q/%q", gotUIN, gotAppID)
	}
	msg, _ := gotBody["msg"].(map[string]any)
	if msg["to_user_id"] != "user1" || msg["context_token"] != "ctx-token" || msg["message_type"] != float64(MsgTypeBot) {
		t.Fatalf("msg = %+v", msg)
	}
	items, _ := msg["item_list"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %+v", items)
	}
	it0, _ := items[0].(map[string]any)
	txt, _ := it0["text_item"].(map[string]any)
	if txt["text"] != "你好" {
		t.Fatalf("text = %+v", txt)
	}
	if _, ok := gotBody["base_info"]; !ok {
		t.Fatal("base_info missing")
	}
}

func TestSendTextRetError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ret":-1,"errmsg":"bad"}`))
	}))
	t.Cleanup(srv.Close)
	cli := NewClient(srv.URL, "tok", "")
	if err := cli.SendText(context.Background(), "u", "x", ""); err == nil {
		t.Fatal("want error on ret!=0")
	}
}

func TestGetUpdatesParsesMsgs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/ilink/bot/getupdates" {
			t.Errorf("path = %s", r.URL.Path)
		}
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &body)
		if body["get_updates_buf"] != "buf-old" {
			t.Errorf("buf = %v", body["get_updates_buf"])
		}
		_, _ = w.Write([]byte(`{
			"ret":0,"get_updates_buf":"buf-new",
			"msgs":[{
				"message_id":"42","from_user_id":"wxid_a","message_type":1,
				"context_token":"ctx1",
				"item_list":[{"type":1,"text_item":{"text":"你好"}}]
			}]
		}`))
	}))
	t.Cleanup(srv.Close)

	cli := NewClient(srv.URL, "tok", "")
	resp, err := cli.GetUpdates(context.Background(), "buf-old")
	if err != nil {
		t.Fatal(err)
	}
	if resp.GetUpdatesBuf != "buf-new" || len(resp.Msgs) != 1 {
		t.Fatalf("resp = %+v", resp)
	}
	m := resp.Msgs[0]
	if m.FromUserID != "wxid_a" || m.ContextToken != "ctx1" || m.ItemList[0].TextItem.Text != "你好" {
		t.Fatalf("msg = %+v", m)
	}
}

func TestStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wechat.json")
	st := &State{
		Token: "t", BaseURL: "https://x", ILinkBotID: "b", ILinkUserID: "u",
		GetUpdatesBuf: "buf", ContextTokens: map[string]string{"u1": "c1"},
	}
	if err := SaveState(path, st); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Token != "t" || got.GetUpdatesBuf != "buf" || got.ContextTokens["u1"] != "c1" {
		t.Fatalf("got %+v", got)
	}
	// 不存在时返回空 State 而不是报错。
	empty, err := LoadState(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || empty.Token != "" {
		t.Fatalf("empty = %+v err=%v", empty, err)
	}
}

func TestChunkRunes(t *testing.T) {
	if got := chunkRunes("短", 10); len(got) != 1 || got[0] != "短" {
		t.Fatalf("got %v", got)
	}
	long := strings.Repeat("字", 3001)
	got := chunkRunes(long, 1500)
	if len(got) != 3 {
		t.Fatalf("chunks = %d", len(got))
	}
	if len([]rune(got[0])) != 1500 || len([]rune(got[2])) != 1 {
		t.Fatalf("sizes = %d/%d", len([]rune(got[0])), len([]rune(got[2])))
	}
}

func TestFetchQRCodeAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/ilink/bot/get_bot_qrcode"):
			if r.URL.Query().Get("bot_type") != "3" {
				t.Errorf("bot_type = %s", r.URL.Query().Get("bot_type"))
			}
			_, _ = w.Write([]byte(`{"qrcode":"QR1","qrcode_img_content":"https://weixin.qq.com/x/abc"}`))
		case strings.HasPrefix(r.URL.Path, "/ilink/bot/get_qrcode_status"):
			_, _ = w.Write([]byte(`{"status":"scaned"}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	t.Cleanup(srv.Close)

	cli := NewClient(srv.URL, "", "")
	qr, err := cli.FetchQRCode(context.Background(), nil)
	if err != nil || qr.QRCode != "QR1" || qr.QRCodeImgContent == "" {
		t.Fatalf("qr = %+v err=%v", qr, err)
	}
	st, err := cli.PollQRStatus(context.Background(), qr.QRCode, "")
	if err != nil || st.Status != "scaned" {
		t.Fatalf("status = %+v err=%v", st, err)
	}
}
