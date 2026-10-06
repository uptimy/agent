package identity

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"

	"github.com/uptimy/agent/internal/store"
)

type TokenStore interface {
	TokenUser(ctx context.Context, hash string) (store.User, store.APIToken, error)
}

var ErrUnauthenticated = errors.New("invalid API token")

func Authenticate(ctx context.Context, r *http.Request, tokens TokenStore) (Principal, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || tokens == nil {
		return Principal{}, ErrUnauthenticated
	}
	parts := strings.Fields(values[0])
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !strings.HasPrefix(parts[1], "upa_") {
		return Principal{}, ErrUnauthenticated
	}
	sum := sha256.Sum256([]byte(parts[1]))
	u, token, err := tokens.TokenUser(ctx, hex.EncodeToString(sum[:]))
	if err != nil || u.MustChangePassword {
		return Principal{}, ErrUnauthenticated
	}
	p := Principal{UserID: u.ID, Role: u.Role, ReadOnly: token.ReadOnly}
	if !p.CanRead() {
		return Principal{}, ErrUnauthenticated
	}
	return p, nil
}
