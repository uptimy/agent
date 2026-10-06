package identity

import "context"

type Principal struct {
	UserID   int64
	Role     string
	ReadOnly bool
}

type principalKey struct{}

func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

func (p Principal) CanRead() bool {
	return p.UserID > 0 && (p.Role == "admin" || p.Role == "viewer")
}

func (p Principal) CanAct() bool {
	return p.UserID > 0 && p.Role == "admin" && !p.ReadOnly
}
