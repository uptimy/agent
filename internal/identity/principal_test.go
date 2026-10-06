package identity

import (
	"context"
	"testing"
)

func TestPrincipal(t *testing.T) {
	for _, tc := range []struct {
		p         Principal
		read, act bool
	}{
		{Principal{UserID: 1, Role: "admin"}, true, true},
		{Principal{UserID: 1, Role: "admin", ReadOnly: true}, true, false},
		{Principal{UserID: 1, Role: "viewer"}, true, false},
		{Principal{UserID: 1, Role: "viewer", ReadOnly: true}, true, false},
		{Principal{UserID: 1, Role: "other"}, false, false},
		{Principal{Role: "admin"}, false, false},
		{Principal{UserID: -1, Role: "admin"}, false, false},
	} {
		if tc.p.CanRead() != tc.read || tc.p.CanAct() != tc.act {
			t.Errorf("permissions for %+v", tc.p)
		}
		ctx := WithPrincipal(context.Background(), tc.p)
		if got, ok := FromContext(ctx); !ok || got != tc.p {
			t.Errorf("context round trip: %+v, %v", got, ok)
		}
	}
	if _, ok := FromContext(context.Background()); ok {
		t.Fatal("missing principal must not authenticate")
	}
}
