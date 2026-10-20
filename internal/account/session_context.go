package account

import "context"

type sessionIDKey struct{}

func WithSessionID(ctx context.Context, id int64) context.Context {
	return context.WithValue(ctx, sessionIDKey{}, id)
}
func SessionIDFromContext(ctx context.Context) int64 {
	id, _ := ctx.Value(sessionIDKey{}).(int64)
	return id
}

// RecheckAdminSession validates an existing verified session without carrying its raw token into agent state.
func (s *Service) RecheckAdminSession(ctx context.Context, userID, sessionID int64) bool {
	u, err := s.store.VerifiedSessionUserByID(ctx, userID, sessionID)
	return err == nil && u != nil && s.LoginAllowed(u.Username) && s.IsAdminName(u.Username)
}
