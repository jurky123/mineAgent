package session

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"

	"mineagent/internal/storage"
)

type fakeChannel struct {
	name string

	mu   sync.Mutex
	msgs []storage.Message
}

func (f *fakeChannel) Name() string { return f.name }

func (f *fakeChannel) Send(_ context.Context, msg storage.Message) error {
	f.mu.Lock()
	f.msgs = append(f.msgs, msg)
	f.mu.Unlock()
	return nil
}

func (f *fakeChannel) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.msgs)
}

func newTestHub(t *testing.T) (*Hub, *Session) {
	t.Helper()
	st, err := storage.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	hub := NewHub(st, slog.New(slog.NewTextHandler(io.Discard, nil)))
	sess, err := hub.Session(context.Background(), "main")
	if err != nil {
		t.Fatal(err)
	}
	return hub, sess
}

func TestIngestExcludesSourceChannel(t *testing.T) {
	_, sess := newTestHub(t)
	mc := &fakeChannel{name: "minecraft"}
	qq := &fakeChannel{name: "qq"}
	sess.Register(mc)
	sess.Register(qq)

	msg, err := sess.Ingest(context.Background(), storage.Message{
		Channel:    "minecraft",
		AuthorKind: "player",
		AuthorName: "Steve",
		Text:       "hello",
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.ID == 0 {
		t.Fatal("expected persisted message id")
	}
	if mc.count() != 0 {
		t.Fatal("source channel should not receive its own message")
	}
	if qq.count() != 1 {
		t.Fatalf("qq count = %d, want 1", qq.count())
	}
}

func TestReplyFansOutToAllChannels(t *testing.T) {
	_, sess := newTestHub(t)
	mc := &fakeChannel{name: "minecraft"}
	qq := &fakeChannel{name: "qq"}
	sess.Register(mc)
	sess.Register(qq)

	if err := sess.Reply(context.Background(), "hi", "Steve"); err != nil {
		t.Fatal(err)
	}
	if mc.count() != 1 || qq.count() != 1 {
		t.Fatalf("counts = %d, %d", mc.count(), qq.count())
	}
	mc.mu.Lock()
	got := mc.msgs[0]
	mc.mu.Unlock()
	if got.AuthorKind != "agent" || got.Text != "hi" || got.Target != "Steve" {
		t.Fatalf("got %+v", got)
	}
}

func TestUnregister(t *testing.T) {
	_, sess := newTestHub(t)
	ch := &fakeChannel{name: "qq"}
	sess.Register(ch)
	sess.Unregister("qq")
	if err := sess.Reply(context.Background(), "hi", ""); err != nil {
		t.Fatal(err)
	}
	if ch.count() != 0 {
		t.Fatal("unregistered channel received message")
	}
}

func TestReplyReturnsErrorWhenAllChannelsFail(t *testing.T) {
	_, sess := newTestHub(t)
	bad := &failChannel{name: "qq"}
	sess.Register(bad)
	// 单通道失败必须返回 error（图片场景靠它让 agent 知道没发出去）。
	if err := sess.Reply(context.Background(), "img", "img:c2c:U:MSG:p.png"); err == nil {
		t.Fatal("want error when the only channel fails")
	}
}

func TestReplySucceedsWhenOneChannelOk(t *testing.T) {
	_, sess := newTestHub(t)
	bad := &failChannel{name: "bad"}
	ok := &fakeChannel{name: "good"}
	sess.Register(bad)
	sess.Register(ok)
	// MC 广播语义：部分通道失败不算错（玩家离线等）。
	if err := sess.Reply(context.Background(), "hi", ""); err != nil {
		t.Fatalf("want nil when one channel ok, got %v", err)
	}
	if ok.count() != 1 {
		t.Fatal("good channel should receive")
	}
}

type failChannel struct {
	name string
}

func (f *failChannel) Name() string { return f.name }

func (f *failChannel) Send(_ context.Context, _ storage.Message) error {
	return errTestFail
}

var errTestFail = errTest()

func errTest() error { return context.DeadlineExceeded }
