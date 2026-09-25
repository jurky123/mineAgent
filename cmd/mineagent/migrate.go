package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"mineagent/internal/account"
	"mineagent/internal/config"
	"mineagent/internal/storage"
)

// runPortalMigration 把旧版"名字会话"迁到 Portal 的 user_id 体系（幂等、先备份）：
//
//	web:c2c:<名字>[:<conv>]  ->  web:user:<ID>[:<conv>]   （messages/summaries/
//	                            reminders/tool_audit/sessions 一起改）
//	web_conversations.account -> web_conversations.user_id
//	workspace/web-files/<名字>/ -> workspace/web-files/u<ID>/
//
// users 表的数据来源：账号系统已导入的 token 用户 ∪ 会话表出现过的名字 ∪
// 上传目录名。重复执行是安全的：改过的键不会再匹配 web:c2c:。
func runPortalMigration(ctx context.Context, log *slog.Logger, cfg config.Config, store *storage.Store) error {
	workspace := cfg.WorkspaceRoot()

	// 1) 收集需要迁移的名字（只挑还有旧数据的，保证重复执行是纯 no-op）。
	names := map[string]string{} // name -> 来源（日志用）
	if ids, err := store.WebSessionIDs(ctx); err == nil {
		for _, id := range ids {
			if name := oldWebSessionName(id); name != "" {
				names[name] = "messages"
			}
		}
	} else {
		return err
	}
	if accounts, err := store.UnboundConversationAccounts(ctx); err == nil {
		for _, a := range accounts {
			names[a] = "conversations"
		}
	} else {
		return err
	}
	if entries, err := os.ReadDir(filepath.Join(workspace, "web-files")); err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() && !isUserFileDir(n) {
				names[n] = "web-files"
			}
		}
	}
	if len(names) == 0 {
		log.Info("没有需要迁移的旧数据（可能已经迁过）")
		return nil
	}

	// 2) 备份数据库（VACUUM INTO 一致性副本）后开始迁移。
	ts := time.Now().Format("20060102-150405")
	backupDir := filepath.Join(os.TempDir(), "mineagent-portal-backup-"+ts)
	if err := os.MkdirAll(backupDir, 0o700); err != nil {
		return err
	}
	dbBackup := filepath.Join(backupDir, filepath.Base(cfg.Storage.Path))
	if err := store.BackupTo(ctx, dbBackup); err != nil {
		return fmt.Errorf("备份数据库失败: %w", err)
	}
	log.Info("数据库已备份", "path", dbBackup)
	sorted := make([]string, 0, len(names))
	for n := range names {
		sorted = append(sorted, n)
	}
	sort.Strings(sorted)
	log.Info("开始迁移", "账号数", len(sorted), "账号", strings.Join(sorted, ", "))

	adminNames := map[string]bool{}
	for _, a := range cfg.Web.AdminUsers {
		adminNames[a] = true
	}

	report := map[string]any{"backup": dbBackup, "accounts": []any{}}
	for _, name := range sorted {
		if !account.ValidName(name) {
			log.Warn("跳过非法名字", "name", name)
			continue
		}
		u, err := store.EnsureUser(ctx, name, time.Now().UnixMilli())
		if err != nil {
			return fmt.Errorf("建用户 %s: %w", name, err)
		}
		// 管理员镜像（config.web.adminUsers 是唯一权威；登录时也会同步）。
		if err := store.SetUserAdmin(ctx, name, adminNames[name]); err != nil {
			return fmt.Errorf("设置管理员 %s: %w", name, err)
		}
		conv, err := store.BindConversationsToUser(ctx, name, u.ID)
		if err != nil {
			return fmt.Errorf("绑定会话 %s: %w", name, err)
		}

		// 会话键改写：先收集该名字的旧键，再逐个改。
		oldIDs := []string{}
		if ids, err := store.WebSessionIDs(ctx); err == nil {
			for _, id := range ids {
				if oldWebSessionName(id) == name {
					oldIDs = append(oldIDs, id)
				}
			}
		}
		moved := 0
		for _, old := range oldIDs {
			convID := oldWebSessionConv(old)
			if err := store.RenameSessionID(ctx, old, webUserSessionKey(u.ID, convID)); err != nil {
				return fmt.Errorf("改会话键 %s: %w", old, err)
			}
			moved++
		}

		// 文件目录改名（已存在 u<ID> 时合并进去）。
		filesMoved, err := moveUserFiles(workspace, name, u.ID)
		if err != nil {
			return err
		}
		log.Info("账号迁移完成", "name", name, "uid", u.ID, "conversations", conv,
			"sessions", moved, "files", filesMoved)
		report["accounts"] = append(report["accounts"].([]any), map[string]any{
			"name": name, "uid": u.ID, "conversations": conv, "sessions": moved, "files": filesMoved,
		})
	}

	report["at"] = time.Now().Format(time.RFC3339)
	if err := store.SetMeta(ctx, "migrated_from_c2c_at", report["at"].(string)); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(report, "", "  ")
	fmt.Printf("迁移完成，报告：\n%s\n备份目录：%s\n", b, backupDir)
	return nil
}

// oldWebSessionName 解析 web:c2c:<name>[:<conv>]；不是旧格式返回 ""。
func oldWebSessionName(sessionID string) string {
	rest, ok := strings.CutPrefix(sessionID, "web:c2c:")
	if !ok || rest == "" {
		return ""
	}
	if i := strings.Index(rest, ":"); i >= 0 {
		return rest[:i]
	}
	return rest
}

func oldWebSessionConv(sessionID string) string {
	rest, _ := strings.CutPrefix(sessionID, "web:c2c:")
	if i := strings.Index(rest, ":"); i >= 0 {
		return rest[i+1:]
	}
	return ""
}

func webUserSessionKey(userID int64, conv string) string {
	base := "web:user:" + fmt.Sprint(userID)
	if conv == "" {
		return base
	}
	return base + ":" + conv
}

func isUserFileDir(name string) bool {
	if !strings.HasPrefix(name, "u") || len(name) < 2 {
		return false
	}
	for _, r := range name[1:] {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// moveUserFiles 把 web-files/<名字>/ 改名为 web-files/u<ID>/（目标已存在则合并）。
func moveUserFiles(workspace, name string, userID int64) (int, error) {
	src := filepath.Join(workspace, "web-files", name)
	if fi, err := os.Stat(src); err != nil || !fi.IsDir() {
		return 0, nil
	}
	dst := filepath.Join(workspace, "web-files", storage.UserFileDir(userID))
	if _, err := os.Stat(dst); os.IsNotExist(err) {
		if err := os.Rename(src, dst); err != nil {
			return 0, fmt.Errorf("移动上传目录 %s: %w", src, err)
		}
		return 1, nil
	}
	// 合并：逐个文件搬到 dst（同名冲突加时间戳前缀）。
	entries, err := os.ReadDir(src)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, e := range entries {
		from := filepath.Join(src, e.Name())
		to := filepath.Join(dst, e.Name())
		if _, err := os.Stat(to); err == nil {
			to = filepath.Join(dst, fmt.Sprintf("%d_%s", time.Now().UnixMilli(), e.Name()))
		}
		if err := os.Rename(from, to); err != nil {
			return n, err
		}
		n++
	}
	_ = os.Remove(src)
	return n, nil
}
