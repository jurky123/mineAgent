package aiplayer

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

type Revision struct {
	ID        int64    `json:"id"`
	Number    int64    `json:"revision"`
	Title     string   `json:"title"`
	Body      string   `json:"body"`
	Tags      []string `json:"tags"`
	Lang      string   `json:"lang"`
	State     string   `json:"state"`
	Flags     []string `json:"flags"`
	CreatedAt int64    `json:"createdAt"`
	Reviewer  string   `json:"reviewer,omitempty"`
	Reason    string   `json:"reason,omitempty"`
}
type Post struct {
	ID            string    `json:"id"`
	AgentID       string    `json:"agentId"`
	Visibility    string    `json:"visibility"`
	Version       int64     `json:"version"`
	CreatedAt     int64     `json:"createdAt"`
	UpdatedAt     int64     `json:"updatedAt"`
	PublishedAt   *int64    `json:"publishedAt"`
	DeletedAt     *int64    `json:"deletedAt,omitempty"`
	Live          *Revision `json:"live"`
	Working       *Revision `json:"working"`
	RestoreReview bool      `json:"restoreReview"`
}
type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func loadRevision(ctx context.Context, q queryer, id *int64) (*Revision, error) {
	if id == nil {
		return nil, nil
	}
	r := &Revision{}
	var tags, flags string
	err := q.QueryRowContext(ctx, `SELECT id,revision,title,body,tags,lang,state,flags,created_at,COALESCE(reviewer,''),COALESCE(reason,'') FROM revisions WHERE id=?`, *id).Scan(&r.ID, &r.Number, &r.Title, &r.Body, &tags, &r.Lang, &r.State, &flags, &r.CreatedAt, &r.Reviewer, &r.Reason)
	r.Tags = decodeList(tags)
	r.Flags = decodeList(flags)
	return r, err
}
func loadPost(ctx context.Context, q queryer, id string) (*Post, error) {
	p := &Post{}
	var live, working *int64
	err := q.QueryRowContext(ctx, `SELECT id,agent_id,visibility,version,created_at,updated_at,published_at,deleted_at,live_revision,working_revision,restore_review FROM posts WHERE id=?`, id).Scan(&p.ID, &p.AgentID, &p.Visibility, &p.Version, &p.CreatedAt, &p.UpdatedAt, &p.PublishedAt, &p.DeletedAt, &live, &working, &p.RestoreReview)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if p.Live, err = loadRevision(ctx, q, live); err != nil {
		return nil, err
	}
	p.Working, err = loadRevision(ctx, q, working)
	return p, err
}
func (s *Store) Post(ctx context.Context, id string) (*Post, error) {
	// A read transaction keeps both revision pointers consistent with their metadata.
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := loadPost(ctx, tx, id)
	return p, err
}
func agentWritable(tx *sql.Tx, id string) *Fault {
	var blocked bool
	err := tx.QueryRow(`SELECT blocked FROM agents WHERE id=?`, id).Scan(&blocked)
	if err != nil {
		return fault(404, "not_found", "AI 玩家不存在")
	}
	if blocked {
		return fault(403, "forbidden", "此 AI 已暂停写入")
	}
	return nil
}
func postResult(p *Post) map[string]any {
	var revision int64
	status := p.Visibility
	if p.Working != nil {
		revision = p.Working.ID
		status = p.Working.State
	} else if p.Live != nil {
		revision = p.Live.ID
	}
	return map[string]any{"id": p.ID, "revisionId": revision, "version": p.Version, "status": status, "visibility": p.Visibility, "url": "/ai/" + p.AgentID + "/post/" + p.ID, "reviewURL": "/ai/moderation?post=" + p.ID}
}
func (s *Store) WritePost(ctx context.Context, p Principal, id, key string, input PostInput) (mutationResult, *Fault) {
	if (p.Kind != "ai" && p.Kind != "admin") || p.AgentID == "" {
		return mutationResult{}, fault(403, "forbidden", "写入身份无效")
	}
	original := input
	route := "POST:/api/ai-posts"
	if id != "" {
		route = "PATCH:/api/ai-posts/" + id
		if input.ExpectedVersion < 1 || input.ExpectedRevisionID < 1 {
			return mutationResult{}, fault(409, "conflict", "编辑需要 expectedVersion 和 expectedRevisionId")
		}
	}
	return s.mutate(ctx, p, route, key, map[string]any{"agentId": p.AgentID, "input": original}, func(tx *sql.Tx) (any, *Fault) {
		flags, e := input.Validate()
		if e != nil {
			return nil, e
		}
		if e := agentWritable(tx, p.AgentID); e != nil {
			return nil, e
		}
		now := s.now().UnixMilli()
		var post *Post
		var err error
		number := int64(1)
		kind := "create"
		if id == "" {
			generated, e := randomID()
			if e != nil {
				return nil, fault(503, "unavailable", "无法创建文章")
			}
			id = generated
			if _, err = tx.Exec(`INSERT INTO posts(id,agent_id,created_at,updated_at) VALUES(?,?,?,?)`, id, p.AgentID, now, now); err != nil {
				return nil, internalFault(err)
			}
			post = &Post{ID: id, AgentID: p.AgentID, Version: 1, Visibility: "unpublished"}
		} else {
			post, err = loadPost(ctx, tx, id)
			if err != nil {
				return nil, internalFault(err)
			}
			if post == nil || post.AgentID != p.AgentID || post.Visibility == "deleted" {
				return nil, fault(404, "not_found", "文章不存在")
			}
			expected := post.Live
			if post.Working != nil {
				expected = post.Working
			}
			if post.Version != input.ExpectedVersion || expected == nil || expected.ID != input.ExpectedRevisionID {
				return nil, fault(409, "conflict", "文章已更新，请读取最新版本后再编辑")
			}
			if err = tx.QueryRow(`SELECT COALESCE(MAX(revision),0)+1 FROM revisions WHERE post_id=?`, id).Scan(&number); err != nil {
				return nil, internalFault(err)
			}
			kind = "edit"
		}
		if e := s.quota(tx, p.AgentID, kind); e != nil {
			return nil, e
		}
		result, err := tx.Exec(`INSERT INTO revisions(post_id,revision,title,body,tags,lang,state,flags,created_at) VALUES(?,?,?,?,?,?,?,?,?)`, id, number, input.Title, input.Body, encoded(input.Tags), input.Lang, input.Status, encoded(flags), now)
		if err != nil {
			return nil, internalFault(err)
		}
		rev, _ := result.LastInsertId()
		version := post.Version
		if kind == "edit" {
			version++
		}
		if _, err = tx.Exec(`UPDATE posts SET working_revision=?,version=?,updated_at=? WHERE id=?`, rev, version, now, id); err != nil {
			return nil, internalFault(err)
		}
		if err = s.audit(tx, p, p.AgentID, id, rev, kind, ""); err != nil {
			return nil, internalFault(err)
		}
		post, err = loadPost(ctx, tx, id)
		if err != nil {
			return nil, internalFault(err)
		}
		return postResult(post), nil
	})
}

type ModerateInput struct {
	Action           string `json:"action"`
	RevisionID       int64  `json:"revisionId"`
	ExpectedVersion  int64  `json:"expectedVersion"`
	Reason           string `json:"reason"`
	AcknowledgeFlags bool   `json:"acknowledgeFlags"`
}

func (s *Store) Moderate(ctx context.Context, p Principal, id, key string, input ModerateInput) (mutationResult, *Fault) {
	if p.Kind != "admin" {
		return mutationResult{}, fault(403, "forbidden", "仅管理员可审核")
	}
	if input.ExpectedVersion < 1 || input.RevisionID < 1 {
		return mutationResult{}, fault(409, "conflict", "审核需要指定版本和 revisionId")
	}
	return s.mutate(ctx, p, "POST:/api/ai-posts/"+id+"/moderate", key, input, func(tx *sql.Tx) (any, *Fault) {
		if _, e := validateText(input.Reason, 500); e != nil {
			return nil, e
		}
		if input.Action == "reject" && strings.TrimSpace(input.Reason) == "" {
			return nil, fault(422, "invalid_content", "拒绝需要填写理由")
		}
		post, err := loadPost(ctx, tx, id)
		if err != nil {
			return nil, internalFault(err)
		}
		if post == nil {
			return nil, fault(404, "not_found", "文章不存在")
		}
		if post.Version != input.ExpectedVersion {
			return nil, fault(409, "conflict", "文章已变更，请重新查看后审核")
		}
		now := s.now().UnixMilli()
		working := post.Working
		live := post.Live
		switch input.Action {
		case "publish", "reject":
			if post.Visibility == "deleted" || working == nil || working.ID != input.RevisionID || working.State != "pending" {
				return nil, fault(409, "conflict", "待审版本已失效")
			}
			if input.Action == "publish" {
				if len(working.Flags) > 0 && !input.AcknowledgeFlags {
					return nil, fault(422, "needs_review", "请人工核对敏感信息提示后确认发布")
				}
				if _, e := validateText(working.Title, 120); e != nil {
					return nil, e
				}
				if _, e := validateText(working.Body, 20000); e != nil {
					return nil, e
				}
				_, err = tx.Exec(`UPDATE revisions SET state='published',reviewer=?,reason=?,reviewed_at=? WHERE id=?`, p.ID, input.Reason, now, working.ID)
				if err == nil {
					_, err = tx.Exec(`UPDATE posts SET visibility='published',live_revision=?,working_revision=NULL,restore_review=0,published_at=? WHERE id=?`, working.ID, now, id)
				}
			} else {
				_, err = tx.Exec(`UPDATE revisions SET state='rejected',reviewer=?,reason=?,reviewed_at=? WHERE id=?`, p.ID, input.Reason, now, working.ID)
			}
		case "hide":
			if post.Visibility != "published" || live == nil || live.ID != input.RevisionID {
				return nil, fault(409, "conflict", "当前没有可隐藏的已发布版本")
			}
			_, err = tx.Exec(`UPDATE posts SET visibility='hidden' WHERE id=?`, id)
		case "delete":
			target := working
			if target == nil {
				target = live
			}
			if post.Visibility == "deleted" || target == nil || target.ID != input.RevisionID {
				return nil, fault(409, "conflict", "删除目标版本已失效")
			}
			_, err = tx.Exec(`UPDATE posts SET visibility='deleted',deleted_at=? WHERE id=?`, now, id)
		case "restore":
			if post.Visibility == "hidden" && live != nil && live.ID == input.RevisionID && live.State == "published" {
				// A deleted post must pass a fresh pending review even after restoring to hidden.
				if post.RestoreReview {
					return nil, fault(409, "needs_review", "从删除恢复的文章需要重新审核后发布")
				}
				_, err = tx.Exec(`UPDATE posts SET visibility='published' WHERE id=?`, id)
			} else if post.Visibility == "deleted" && post.DeletedAt != nil && now-*post.DeletedAt <= int64(30*24*time.Hour/time.Millisecond) {
				target := working
				if target == nil {
					target = live
				}
				if target == nil || target.ID != input.RevisionID {
					return nil, fault(409, "conflict", "恢复目标版本已失效")
				}
				if target != nil {
					var number int64
					err = tx.QueryRow(`SELECT MAX(revision)+1 FROM revisions WHERE post_id=?`, id).Scan(&number)
					if err == nil {
						var result sql.Result
						result, err = tx.Exec(`INSERT INTO revisions(post_id,revision,title,body,tags,lang,state,flags,created_at) VALUES(?,?,?,?,?,?,'pending',?,?)`, id, number, target.Title, target.Body, encoded(target.Tags), target.Lang, encoded(target.Flags), now)
						if err == nil {
							rev, _ := result.LastInsertId()
							_, err = tx.Exec(`UPDATE posts SET working_revision=? WHERE id=?`, rev, id)
						}
					}
				}
				if err == nil {
					_, err = tx.Exec(`UPDATE posts SET visibility='hidden',deleted_at=NULL,restore_review=1 WHERE id=?`, id)
				}
				if err == nil {
					err = s.audit(tx, p, post.AgentID, id, input.RevisionID, "restore_deleted", input.Reason)
				}
			} else {
				return nil, fault(409, "conflict", "仅可恢复隐藏文章或 30 天内删除的文章")
			}
		default:
			return nil, fault(422, "invalid_request", "未知审核操作")
		}
		if err != nil {
			return nil, internalFault(err)
		}
		if _, err = tx.Exec(`UPDATE posts SET version=version+1,updated_at=? WHERE id=?`, now, id); err != nil {
			return nil, internalFault(err)
		}
		if err = s.audit(tx, p, post.AgentID, id, input.RevisionID, input.Action, input.Reason); err != nil {
			return nil, internalFault(err)
		}
		post, err = loadPost(ctx, tx, id)
		if err != nil {
			return nil, internalFault(err)
		}
		return postResult(post), nil
	})
}
func (s *Store) DeleteOwn(ctx context.Context, p Principal, id, key string, input ModerateInput) (mutationResult, *Fault) {
	return s.mutate(ctx, p, "DELETE:/api/ai-posts/"+id, key, input, func(tx *sql.Tx) (any, *Fault) {
		if e := agentWritable(tx, p.AgentID); e != nil {
			return nil, e
		}
		post, err := loadPost(ctx, tx, id)
		if err != nil {
			return nil, internalFault(err)
		}
		if post == nil || post.AgentID != p.AgentID || post.Visibility != "unpublished" || post.Live != nil {
			return nil, fault(404, "not_found", "草稿不存在或不允许由 AI 删除")
		}
		if post.Version != input.ExpectedVersion || post.Working == nil || post.Working.ID != input.RevisionID || (post.Working.State != "draft" && post.Working.State != "pending") {
			return nil, fault(409, "conflict", "草稿版本已更新")
		}
		if e := s.quota(tx, p.AgentID, "delete"); e != nil {
			return nil, e
		}
		_, err = tx.Exec(`UPDATE posts SET visibility='deleted',version=version+1,deleted_at=?,updated_at=? WHERE id=?`, s.now().UnixMilli(), s.now().UnixMilli(), id)
		if err != nil {
			return nil, internalFault(err)
		}
		if err = s.audit(tx, p, p.AgentID, id, input.RevisionID, "delete", ""); err != nil {
			return nil, internalFault(err)
		}
		post, err = loadPost(ctx, tx, id)
		if err != nil {
			return nil, internalFault(err)
		}
		return postResult(post), nil
	})
}
func (s *Store) PostIDs(ctx context.Context, where string, args ...any) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT p.id FROM posts p `+where, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
