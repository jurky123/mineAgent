package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Listen       string    `json:"listen"`
	Token        string    `json:"token"`
	LogLevel     string    `json:"logLevel"`
	AllowedRoles []string  `json:"allowedRoles"`
	Minecraft    Minecraft `json:"minecraft"`
	Model        Model     `json:"model"`
	Storage      Storage   `json:"storage"`
	Tools        Tools     `json:"tools"`
}

type Minecraft struct {
	Trigger   string `json:"trigger"`
	SessionID string `json:"sessionId"`
	ReplyMode string `json:"replyMode"`
}

type Model struct {
	BaseURL string `json:"baseURL"`
	APIKey  string `json:"apiKey"`
	Name    string `json:"name"`
}

type Storage struct {
	Path string `json:"path"`
}

type Tools struct {
	ApprovalTimeoutSeconds int `json:"approvalTimeoutSeconds"`
}

func DefaultTools() Tools {
	return Tools{
		ApprovalTimeoutSeconds: 180,
	}
}

func Default() Config {
	return Config{
		Listen:       "127.0.0.1:8765",
		LogLevel:     "info",
		AllowedRoles: []string{"minecraft"},
		Minecraft:    Minecraft{Trigger: "@agent", SessionID: "minecraft-main", ReplyMode: "broadcast"},
		Storage:      Storage{Path: "data/mineagent.db"},
		Tools:        DefaultTools(),
	}
}

func Load(path string) (Config, error) {
	cfg := Default()
	if path != "" {
		b, err := os.ReadFile(path)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &cfg); err != nil {
				return cfg, fmt.Errorf("parse %s: %w", path, err)
			}
		case !os.IsNotExist(err):
			return cfg, err
		}
	}
	applyEnv(&cfg)
	if cfg.Listen == "" {
		cfg.Listen = Default().Listen
	}
	return cfg, nil
}

func (c Config) SlogLevel() slog.Level {
	switch strings.ToLower(c.LogLevel) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

func (c Config) Redacted() Config {
	if c.Token != "" {
		c.Token = "<set>"
	}
	if c.Model.APIKey != "" {
		c.Model.APIKey = "<set>"
	}
	return c
}

func applyEnv(cfg *Config) {
	set := func(dst *string, key string) {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			*dst = v
		}
	}
	set(&cfg.Listen, "MINEAGENT_LISTEN")
	set(&cfg.Token, "MINEAGENT_TOKEN")
	set(&cfg.Model.APIKey, "MINEAGENT_MODEL_API_KEY")
	set(&cfg.Model.BaseURL, "MINEAGENT_MODEL_BASE_URL")
	set(&cfg.Model.Name, "MINEAGENT_MODEL_NAME")
	set(&cfg.Minecraft.Trigger, "MINEAGENT_TRIGGER")
}
