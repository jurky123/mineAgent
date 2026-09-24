package wechat

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// State 是登录凭证与轮询游标的本地持久化（默认 data/wechat.json）。
// 与 QQ/企微不同，个人微信走扫码登录，凭证是长期 token，需落盘。
type State struct {
	Token       string `json:"token"`
	BaseURL     string `json:"baseUrl"`
	ILinkBotID  string `json:"ilinkBotId"`
	ILinkUserID string `json:"ilinkUserId"`
	LoggedInAt  int64  `json:"loggedInAt"`

	// GetUpdatesBuf 是长轮询游标，落盘以避免重启后重复收消息。
	GetUpdatesBuf string `json:"getUpdatesBuf,omitempty"`
	// ContextTokens: 对方 userid -> context_token（回复时必须透传）。
	ContextTokens map[string]string `json:"contextTokens,omitempty"`
}

func LoadState(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &State{}, nil
		}
		return nil, err
	}
	var st State
	if err := json.Unmarshal(b, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if st.ContextTokens == nil {
		st.ContextTokens = map[string]string{}
	}
	return &st, nil
}

func SaveState(path string, st *State) error {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	// 凭证文件用 0600。
	return os.WriteFile(path, b, 0o600)
}

// Login 跑扫码登录：拿二维码 -> 打印链接/可选二维码图 -> 轮询状态直到 confirmed。
// 二维码 5 分钟过期，最多自动刷新 3 次。ctx 取消即中止。
// qrOutPath 非空时把二维码 PNG 写到该路径（方便转发给用户扫）。
func Login(ctx context.Context, log *slog.Logger, qrOutPath string) (*State, error) {
	cli := NewClient(defaultBaseURL, "", "")
	var (
		qr       QRCodeResponse
		verify   string
		refreshes int
	)
	loadQR := func() error {
		var err error
		qr, err = cli.FetchQRCode(ctx, nil)
		if err != nil {
			return fmt.Errorf("fetch qrcode: %w", err)
		}
		log.Info("wechat login: qrcode fetched")
		fmt.Printf("二维码内容（用手机微信扫码；也可以直接在手机微信里打开）:\n%s\n", qr.QRCodeImgContent)
		if qrOutPath != "" {
			if err := renderQRPNG(qr.QRCodeImgContent, qrOutPath); err != nil {
				log.Warn("render qr png failed", "err", err)
			} else {
				fmt.Printf("二维码图片: %s\n", qrOutPath)
			}
		}
		return nil
	}
	if err := loadQR(); err != nil {
		return nil, err
	}

	// 腾讯侧二维码有效期偏短（约 2 分钟），本地按 2 分钟主动续期，最多 8 次。
	const qrTTL = 2 * time.Minute
	const maxRefreshes = 8
	deadline := time.Now().Add(qrTTL)
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if time.Now().After(deadline) {
			if refreshes >= maxRefreshes {
				return nil, fmt.Errorf("二维码多次过期，登录未完成")
			}
			refreshes++
			fmt.Println("（上一个二维码已过期，已自动换新，请用最新的链接/图片）")
			log.Info("wechat login: qrcode refreshed", "count", refreshes)
			if err := loadQR(); err != nil {
				return nil, err
			}
			deadline = time.Now().Add(qrTTL)
			continue
		}
		st, err := cli.PollQRStatus(ctx, qr.QRCode, verify)
		if err != nil {
			log.Warn("poll qr status failed", "err", err)
			time.Sleep(time.Second)
			continue
		}
		switch st.Status {
		case "wait":
			// 长轮询正常返回（含客户端超时），继续。
		case "scaned":
			log.Info("wechat login: scanned, waiting for confirm on phone")
		case "scaned_but_redirect":
			if st.RedirectHost != "" {
				cli.SetBase("https://" + st.RedirectHost)
				log.Info("wechat login: redirected to idc", "host", st.RedirectHost)
			}
		case "need_verifycode":
			fmt.Print("需要输入手机微信上显示的验证码: ")
			var code string
			if _, err := fmt.Scanln(&code); err != nil || strings.TrimSpace(code) == "" {
				log.Warn("wechat login: verify code required but not provided")
				time.Sleep(2 * time.Second)
				continue
			}
			verify = strings.TrimSpace(code)
		case "verify_code_blocked":
			return nil, fmt.Errorf("验证码多次错误，登录被锁定")
		case "binded_redirect":
			return nil, fmt.Errorf("该微信已绑定过其它 bot 实例；如需重新登录请先在微信 ClawBot 插件里解绑，或直接使用现有凭证")
		case "expired":
			// 让 deadline 分支刷新二维码。
			deadline = time.Now().Add(-time.Second)
		case "confirmed":
			if st.BotToken == "" {
				return nil, fmt.Errorf("confirmed 但未返回 bot_token")
			}
			state := &State{
				Token:       st.BotToken,
				BaseURL:     defaultBaseURL,
				ILinkBotID:  st.ILinkBotID,
				ILinkUserID: st.ILinkUserID,
				LoggedInAt:  time.Now().UnixMilli(),
				ContextTokens: map[string]string{},
			}
			if st.BaseURL != "" {
				state.BaseURL = st.BaseURL
			}
			log.Info("wechat login: confirmed",
				"ilinkBotId", st.ILinkBotID, "ilinkUserId", st.ILinkUserID, "baseUrl", state.BaseURL)
			return state, nil
		default:
			log.Debug("wechat login: unknown status", "status", st.Status)
		}
		time.Sleep(500 * time.Millisecond)
	}
}
