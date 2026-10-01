package games

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"mineagent/internal/storage"
)

// startGamesAPI 起一个带测试鉴权的 API：用 X-Test-User 头决定当前用户，
// 避免依赖 account 服务；解说器固定返回，便于并发下反复写 Comments。
func startGamesAPI(t *testing.T) (*Manager, *httptest.Server) {
	t.Helper()
	m := testManager(t)
	m.SetCommentator(func(context.Context, string) (string, error) { return "解说内容", nil })
	auth := func(h func(w http.ResponseWriter, r *http.Request, u *storage.User)) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			id, name := int64(1), "a"
			if r.Header.Get("X-Test-User") == "2" {
				id, name = 2, "b"
			}
			h(w, r, &storage.User{ID: id, Username: name})
		}
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(NewAPI(log, m, nil, auth).Handler())
	t.Cleanup(srv.Close)
	return m, srv
}

// apiDo 发一个请求；错误和非 2xx 都如实返回，调用方自行决定是否忽略（并发探针不 assert 业务结果）。
func apiDo(srv *httptest.Server, method, path, user string, body any) (int, []byte, error) {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, srv.URL+path, reader)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("X-Test-User", user)
	resp, err := srv.Client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, data, nil
}

// TestGamesAPIConcurrency 并发跑 HTTP 读/走棋/换边/悔棋/离开/解说，
// 由 -race 捕获响应快照与内部状态的共享读写。
func TestGamesAPIConcurrency(t *testing.T) {
	_, srv := startGamesAPI(t)

	code, data, err := apiDo(srv, http.MethodPost, "/api/games/gomoku/rooms", "1", nil)
	if err != nil || code != http.StatusOK {
		t.Fatalf("建房失败: code=%d err=%v body=%s", code, err, data)
	}
	var created struct {
		Room map[string]any `json:"room"`
	}
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	roomID, _ := created.Room["id"].(string)
	if roomID == "" {
		t.Fatalf("建房响应缺房间号: %s", data)
	}
	if code, data, err = apiDo(srv, http.MethodPost, "/api/games/gomoku/rooms/join", "2", map[string]any{"room": roomID}); err != nil || code != http.StatusOK {
		t.Fatalf("加入失败: code=%d err=%v body=%s", code, err, data)
	}

	// 先确定性地走一步并生成一次解说：保证并发探针不是空跑（comment 需要已有着法）。
	if code, data, err = apiDo(srv, http.MethodPost, "/api/games/gomoku/move", "1", map[string]any{"point": "h8"}); err != nil || code != http.StatusOK {
		t.Fatalf("起始落子失败: code=%d err=%v body=%s", code, err, data)
	}
	if code, data, err = apiDo(srv, http.MethodPost, "/api/games/gomoku/comment", "1", nil); err != nil || code != http.StatusOK {
		t.Fatalf("起始解说失败: code=%d err=%v body=%s", code, err, data)
	}
	if !bytes.Contains(data, []byte("解说内容")) {
		t.Fatalf("解说响应不含解说内容: %s", data)
	}

	var wg sync.WaitGroup
	worker := func(fn func(i int)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 150; i++ {
				fn(i)
			}
		}()
	}

	// 单个走子线程严格交替黑白并使用不重复坐标，保证持续有真实写操作。
	worker(func(int) {
		for i := 0; i < 60; i++ {
			user := "1"
			if i%2 == 1 {
				user = "2"
			}
			pt := fmt.Sprintf("%c%d", 'a'+i%15, i/15+1)
			_, _, _ = apiDo(srv, http.MethodPost, "/api/games/gomoku/move", user, map[string]any{"point": pt})
		}
	})
	worker(func(int) {
		_, _, _ = apiDo(srv, http.MethodGet, "/api/games/gomoku/room", "1", nil)
		_, _, _ = apiDo(srv, http.MethodGet, "/api/games/gomoku/room", "2", nil)
	})
	worker(func(int) {
		_, _, _ = apiDo(srv, http.MethodGet, "/api/games/gomoku/rooms", "1", nil)
	})
	worker(func(int) {
		_, _, _ = apiDo(srv, http.MethodPost, "/api/games/gomoku/comment", "1", nil)
	})
	worker(func(int) {
		_, _, _ = apiDo(srv, http.MethodPost, "/api/games/gomoku/swap", "1", map[string]any{"action": "request"})
		_, _, _ = apiDo(srv, http.MethodPost, "/api/games/gomoku/swap", "2", map[string]any{"action": "decline"})
	})
	worker(func(int) {
		_, _, _ = apiDo(srv, http.MethodPost, "/api/games/gomoku/undo", "1", map[string]any{"action": "request"})
		_, _, _ = apiDo(srv, http.MethodPost, "/api/games/gomoku/undo", "2", map[string]any{"action": "accept"})
	})
	wg.Wait()

	// 离开也要在并发快照之外单独覆盖一次
	if _, _, err := apiDo(srv, http.MethodPost, "/api/games/gomoku/leave", "2", nil); err != nil {
		t.Fatalf("离开失败: %v", err)
	}
}
