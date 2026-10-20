package storage

import (
	"context"
	"time"
)

func (s *Store) BodyAudits(ctx context.Context, limit int) ([]AuditEntry, error) {
	if limit < 1 || limit > 50 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id,session_id,call_id,tool,risk,requester,args,decision,operator,result,created_at FROM tool_audit WHERE risk LIKE 'body:%' ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var a AuditEntry
		if err = rows.Scan(&a.ID, &a.SessionID, &a.CallID, &a.Tool, &a.Risk, &a.Requester, &a.Args, &a.Decision, &a.Operator, &a.Result, &a.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}
func (s *Store) CleanupBodyAudits(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM tool_audit WHERE id IN (SELECT id FROM tool_audit WHERE risk LIKE 'body:%' AND created_at<? LIMIT 1000)`, time.Now().Add(-90*24*time.Hour).UnixMilli())
	return err
}
