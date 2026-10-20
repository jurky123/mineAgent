// Package aiplayer implements an independent AI-player publishing service.
// It never imports MineAgent's storage, identity, body control or agent loop.
package aiplayer

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

type Config struct {
	Listen       string         `json:"listen"`
	Database     string         `json:"database"`
	Origin       string         `json:"origin"`
	PortalURL    string         `json:"portalURL"`
	Name         string         `json:"name"`
	Agents       []AgentConfig  `json:"agents"`
	Profile      *ProfileSource `json:"profile,omitempty"`
	SkinHosts    []string       `json:"skinHosts"`
	ForumEnabled bool           `json:"forumEnabled,omitempty"`
}
type AgentConfig struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
}
type ProfileSource struct {
	AgentID   string `json:"agentId"`
	URL       string `json:"url"`
	TokenFile string `json:"tokenFile"`
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,40}$`)

func LoadConfig(path string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8890", Database: "data/aiplayer/portal.db", Name: "AI 玩家", Agents: []AgentConfig{{"main", "MineAgentBot", "MineAgentBot"}}}
	data, err := os.ReadFile(path)
	if err != nil {
		return c, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(data)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&c); err != nil {
		return c, errors.New("invalid AI portal configuration")
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return c, errors.New("configuration must contain one JSON object")
	}
	base, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	if !filepath.IsAbs(c.Database) {
		c.Database = filepath.Join(base, c.Database)
	}
	if c.Profile != nil && !filepath.IsAbs(c.Profile.TokenFile) {
		c.Profile.TokenFile = filepath.Join(base, c.Profile.TokenFile)
	}
	return c, c.Validate()
}
func (c Config) Validate() error {
	host, _, err := net.SplitHostPort(c.Listen)
	if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
		return errors.New("listen must be an explicit loopback address")
	}
	if c.Database == "" || c.Origin == "" || !safeServiceURL(c.Origin) {
		return errors.New("database and canonical HTTPS/loopback origin are required")
	}
	u, _ := url.Parse(c.Origin)
	if u.Path != "" && u.Path != "/" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("origin must contain only scheme and authority")
	}
	if strings.TrimRight(c.Origin, "/") != c.Origin {
		return errors.New("origin must not end in a slash")
	}
	if c.PortalURL != "" && !safeServiceURL(c.PortalURL) {
		return errors.New("invalid public portal URL")
	}
	if len(c.Agents) == 0 {
		return errors.New("at least one agent is required")
	}
	seen := map[string]bool{}
	for _, a := range c.Agents {
		if !identifier.MatchString(a.ID) || seen[a.ID] || len([]rune(a.Name)) < 1 || len([]rune(a.Name)) > 40 || len([]rune(a.DisplayName)) > 60 {
			return errors.New("invalid agent identity")
		}
		seen[a.ID] = true
	}
	if c.Profile != nil {
		p := c.Profile
		u, e := url.Parse(p.URL)
		if e != nil || !seen[p.AgentID] || !safeServiceURL(p.URL) || u.Path != "/api/body/profile" || u.RawQuery != "" || u.Fragment != "" || p.TokenFile == "" {
			return errors.New("profile requires a configured agent, pinned /api/body/profile URL and private token file")
		}
	}
	for _, host := range c.SkinHosts {
		if strings.ContainsAny(host, "/:* @") || !publicHost(host) {
			return fmt.Errorf("invalid skin allowlist host")
		}
	}
	return nil
}
func safeServiceURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return u.Scheme == "http" && (u.Hostname() == "localhost" || (ip != nil && ip.IsLoopback()))
}
func readPrivate(path string) (string, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", errors.New("private credential file unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > 16384 {
		return "", errors.New("credential file must be regular, owner-only and small")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("private credential file unreadable")
	}
	value := strings.TrimSpace(string(data))
	if value == "" {
		return "", errors.New("credential file is empty")
	}
	return value, nil
}
