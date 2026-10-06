package investigation

import (
	"context"
	"errors"

	"github.com/uptimy/agent/internal/identity"
)

var ErrForbidden = errors.New("operation not permitted")
var ErrInvalidInput = errors.New("invalid investigation input")

// Authorize distinguishes operations, not HTTP verbs. Actions are reserved for
// future adapters and are never registered by this read-only release.
func Authorize(ctx context.Context, action bool) error {
	p, ok := identity.FromContext(ctx)
	if !ok || !p.CanRead() || (action && !p.CanAct()) {
		return ErrForbidden
	}
	return nil
}
