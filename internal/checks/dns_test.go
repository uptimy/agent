package checks

import "testing"

// LookupCNAME returns the domain's own name when it has no CNAME record, so
// that must count as no CNAME, not as the answer.
func TestCnameTarget(t *testing.T) {
	cases := []struct {
		domain, cname, want string
		ok                  bool
	}{
		{"www.example.com", "example.netlify.app.", "example.netlify.app", true},
		{"www.example.com", "example.netlify.app", "example.netlify.app", true},
		{"example.com", "example.com.", "", false},
		{"Example.COM", "example.com.", "", false},
		{"example.com.", "example.com.", "", false},
		{"example.com", "", "", false},
	}
	for _, c := range cases {
		got, ok := cnameTarget(c.domain, c.cname)
		if got != c.want || ok != c.ok {
			t.Errorf("cnameTarget(%q, %q) = %q, %v; want %q, %v", c.domain, c.cname, got, ok, c.want, c.ok)
		}
	}
}
