package version

import (
	"strings"
	"testing"
)

func TestUserAgentIsNotTheCloudCheckers(t *testing.T) {
	ua := UserAgent()
	if ua != "uptimy-agent/"+Version {
		t.Fatalf("user agent %q", ua)
	}
	// A rule matching the agent must not match upti.my-healthcheck/1.0.
	if strings.Contains(ua, "upti.my") || strings.Contains(ua, "healthcheck") {
		t.Fatalf("user agent %q overlaps the Cloud healthchecker's", ua)
	}
}
