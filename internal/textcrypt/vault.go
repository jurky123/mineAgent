// Package textcrypt stores per-batch daily keys. Only the current server day is
// available to clients; historical keys are retained exclusively for the operator.
package textcrypt

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	ErrDenied  = errors.New("程序未授权或已停用")
	ErrDeleted = errors.New("当天密钥已被管理员清除，今天无法继续处理消息")
	ErrMissing = errors.New("指定批次或密钥不存在")
)

var zone = time.FixedZone("CST", 8*60*60)

type Vault struct {
	dir string
	mu  sync.Mutex
}
type state struct {
	Groups map[string]*group `json:"groups"`
}
type group struct {
	Label     string            `json:"label"`
	TokenHash string            `json:"tokenHash"`
	Enabled   bool              `json:"enabled"`
	Keys      map[string][]byte `json:"keys"`
	Deleted   map[string]bool   `json:"deletedDays"`
}
type GroupInfo struct {
	ID      string   `json:"id"`
	Label   string   `json:"label"`
	Enabled bool     `json:"enabled"`
	Days    []string `json:"days"`
}
type Daily struct {
	Group      string `json:"group"`
	Day        string `json:"day"`
	ServerTime int64  `json:"serverTime"`
	ExpiresAt  int64  `json:"expiresAt"`
	Key        string `json:"key"`
}

func New(dir string) *Vault      { return &Vault{dir: dir} }
func randomHex(n int) string     { b := make([]byte, n); _, _ = rand.Read(b); return hex.EncodeToString(b) }
func digest(token string) string { h := sha256.Sum256([]byte(token)); return hex.EncodeToString(h[:]) }

func (v *Vault) load() (state, error) {
	s := state{Groups: map[string]*group{}}
	b, err := os.ReadFile(filepath.Join(v.dir, "vault.json"))
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err = json.Unmarshal(b, &s); err != nil || s.Groups == nil {
		return state{}, errors.New("invalid key vault")
	}
	for _, g := range s.Groups {
		if g == nil || g.Keys == nil || g.Deleted == nil {
			return state{}, errors.New("invalid key vault group")
		}
		for _, key := range g.Keys {
			if len(key) != 32 {
				return state{}, errors.New("invalid key material")
			}
		}
	}
	return s, nil
}

func (v *Vault) save(s state) error {
	if err := os.MkdirAll(v.dir, 0700); err != nil {
		return err
	}
	if err := os.Chmod(v.dir, 0700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	defer clear(b)
	f, err := os.CreateTemp(v.dir, ".vault-")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(b); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), filepath.Join(v.dir, "vault.json"))
}

func (v *Vault) Issue(label string) (string, string, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, err := v.load()
	if err != nil {
		return "", "", err
	}
	id, token := randomHex(8), randomHex(32)
	for s.Groups[id] != nil {
		id = randomHex(8)
	}
	label = strings.TrimSpace(label)
	if len([]rune(label)) > 80 {
		return "", "", errors.New("批次名称过长")
	}
	s.Groups[id] = &group{Label: label, TokenHash: digest(token), Enabled: true, Keys: map[string][]byte{}, Deleted: map[string]bool{}}
	if err = v.save(s); err != nil {
		return "", "", err
	}
	return id, token, nil
}

func (v *Vault) Current(id, token string, now time.Time) (Daily, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, err := v.load()
	if err != nil {
		return Daily{}, err
	}
	g := s.Groups[id]
	if g == nil || !g.Enabled || subtle.ConstantTimeCompare([]byte(g.TokenHash), []byte(digest(token))) != 1 {
		return Daily{}, ErrDenied
	}
	local := now.In(zone)
	day := local.Format("2006-01-02")
	if g.Deleted[day] {
		return Daily{}, ErrDeleted
	}
	key, ok := g.Keys[day]
	if !ok {
		key = make([]byte, 32)
		_, _ = rand.Read(key)
		g.Keys[day] = key
		if err = v.save(s); err != nil {
			return Daily{}, err
		}
	}
	end := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, zone)
	return Daily{Group: id, Day: day, ServerTime: now.UnixMilli(), ExpiresAt: end.UnixMilli(), Key: base64.StdEncoding.EncodeToString(key)}, nil
}

func (v *Vault) List() ([]GroupInfo, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, err := v.load()
	if err != nil {
		return nil, err
	}
	list := []GroupInfo{}
	for id, g := range s.Groups {
		days := []string{}
		for day := range g.Keys {
			days = append(days, day)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(days)))
		list = append(list, GroupInfo{ID: id, Label: g.Label, Enabled: g.Enabled, Days: days})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	return list, nil
}

func (v *Vault) Revoke(id string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	s, err := v.load()
	if err != nil {
		return err
	}
	g := s.Groups[id]
	if g == nil {
		return ErrMissing
	}
	g.Enabled = false
	return v.save(s)
}

func (v *Vault) Delete(id, day string) error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if _, err := time.Parse("2006-01-02", day); err != nil {
		return ErrMissing
	}
	s, err := v.load()
	if err != nil {
		return err
	}
	g := s.Groups[id]
	if g == nil {
		return ErrMissing
	}
	key, ok := g.Keys[day]
	if !ok {
		return ErrMissing
	}
	clear(key)
	delete(g.Keys, day)
	g.Deleted[day] = true
	return v.save(s)
}
