package wecom

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// CallbackServer 是企业微信回调入口（自建应用"设置 API 接收"指向本机）。
//
// 流程（document/path/90930）：
//   - GET  ?msg_signature&timestamp&nonce&echostr
//     验签 -> 解密 echostr -> 1 秒内原样返回明文（不能带引号/BOM/换行）。
//   - POST ?msg_signature&timestamp&nonce，body=<xml><Encrypt>..</Encrypt></xml>
//     验签 -> 解密得业务 XML -> 归一化 -> 交给 onMessage（异步）
//     立即回空串 200（表示"不被动回复"），回复走主动 SendText，
//     避免 5 秒超时；平台对网络失败会重试 3 次，我们靠 msgid 去重。
//
// 只监听 POST/GET /wecom，其它路径 404。微信只允许 80/443 端口回调，
// 所以 cfg.WeCom.Port 默认 80；本机需要腾讯云控制台放行该端口。
type CallbackServer struct {
	cfg  Config
	log  *slog.Logger
	srv  *http.Server

	onMessage func(InboundMessage)
}

type Config struct {
	CorpID      string
	Token       string
	EncodingAES string
	Port        int
}

func NewCallbackServer(cfg Config, log *slog.Logger, onMessage func(InboundMessage)) *CallbackServer {
	return &CallbackServer{cfg: cfg, log: log, onMessage: onMessage}
}

func (s *CallbackServer) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/wecom", s.handle)
	s.srv = &http.Server{
		Addr:              fmt.Sprintf(":%d", s.cfg.Port),
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	s.log.Info("wecom callback listening", "port", s.cfg.Port, "path", "/wecom")
	return s.srv.ListenAndServe()
}

func (s *CallbackServer) Stop() {
	if s.srv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = s.srv.Shutdown(shutdownCtx)
	}
}

func (s *CallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	signature := q.Get("msg_signature")
	timestamp := q.Get("timestamp")
	nonce := q.Get("nonce")

	switch r.Method {
	case http.MethodGet:
		echostr := q.Get("echostr")
		if !VerifySignature(s.cfg.Token, timestamp, nonce, echostr, signature) {
			s.log.Warn("wecom verify: bad signature")
			http.Error(w, "bad signature", http.StatusForbidden)
			return
		}
		plain, err := Decrypt(s.cfg.EncodingAES, echostr, s.cfg.CorpID)
		if err != nil {
			s.log.Warn("wecom verify: decrypt failed", "err", err)
			http.Error(w, "decrypt failed", http.StatusForbidden)
			return
		}
		// 原样返回明文，不能加引号/BOM/换行。
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte(plain))
		s.log.Info("wecom callback verified")
		return
	case http.MethodPost:
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "read body", http.StatusBadRequest)
			return
		}
		var env struct {
			ToUserName string `xml:"ToUserName"`
			AgentID    string `xml:"AgentID"`
			Encrypt    string `xml:"Encrypt"`
		}
		if err := xml.Unmarshal(body, &env); err != nil {
			s.log.Warn("wecom callback: bad envelope", "err", err)
			http.Error(w, "bad xml", http.StatusBadRequest)
			return
		}
		if !VerifySignature(s.cfg.Token, timestamp, nonce, env.Encrypt, signature) {
			s.log.Warn("wecom callback: bad signature")
			http.Error(w, "bad signature", http.StatusForbidden)
			return
		}
		plain, err := Decrypt(s.cfg.EncodingAES, env.Encrypt, s.cfg.CorpID)
		if err != nil {
			s.log.Warn("wecom callback: decrypt failed", "err", err)
			http.Error(w, "decrypt failed", http.StatusForbidden)
			return
		}
		var msg callbackText
		if err := xml.Unmarshal([]byte(plain), &msg); err != nil {
			s.log.Warn("wecom callback: bad message xml", "err", err)
			http.Error(w, "bad message", http.StatusBadRequest)
			return
		}
		// 空串 200 = 收到、不被动回复（回复异步走主动 SendText）。
		_, _ = w.Write([]byte(""))
		s.dispatch(msg)
		return
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *CallbackServer) dispatch(msg callbackText) {
	if msg.MsgType != "text" {
		s.log.Debug("wecom callback: skip non-text", "type", msg.MsgType, "from", msg.FromUserName)
		return
	}
	in := InboundMessage{
		MsgID:  msg.MsgID,
		UserID: msg.FromUserName,
		Text:   msg.Content,
	}
	if msg.ChatID != "" {
		in.Kind = "group"
		in.GroupChatID = msg.ChatID
	} else {
		in.Kind = "c2c"
	}
	if s.onMessage == nil {
		return
	}
	func() {
		defer func() {
			if r := recover(); r != nil {
				s.log.Error("wecom onMessage panic", "err", r)
			}
		}()
		s.onMessage(in)
	}()
}
