package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/uptimy/agent/internal/store"
)

type tokenLookup func(context.Context, string) (store.User, store.APIToken, error)

func (f tokenLookup) TokenUser(ctx context.Context, hash string) (store.User, store.APIToken, error) {
	return f(ctx, hash)
}

func TestAuthenticate(t *testing.T) {
	for _, tc := range []struct {
		name                  string
		headers               []string
		user                  store.User
		readOnly              bool
		lookupError           error
		wantLookup, wantValid bool
	}{
		{name: "admin", headers: []string{"Bearer upa_test"}, user: store.User{ID: 1, Role: "admin"}, wantLookup: true, wantValid: true},
		{name: "readonly admin", headers: []string{"Bearer upa_test"}, user: store.User{ID: 1, Role: "admin"}, readOnly: true, wantLookup: true, wantValid: true},
		{name: "viewer", headers: []string{"bearer upa_test"}, user: store.User{ID: 2, Role: "viewer"}, readOnly: true, wantLookup: true, wantValid: true},
		{name: "missing"},
		{name: "basic", headers: []string{"Basic upa_test"}},
		{name: "wrong prefix", headers: []string{"Bearer test"}},
		{name: "extra field", headers: []string{"Bearer upa_test extra"}},
		{name: "multiple headers", headers: []string{"Bearer upa_test", "Bearer upa_test"}},
		{name: "revoked", headers: []string{"Bearer upa_test"}, lookupError: store.ErrNotFound, wantLookup: true},
		{name: "private error", headers: []string{"Bearer upa_test"}, lookupError: errors.New("private database password"), wantLookup: true},
		{name: "invalid role", headers: []string{"Bearer upa_test"}, user: store.User{ID: 1, Role: "other"}, wantLookup: true},
		{name: "zero ID", headers: []string{"Bearer upa_test"}, user: store.User{Role: "admin"}, wantLookup: true},
		{name: "password change", headers: []string{"Bearer upa_test"}, user: store.User{ID: 1, Role: "admin", MustChangePassword: true}, wantLookup: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "http://localhost/mcp?token=upa_test", nil)
			r.Header["Authorization"] = tc.headers
			r.Header.Set("Cookie", "session=valid")
			called := false
			ctx := context.Background()
			tokens := tokenLookup(func(gotCtx context.Context, hash string) (store.User, store.APIToken, error) {
				called = true
				if gotCtx != ctx || hash != fmt.Sprintf("%x", sha256.Sum256([]byte("upa_test"))) {
					t.Error("incorrect context or token hash")
				}
				return tc.user, store.APIToken{ReadOnly: tc.readOnly}, tc.lookupError
			})
			p, err := Authenticate(ctx, r, tokens)
			if called != tc.wantLookup {
				t.Fatalf("lookup called=%v, want %v", called, tc.wantLookup)
			}
			if tc.wantValid {
				want := Principal{UserID: tc.user.ID, Role: tc.user.Role, ReadOnly: tc.readOnly}
				if err != nil || p != want {
					t.Fatalf("principal=%+v err=%v, want %+v", p, err, want)
				}
			} else if !errors.Is(err, ErrUnauthenticated) || p != (Principal{}) {
				t.Fatalf("failure was not sanitized: %+v, %v", p, err)
			}
		})
	}
	r := httptest.NewRequest(http.MethodPost, "http://localhost/mcp", nil)
	r.Header.Set("Authorization", "Bearer upa_test")
	if _, err := Authenticate(context.Background(), r, nil); !errors.Is(err, ErrUnauthenticated) {
		t.Fatalf("nil token store accepted: %v", err)
	}
}
