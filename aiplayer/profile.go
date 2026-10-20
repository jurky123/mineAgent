package aiplayer

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

type Snapshot struct {
	ProfileVersion int      `json:"profileVersion"`
	ID             string   `json:"id"`
	Name           string   `json:"name"`
	DisplayName    string   `json:"displayName"`
	Skin           *string  `json:"skin"`
	Status         string   `json:"status"`
	Activity       string   `json:"activity"`
	Dimension      *string  `json:"dimension"`
	OnlineSince    *int64   `json:"onlineSince"`
	UpdatedAt      int64    `json:"updatedAt"`
	FetchedAt      int64    `json:"fetchedAt"`
	Stale          bool     `json:"stale"`
	Capabilities   []string `json:"capabilities"`
}
type Profiles struct {
	mu       sync.RWMutex
	snapshot Snapshot
	failed   bool
	cfg      Config
	client   *http.Client
	now      func() time.Time
	log      *slog.Logger
}

func NewProfiles(c Config, log *slog.Logger) *Profiles {
	return &Profiles{cfg: c, now: time.Now, log: log, client: &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
}
func has(values []string, v string) bool {
	for _, s := range values {
		if s == v {
			return true
		}
	}
	return false
}
func (p *Profiles) project(s Snapshot) Snapshot {
	s.FetchedAt = p.now().UnixMilli()
	s.Capabilities = filterValues(s.Capabilities, []string{"observe", "move", "build", "chat", "command"})
	if !has([]string{"online", "busy", "offline", "unknown"}, s.Status) {
		s.Status = "unknown"
		s.Stale = true
	}
	if !has([]string{"idle", "moving", "digging", "placing", "interacting", "using_item", "chatting", "command", "unknown"}, s.Activity) {
		s.Activity = "unknown"
	}
	if s.Dimension != nil && !has([]string{"minecraft:overworld", "minecraft:the_nether", "minecraft:the_end"}, *s.Dimension) {
		s.Dimension = nil
	}
	if s.Skin != nil {
		u, e := url.Parse(*s.Skin)
		if e != nil || u.Scheme != "https" || u.User != nil || u.RawQuery != "" || !has(p.cfg.SkinHosts, u.Hostname()) || !publicHost(u.Hostname()) {
			s.Skin = nil
		}
	}
	if len([]rune(s.Name)) > 40 || secretPattern.MatchString(s.Name) {
		s.Name = ""
	}
	if len([]rune(s.DisplayName)) > 60 || secretPattern.MatchString(s.DisplayName) {
		s.DisplayName = ""
	}
	return s
}
func filterValues(values, allowed []string) []string {
	out := []string{}
	for _, s := range values {
		if has(allowed, s) && !has(out, s) {
			out = append(out, s)
		}
	}
	return out
}
func (p *Profiles) Get(id string) Snapshot {
	p.mu.RLock()
	s, failed := p.snapshot, p.failed
	p.mu.RUnlock()
	if s.ID != id {
		s = Snapshot{ProfileVersion: 1, ID: id, Capabilities: []string{}, Stale: true}
	}
	for _, a := range p.cfg.Agents {
		if a.ID == id {
			if s.Name == "" {
				s.Name = a.Name
			}
			if s.DisplayName == "" {
				s.DisplayName = a.DisplayName
			}
			break
		}
	}
	age := p.now().UnixMilli() - s.UpdatedAt
	if failed || s.Stale || s.UpdatedAt <= 0 || age > 10000 || age < -2000 || p.now().UnixMilli()-s.FetchedAt > 10000 {
		s.Stale = true
		s.Status = "unknown"
	}
	if s.Status == "" {
		s.Status = "unknown"
		s.Stale = true
	}
	if s.Status == "unknown" || s.Status == "offline" {
		s.OnlineSince = nil
		s.Activity = "unknown"
	}
	if s.Status == "unknown" {
		s.Stale = true
		s.Dimension = nil
	}
	return s
}

// poll is called only by the single background worker. Public requests never
// call upstream or get its token/errors, and redirects never carry credentials.
func (p *Profiles) poll(ctx context.Context) (delay time.Duration, pause bool) {
	delay = 5 * time.Second
	src := p.cfg.Profile
	if src == nil {
		return delay, true
	}
	p.mu.Lock()
	p.failed = true
	p.mu.Unlock()
	token, err := readPrivate(src.TokenFile)
	if err != nil {
		p.warn("profile_credentials_unavailable")
		return delay, true
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.URL, nil)
	if err != nil {
		return delay, true
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := p.client.Do(req)
	if err != nil {
		p.warn("profile_transport_failed")
		return 0, false
	}
	defer res.Body.Close()
	if res.StatusCode == 401 || res.StatusCode == 403 {
		p.warn("profile_auth_rejected_restart_after_rotation")
		return delay, true
	}
	if res.StatusCode != 200 {
		p.warn("profile_http_" + strconv.Itoa(res.StatusCode))
		if res.StatusCode == 429 {
			raw := res.Header.Get("Retry-After")
			if n, e := strconv.Atoi(raw); e == nil && n > 0 {
				return time.Duration(n) * time.Second, false
			}
			if t, e := http.ParseTime(raw); e == nil && t.After(p.now()) {
				return t.Sub(p.now()), false
			}
		}
		return 0, false
	}
	var s Snapshot
	if err = json.NewDecoder(io.LimitReader(res.Body, 32768)).Decode(&s); err != nil || s.ProfileVersion != 1 || s.ID != src.AgentID {
		p.warn("profile_invalid_contract")
		return 0, false
	}
	s = p.project(s)
	p.mu.Lock()
	p.snapshot = s
	p.failed = false
	p.mu.Unlock()
	return delay, false
}
func (p *Profiles) warn(event string) {
	if p.log != nil {
		p.log.Warn(event)
	}
}
func (p *Profiles) Run(ctx context.Context) {
	backoff := 5 * time.Second
	for {
		delay, pause := p.poll(ctx)
		if pause {
			return
		}
		if delay == 0 {
			delay = backoff
			backoff *= 2
			if backoff > 60*time.Second {
				backoff = 60 * time.Second
			}
		} else {
			p.mu.RLock()
			failed := p.failed
			p.mu.RUnlock()
			if !failed {
				backoff = 5 * time.Second
			}
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

type AgentRecord struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	DisplayName string           `json:"displayName"`
	Bio         string           `json:"bio"`
	Tags        []string         `json:"tags"`
	CreatedAt   int64            `json:"createdAt"`
	Version     int64            `json:"version,omitempty"`
	Blocked     bool             `json:"blocked,omitempty"`
	Trust       string           `json:"trustLevel,omitempty"`
	Working     *ProfileRevision `json:"working,omitempty"`
}
type ProfileRevision struct {
	ID     int64    `json:"id"`
	Bio    string   `json:"bio"`
	Tags   []string `json:"tags"`
	State  string   `json:"state"`
	Flags  []string `json:"flags"`
	Reason string   `json:"reason"`
}

func (s *Store) Agents(ctx context.Context, private bool) ([]AgentRecord, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id,name,display_name,bio,tags,created_at,version,blocked,trust_level FROM agents ORDER BY name`)
	if err != nil {
		return nil, err
	}
	out := []AgentRecord{}
	for rows.Next() {
		var a AgentRecord
		var tags string
		if err = rows.Scan(&a.ID, &a.Name, &a.DisplayName, &a.Bio, &tags, &a.CreatedAt, &a.Version, &a.Blocked, &a.Trust); err != nil {
			rows.Close()
			return nil, err
		}
		a.Tags = decodeList(tags)
		out = append(out, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if private {
		for i := range out {
			var r ProfileRevision
			var tags, flags string
			err = s.db.QueryRowContext(ctx, `SELECT r.id,r.bio,r.tags,r.state,r.flags,COALESCE(r.reason,'') FROM profile_revisions r JOIN agents a ON a.profile_working=r.id WHERE a.id=?`, out[i].ID).Scan(&r.ID, &r.Bio, &tags, &r.State, &flags, &r.Reason)
			if err == nil {
				r.Tags = decodeList(tags)
				r.Flags = decodeList(flags)
				out[i].Working = &r
			}
		}
	}
	return out, nil
}

// Explicit, versioned profile proposals use the same approval rules as posts.
type ProfileInput struct {
	Bio             string   `json:"bio"`
	Tags            []string `json:"tags"`
	ExpectedVersion int64    `json:"expectedVersion"`
}
type AgentAdminInput struct {
	ExpectedVersion  int64  `json:"expectedVersion"`
	Blocked          *bool  `json:"blocked,omitempty"`
	Level            string `json:"level,omitempty"`
	Action           string `json:"action,omitempty"`
	RevisionID       int64  `json:"revisionId,omitempty"`
	Reason           string `json:"reason"`
	AcknowledgeFlags bool   `json:"acknowledgeFlags,omitempty"`
}

func (s *Store) ProposeProfile(ctx context.Context, p Principal, key string, in ProfileInput) (mutationResult, *Fault) {
	if (p.Kind != "ai" && p.Kind != "admin") || p.AgentID == "" {
		return mutationResult{}, fault(403, "forbidden", "写入身份无效")
	}
	return s.mutate(ctx, p, "PATCH:/api/ai-agents/"+p.AgentID+"/profile", key, in, func(tx *sql.Tx) (any, *Fault) {
		flags, e := validateText(in.Bio, 2000)
		if e != nil {
			return nil, e
		}
		in.Tags, e = normalizeTags(in.Tags)
		if e != nil {
			return nil, e
		}
		if e := agentWritable(tx, p.AgentID); e != nil {
			return nil, e
		}
		var version int64
		if err := tx.QueryRow(`SELECT version FROM agents WHERE id=?`, p.AgentID).Scan(&version); err != nil {
			return nil, internalFault(err)
		}
		if version != in.ExpectedVersion {
			return nil, fault(409, "conflict", "资料已更新，请重新读取")
		}
		if e := s.quota(tx, p.AgentID, "profile"); e != nil {
			return nil, e
		}
		res, err := tx.Exec(`INSERT INTO profile_revisions(agent_id,bio,tags,state,flags,created_at) VALUES(?,?,?,'pending',?,?)`, p.AgentID, in.Bio, encoded(in.Tags), encoded(flags), s.now().UnixMilli())
		if err != nil {
			return nil, internalFault(err)
		}
		id, _ := res.LastInsertId()
		_, err = tx.Exec(`UPDATE agents SET version=version+1,profile_working=? WHERE id=?`, id, p.AgentID)
		if err != nil {
			return nil, internalFault(err)
		}
		if err = s.audit(tx, p, p.AgentID, "", id, "profile_proposed", ""); err != nil {
			return nil, internalFault(err)
		}
		return map[string]any{"id": p.AgentID, "revisionId": id, "version": version + 1, "status": "pending"}, nil
	})
}
func (s *Store) ManageAgent(ctx context.Context, p Principal, id, kind, key string, in AgentAdminInput) (mutationResult, *Fault) {
	if p.Kind != "admin" {
		return mutationResult{}, fault(403, "forbidden", "仅管理员可管理 AI")
	}
	return s.mutate(ctx, p, "POST:/api/ai-agents/"+id+"/"+kind, key, in, func(tx *sql.Tx) (any, *Fault) {
		if _, e := validateText(in.Reason, 500); e != nil {
			return nil, e
		}
		var version int64
		var working *int64
		err := tx.QueryRow(`SELECT version,profile_working FROM agents WHERE id=?`, id).Scan(&version, &working)
		if err != nil {
			return nil, fault(404, "not_found", "AI 玩家不存在")
		}
		if in.ExpectedVersion != version {
			return nil, fault(409, "conflict", "资料版本已变化")
		}
		switch kind {
		case "block":
			if in.Blocked == nil {
				return nil, fault(422, "invalid_request", "缺少 blocked")
			}
			_, err = tx.Exec(`UPDATE agents SET blocked=? WHERE id=?`, *in.Blocked, id)
		case "trust":
			if !has([]string{"review", "trusted"}, in.Level) {
				return nil, fault(422, "invalid_request", "未知信任标记")
			}
			_, err = tx.Exec(`UPDATE agents SET trust_level=? WHERE id=?`, in.Level, id)
		case "moderate-profile":
			if working == nil || *working != in.RevisionID || !has([]string{"publish", "reject"}, in.Action) {
				return nil, fault(409, "conflict", "待审资料版本已失效")
			}
			var bio, tags, state, flags string
			err = tx.QueryRow(`SELECT bio,tags,state,flags FROM profile_revisions WHERE id=?`, *working).Scan(&bio, &tags, &state, &flags)
			if err != nil {
				return nil, internalFault(err)
			}
			if state != "pending" {
				return nil, fault(409, "conflict", "资料不在待审状态")
			}
			if in.Action == "publish" {
				if len(decodeList(flags)) > 0 && !in.AcknowledgeFlags {
					return nil, fault(422, "needs_review", "请核对敏感信息提示")
				}
				if _, e := validateText(bio, 2000); e != nil {
					return nil, e
				}
				_, err = tx.Exec(`UPDATE agents SET bio=?,tags=?,profile_working=NULL WHERE id=?`, bio, tags, id)
			} else if in.Reason == "" {
				return nil, fault(422, "invalid_content", "拒绝资料需要理由")
			}
			if err == nil {
				_, err = tx.Exec(`UPDATE profile_revisions SET state=?,reviewer=?,reason=? WHERE id=?`, map[bool]string{true: "published", false: "rejected"}[in.Action == "publish"], p.ID, in.Reason, *working)
			}
		default:
			return nil, fault(422, "invalid_request", "未知资料管理操作")
		}
		if err != nil {
			return nil, internalFault(err)
		}
		if _, err = tx.Exec(`UPDATE agents SET version=version+1 WHERE id=?`, id); err != nil {
			return nil, internalFault(err)
		}
		if err = s.audit(tx, p, id, "", in.RevisionID, kind+":"+in.Action, in.Reason); err != nil {
			return nil, internalFault(err)
		}
		return map[string]any{"id": id, "version": version + 1}, nil
	})
}
