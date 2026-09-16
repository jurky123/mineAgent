package session

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"mineagent/internal/storage"
)

type Channel interface {
	Name() string
	Send(ctx context.Context, msg storage.Message) error
}

type Hub struct {
	store *storage.Store
	log   *slog.Logger

	mu       sync.Mutex
	sessions map[string]*Session
}

func NewHub(store *storage.Store, log *slog.Logger) *Hub {
	return &Hub{
		store:    store,
		log:      log,
		sessions: make(map[string]*Session),
	}
}

func (h *Hub) Session(ctx context.Context, id string) (*Session, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if s, ok := h.sessions[id]; ok {
		return s, nil
	}
	if err := h.store.EnsureSession(ctx, id, time.Now().UnixMilli()); err != nil {
		return nil, err
	}
	s := &Session{ID: id, hub: h, channels: make(map[string]Channel)}
	h.sessions[id] = s
	return s, nil
}

type Session struct {
	ID  string
	hub *Hub

	mu       sync.RWMutex
	channels map[string]Channel
}

func (s *Session) Register(ch Channel) {
	s.mu.Lock()
	s.channels[ch.Name()] = ch
	s.mu.Unlock()
}

func (s *Session) Unregister(name string) {
	s.mu.Lock()
	delete(s.channels, name)
	s.mu.Unlock()
}

func (s *Session) Ingest(ctx context.Context, msg storage.Message) (storage.Message, error) {
	if msg.CreatedAt == 0 {
		msg.CreatedAt = time.Now().UnixMilli()
	}
	msg.SessionID = s.ID
	id, err := s.hub.store.AppendMessage(ctx, msg)
	if err != nil {
		return msg, err
	}
	msg.ID = id
	s.fanout(ctx, msg, msg.Channel)
	return msg, nil
}

func (s *Session) Reply(ctx context.Context, text, target string) error {
	msg := storage.Message{
		SessionID:  s.ID,
		Channel:    "agent",
		AuthorKind: "agent",
		AuthorID:   "mineagent",
		AuthorName: "MineAgent",
		Text:       text,
		Target:     target,
		CreatedAt:  time.Now().UnixMilli(),
	}
	id, err := s.hub.store.AppendMessage(ctx, msg)
	if err != nil {
		return err
	}
	msg.ID = id
	s.fanout(ctx, msg, "")
	return nil
}

func (s *Session) fanout(ctx context.Context, msg storage.Message, exclude string) {
	s.mu.RLock()
	chans := make([]Channel, 0, len(s.channels))
	for name, ch := range s.channels {
		if name != exclude {
			chans = append(chans, ch)
		}
	}
	s.mu.RUnlock()
	for _, ch := range chans {
		if err := ch.Send(ctx, msg); err != nil {
			s.hub.log.Warn("channel send failed", "channel", ch.Name(), "err", err)
		}
	}
}
