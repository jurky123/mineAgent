package agent

import (
	"context"
	"time"

	"mineagent/internal/storage"
)

type checkpointStore struct {
	store *storage.Store
}

func newCheckpointStore(store *storage.Store) *checkpointStore {
	return &checkpointStore{store: store}
}

func (c *checkpointStore) Get(ctx context.Context, checkPointID string) ([]byte, bool, error) {
	return c.store.LoadCheckpoint(ctx, checkPointID)
}

func (c *checkpointStore) Set(ctx context.Context, checkPointID string, checkPoint []byte) error {
	return c.store.SaveCheckpoint(ctx, checkPointID, checkPoint, time.Now().UnixMilli())
}

func (c *checkpointStore) Delete(ctx context.Context, checkPointID string) error {
	return c.store.DeleteCheckpoint(ctx, checkPointID)
}
