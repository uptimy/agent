package config

import "testing"

func TestMCPConfiguration(t *testing.T) {
	t.Setenv("MCP_ENABLED", "")
	t.Setenv("MCP_ALLOWED_HOSTS", "")
	t.Setenv("MCP_MAX_CONCURRENT", "")
	c, err := Load()
	if err != nil || c.MCPEnabled || c.MCPMaxConcurrent != 4 {
		t.Fatalf("default: %+v %v", c, err)
	}
	t.Setenv("MCP_ENABLED", "true")
	if _, err := Load(); err == nil {
		t.Fatal("enabled MCP without explicit allowed hosts")
	}
	t.Setenv("MCP_ALLOWED_HOSTS", " agent.internal:8080, localhost:8080 ")
	c, err = Load()
	if err != nil || !c.MCPEnabled || len(c.MCPAllowedHosts) != 2 || c.MCPAllowedHosts[0] != "agent.internal:8080" {
		t.Fatalf("enabled: %+v %v", c, err)
	}
	for _, v := range []string{"0", "33", "invalid"} {
		t.Setenv("MCP_MAX_CONCURRENT", v)
		if _, err := Load(); err == nil {
			t.Fatalf("accepted concurrency %s", v)
		}
	}
}
