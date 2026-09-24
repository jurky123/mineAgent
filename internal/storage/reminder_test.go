package storage

import (
	"context"
	"path/filepath"
	"testing"
)

func TestReminders(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(filepath.Join(dir, "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ctx := context.Background()

	now := int64(1_000_000)
	id, err := st.AddReminder(ctx, Reminder{SessionID: "web:c2c:a", Channel: "web", Target: "c2c:a", Text: "喝水", DueAt: now + 60_000, CreatedAt: now})
	if err != nil || id == 0 {
		t.Fatalf("add: %v %d", err, id)
	}
	if _, err := st.AddReminder(ctx, Reminder{SessionID: "web:c2c:a", Channel: "web", Text: "睡觉", DueAt: now + 120_000, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}

	if n, _ := st.CountPendingReminders(ctx, "web:c2c:a"); n != 2 {
		t.Fatalf("pending = %d", n)
	}
	list, err := st.ListReminders(ctx, "web:c2c:a")
	if err != nil || len(list) != 2 || list[0].Text != "喝水" {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	// 未到点不该触发
	if due, _ := st.DueReminders(ctx, now, 10); len(due) != 0 {
		t.Fatalf("不该有到期: %+v", due)
	}
	due, err := st.DueReminders(ctx, now+70_000, 10)
	if err != nil || len(due) != 1 || due[0].ID != id {
		t.Fatalf("due = %+v err=%v", due, err)
	}
	if err := st.CompleteReminder(ctx, id); err != nil {
		t.Fatal(err)
	}
	if due, _ := st.DueReminders(ctx, now+70_000, 10); len(due) != 0 {
		t.Fatalf("完成后不该再触发: %+v", due)
	}
	// 取消：只能取消自己的
	if ok, _ := st.CancelReminder(ctx, "web:c2c:b", 2); ok {
		t.Fatal("别人的提醒不该能取消")
	}
	if ok, err := st.CancelReminder(ctx, "web:c2c:a", 2); err != nil || !ok {
		t.Fatalf("cancel: %v %v", ok, err)
	}
	if n, _ := st.CountPendingReminders(ctx, "web:c2c:a"); n != 0 {
		t.Fatalf("取消后 pending = %d", n)
	}
}
