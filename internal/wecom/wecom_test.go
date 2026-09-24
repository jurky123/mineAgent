package wecom

import (
	"encoding/base64"
	"encoding/xml"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// 32 字节 AES key 的 base64（去掉末尾 =），企微 EncodingAESKey 的形态。
func testAESKey() string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = byte('a' + i%26)
	}
	return strings.TrimRight(base64.StdEncoding.EncodeToString(raw), "=")
}

func TestCryptoRoundTrip(t *testing.T) {
	key := testAESKey()
	corpID := "ww1234567890abcdef"
	plain := `<xml><ToUserName>` + corpID + `</ToUserName><FromUserName>zhangsan</FromUserName><Content>你好</Content></xml>`
	enc, err := Encrypt(key, plain, corpID)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Decrypt(key, enc, corpID)
	if err != nil {
		t.Fatal(err)
	}
	if got != plain {
		t.Fatalf("got %q", got)
	}
	// 错误 corpID 必须拒绝：加密时尾部拼的是别的 corpID，解密方按自己的校验。
	encBad, err := Encrypt(key, plain, "ww0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Decrypt(key, encBad, corpID); err == nil {
		t.Fatal("corpId mismatch should fail")
	}
	// 坏密文。
	if _, err := Decrypt(key, "!!!not-base64!!!", corpID); err == nil {
		t.Fatal("bad base64 should fail")
	}
}

func TestSignAndVerify(t *testing.T) {
	sig := Sign("tok", "123", "456", "enc")
	if !VerifySignature("tok", "123", "456", "enc", sig) {
		t.Fatal("verify should pass")
	}
	if VerifySignature("tok", "123", "456", "enc2", sig) {
		t.Fatal("different encrypt should fail")
	}
	if VerifySignature("tok", "abc", "456", "enc", sig) {
		t.Fatal("different timestamp should fail")
	}
}

func TestCallbackGETVerification(t *testing.T) {
	key := testAESKey()
	corpID := "wwtest"
	token := "tok123"
	echostrEnc, err := Encrypt(key, "hello-echo", corpID)
	if err != nil {
		t.Fatal(err)
	}
	srv := &CallbackServer{
		cfg: Config{CorpID: corpID, Token: token, EncodingAES: key, Port: 0},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ts := httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(ts.Close)

	tsStr, nonce := "111", "222"
	sig := Sign(token, tsStr, nonce, echostrEnc)
	q := url.Values{}
	q.Set("msg_signature", sig)
	q.Set("timestamp", tsStr)
	q.Set("nonce", nonce)
	q.Set("echostr", echostrEnc)
	resp, err := http.Get(ts.URL + "/wecom?" + q.Encode())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != "hello-echo" {
		t.Fatalf("status=%d body=%q", resp.StatusCode, body)
	}
	// 坏签名 -> 403。
	badQ := url.Values{}
	badQ.Set("msg_signature", "deadbeef")
	badQ.Set("timestamp", tsStr)
	badQ.Set("nonce", nonce)
	badQ.Set("echostr", echostrEnc)
	resp2, _ := http.Get(ts.URL + "/wecom?" + badQ.Encode())
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusForbidden {
		t.Fatalf("bad sig status=%d", resp2.StatusCode)
	}
}

func TestCallbackPOSTDispatch(t *testing.T) {
	key := testAESKey()
	corpID := "wwtest"
	token := "tok123"
	var got *InboundMessage
	srv := &CallbackServer{
		cfg: Config{CorpID: corpID, Token: token, EncodingAES: key},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		onMessage: func(m InboundMessage) { got = &m },
	}
	ts := httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(ts.Close)

	inner := `<xml><ToUserName>` + corpID + `</ToUserName><FromUserName>zhangsan</FromUserName><CreateTime>1</CreateTime><MsgType>text</MsgType><Content>服务器卡不卡</Content><MsgId>MSG9</MsgId><AgentID>1000002</AgentID></xml>`
	enc, _ := Encrypt(key, inner, corpID)
	body, _ := xml.Marshal(map[string]string{})
	_ = body
	payload := `<xml><ToUserName>` + corpID + `</ToUserName><AgentID>1000002</AgentID><Encrypt>` + enc + `</Encrypt></xml>`
	tsStr, nonce := "333", "444"
	sig := Sign(token, tsStr, nonce, enc)
	q := url.Values{}
	q.Set("msg_signature", sig)
	q.Set("timestamp", tsStr)
	q.Set("nonce", nonce)
	resp, err := http.Post(ts.URL+"/wecom?"+q.Encode(), "text/xml", strings.NewReader(payload))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	if got == nil {
		t.Fatal("no dispatch")
	}
	if got.Kind != "c2c" || got.UserID != "zhangsan" || got.Text != "服务器卡不卡" || got.MsgID != "MSG9" {
		t.Fatalf("got %+v", got)
	}
}

func TestCallbackPOSTSkipsNonText(t *testing.T) {
	key := testAESKey()
	corpID := "wwtest"
	token := "tok"
	called := 0
	srv := &CallbackServer{
		cfg: Config{CorpID: corpID, Token: token, EncodingAES: key},
		log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		onMessage: func(m InboundMessage) { called++ },
	}
	ts := httptest.NewServer(http.HandlerFunc(srv.handle))
	t.Cleanup(ts.Close)
	inner := `<xml><ToUserName>` + corpID + `</ToUserName><FromUserName>u1</FromUserName><MsgType>image</MsgType><MsgId>IMG1</MsgId></xml>`
	enc, _ := Encrypt(key, inner, corpID)
	payload := `<xml><ToUserName>` + corpID + `</ToUserName><Encrypt>` + enc + `</Encrypt></xml>`
	sig := Sign(token, "1", "2", enc)
	q := url.Values{}
	q.Set("msg_signature", sig)
	q.Set("timestamp", "1")
	q.Set("nonce", "2")
	resp, _ := http.Post(ts.URL+"/wecom?"+q.Encode(), "text/xml", strings.NewReader(payload))
	resp.Body.Close()
	if called != 0 {
		t.Fatalf("called=%d", called)
	}
}

func TestChannelTargetSplit(t *testing.T) {
	kind, id, rest := splitTarget("c2c:zhangsan")
	if kind != "c2c" || id != "zhangsan" || rest != "" {
		t.Fatalf("got %q %q %q", kind, id, rest)
	}
	kind, id, rest = splitTarget("group:wrOgAAA")
	if kind != "group" || id != "wrOgAAA" || rest != "" {
		t.Fatalf("got %q %q %q", kind, id, rest)
	}
	if kind, _, _ := splitTarget("bad"); kind != "" {
		t.Fatal("short should fail")
	}
	kind, id, path := splitImageTarget("c2c:zhangsan:plot.png")
	if kind != "c2c" || id != "zhangsan" || path != "plot.png" {
		t.Fatalf("got %q %q %q", kind, id, path)
	}
	for _, bad := range []string{"c2c:zhangsan", "c2c:zhangsan:", "dm:u:p.png", "c2c:u:a:b.png"} {
		if k, _, _ := splitImageTarget(bad); k != "" {
			t.Fatalf("%q should fail", bad)
		}
	}
}
