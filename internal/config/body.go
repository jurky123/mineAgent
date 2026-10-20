package config

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"net"
	"net/url"
	"regexp"
	"time"
)

// Body is optional. Enabling it never grants Minecraft names control rights.
type Body struct {
	Enabled           bool         `json:"enabled"`
	Mode              string       `json:"mode"`
	ID                string       `json:"id"`
	Name              string       `json:"name"`
	Token             string       `json:"token"`
	ExpectedMcVersion string       `json:"expectedMcVersion"`
	MaxSteps          int          `json:"maxSteps"`      // 0: continue until the model finishes or the task is cancelled.
	RunTimeoutSec     int          `json:"runTimeoutSec"` // 0: no task deadline; individual calls still have timeouts.
	WorldMappings     []BodyWorld  `json:"worldMappings"`
	Zones             BodyZones    `json:"zones"`
	DenyBlocks        []string     `json:"denyBlocks"`
	DenyItems         []string     `json:"denyItems"`
	HighRiskItems     []string     `json:"highRiskItems"`
	LoopGuardCommands []string     `json:"loopGuardCommands"`
	Limits            BodyLimits   `json:"limits"`
	Approval          BodyApproval `json:"approval"`
	Profile           BodyProfile  `json:"profile"`
}
type BodyWorld struct {
	Dimension string `json:"dimension"`
	WorldID   string `json:"worldId"`
}
type BodyArea struct {
	Name    string     `json:"name"`
	WorldID string     `json:"worldId"`
	Min     [3]float64 `json:"min"`
	Max     [3]float64 `json:"max"`
}
type BodyZones struct {
	Mode  string     `json:"mode"`
	Areas []BodyArea `json:"areas"`
}
type BodyLimits struct {
	ActionsPerMin   int `json:"actionsPerMin"`
	BlocksPerMin    int `json:"blocksPerMin"`
	ChatPerMin      int `json:"chatPerMin"`
	CommandsPerMin  int `json:"commandsPerMin"`
	ApprovalsPerMin int `json:"approvalsPerMin"`
}
type BodyApproval struct {
	Source         string `json:"source"`
	TimeoutSec     int    `json:"timeoutSec"`
	GrantTTLMS     int    `json:"grantTTLms"`
	ReasonMaxChars int    `json:"reasonMaxChars"`
}
type BodyProfile struct {
	Enabled       bool   `json:"enabled"`
	TokenHash     string `json:"tokenHash"`
	DisplayName   string `json:"displayName"`
	Skin          string `json:"skin"`
	HideDimension bool   `json:"hideDimension"`
}

func DefaultBody() Body {
	return Body{Mode: "off", ID: "main", Name: "MineAgentBot", ExpectedMcVersion: "26.2",
		Zones: BodyZones{Mode: "allowlist"}, Limits: BodyLimits{60, 30, 6, 3, 5}, Approval: BodyApproval{"portal", 120, 5000, 60}}
}
func (b Body) Validate(mainToken, listen string) error {
	if !b.Enabled {
		return nil
	}
	if b.Mode != "off" && b.Mode != "observe" && b.Mode != "assist" {
		return fmt.Errorf("body.mode 必须为 off/observe/assist")
	}
	if len(mainToken) < 32 {
		return fmt.Errorf("启用 body 要求 Paper 通道也配置至少 32 字符的独立密钥")
	}
	if len(b.Token) < 32 || b.Token == mainToken {
		return fmt.Errorf("body.token 必须是独立且至少 32 字符的密钥")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`).MatchString(b.ID) || !regexp.MustCompile(`^[A-Za-z0-9_]{3,16}$`).MatchString(b.Name) {
		return fmt.Errorf("body id/name 无效")
	}
	host, _, err := net.SplitHostPort(listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return fmt.Errorf("启用 body 要求后端 WS 仅监听回环地址")
	}
	if b.Zones.Mode != "allowlist" && b.Zones.Mode != "unrestricted" {
		return fmt.Errorf("body.zones.mode 必须为 allowlist/unrestricted")
	}
	for _, a := range b.Zones.Areas {
		for i := 0; i < 3; i++ {
			if a.Min[i] >= a.Max[i] {
				return fmt.Errorf("body zone 边界无效")
			}
		}
		if a.WorldID == "" {
			return fmt.Errorf("body zone 缺少 worldId")
		}
	}
	seen := map[string]bool{}
	for _, w := range b.WorldMappings {
		if w.WorldID == "" || w.Dimension == "" || seen[w.Dimension] {
			return fmt.Errorf("body worldMappings 必须有唯一维度映射")
		}
		seen[w.Dimension] = true
	}
	if b.MaxSteps < 0 || b.RunTimeoutSec < 0 || int64(b.RunTimeoutSec) > math.MaxInt64/int64(time.Second) {
		return fmt.Errorf("body run 预算无效")
	}
	if b.Approval.Source != "portal" || b.Approval.TimeoutSec < 1 || b.Approval.TimeoutSec > 120 || b.Approval.GrantTTLMS < 1 || b.Approval.GrantTTLMS > 5000 || b.Approval.ReasonMaxChars < 1 || b.Approval.ReasonMaxChars > 60 {
		return fmt.Errorf("body approval 配置无效（首期仅支持 portal）")
	}
	for _, n := range []int{b.Limits.ActionsPerMin, b.Limits.BlocksPerMin, b.Limits.ChatPerMin, b.Limits.CommandsPerMin, b.Limits.ApprovalsPerMin} {
		if n < 1 || n > 120 {
			return fmt.Errorf("body limits 必须在 1–120")
		}
	}
	if b.Profile.Enabled {
		for _, token := range []string{b.Token, mainToken} {
			h := sha256.Sum256([]byte(token))
			if hex.EncodeToString(h[:]) == b.Profile.TokenHash {
				return fmt.Errorf("body profile 不能复用控制/管理员凭据")
			}
		}
		if b.Profile.Skin != "" {
			u, e := url.Parse(b.Profile.Skin)
			if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
				return fmt.Errorf("body profile skin 必须为公开 HTTPS URL")
			}
		}
	}
	return nil
}
