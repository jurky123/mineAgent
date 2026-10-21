package tools

import (
	"context"
	"github.com/cloudwego/eino/compose"
)

type externalApprovalKey struct{}
type approvalDecisionKey struct{}

// ExternalApprovalPending lets a non-Eino runner suspend after tool cleanup.
// The receiver is registered atomically when the approval is created.
type ExternalApprovalPending struct{ Info ApprovalInfo }

func (e *ExternalApprovalPending) Error() string { return "waiting for approval" }
func WithExternalApprovals(ctx context.Context) (context.Context, <-chan ApprovalDecision) {
	ch := make(chan ApprovalDecision, 1)
	return context.WithValue(ctx, externalApprovalKey{}, ch), ch
}
func externalApprovalChannel(ctx context.Context) chan ApprovalDecision {
	ch, _ := ctx.Value(externalApprovalKey{}).(chan ApprovalDecision)
	return ch
}
func InterruptForApproval(ctx context.Context, info *ApprovalInfo) error {
	if externalApprovalChannel(ctx) != nil {
		return &ExternalApprovalPending{Info: *info}
	}
	return compose.Interrupt(ctx, info)
}
func WithApprovalDecision(ctx context.Context, d *ApprovalDecision) context.Context {
	return context.WithValue(ctx, approvalDecisionKey{}, d)
}
func ApprovalResumeContext(ctx context.Context) (bool, bool, *ApprovalDecision) {
	if d, ok := ctx.Value(approvalDecisionKey{}).(*ApprovalDecision); ok {
		return true, true, d
	}
	return compose.GetResumeContext[*ApprovalDecision](ctx)
}
