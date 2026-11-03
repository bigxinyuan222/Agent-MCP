package mcpbridge

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// The test executable acts as an external server to exercise actual stdio
// framing, subprocess startup, discovery, calls and shutdown without a GUI.
func TestMCPServerProcess(t *testing.T) {
	if os.Getenv("AI_AGENT_MCP_TEST_SERVER") != "1" {
		return
	}
	err := newBrowserServer(func(context.Context, string) error { return nil }).Run(context.Background(), &mcp.StdioTransport{})
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestConnectStdio(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	manager := Connect(ctx, Config{MCPServers: map[string]ServerConfig{
		"browser": {Command: executable, Args: []string{"-test.run=^TestMCPServerProcess$"}, Env: map[string]string{"AI_AGENT_MCP_TEST_SERVER": "1"}},
		"broken":  {Command: filepath.Join(t.TempDir(), "missing-server.exe")},
		"off":     {Disabled: true},
	}})
	cancel() // Startup cancellation must not end an established MCP session.
	t.Cleanup(func() { _ = manager.Close() })
	if len(manager.Tools) != 2 || len(manager.Warnings) != 1 || !strings.Contains(manager.Warnings[0], "broken") {
		t.Fatalf("tools=%d, warnings=%v", len(manager.Tools), manager.Warnings)
	}
	for _, tool := range manager.Tools {
		if strings.Contains(tool.Name, "search_browser") {
			text, err := tool.Call(context.Background(), `{"query":"天气"}`)
			if err != nil || !strings.Contains(text, "bing.com/search") {
				t.Fatalf("stdio call: %q, %v", text, err)
			}
		}
	}
	if err := manager.Close(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestLoadConfig(t *testing.T) {
	t.Chdir(t.TempDir())
	config, err := LoadConfig("")
	if err != nil || config.MCPServers["browser"].Command != "@self" {
		t.Fatalf("default config: %v, %v", config, err)
	}
	if _, err := LoadConfig("missing.json"); err == nil {
		t.Fatal("explicit missing config was accepted")
	}
	for _, data := range []string{"{", "{}", `{"mcpServers":null}`} {
		if err := os.WriteFile("mcp.json", []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadConfig(""); err == nil {
			t.Fatalf("invalid config accepted: %s", data)
		}
	}
	if err := os.WriteFile("mcp.json", []byte(`{"mcpServers":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if config, err := LoadConfig(""); err != nil || len(config.MCPServers) != 0 {
		t.Fatalf("MCP disabled config: %v, %v", config, err)
	}
}

func TestToolNamesAreCompatibleAndDistinct(t *testing.T) {
	valid := regexp.MustCompile(`^[a-zA-Z0-9_-]{1,64}$`)
	pairs := [][2]string{
		{"browser", "search_browser"}, {"a_b", "c"}, {"a", "b_c"},
		{"server", "a.b"}, {"server", "a_b"}, {"浏览器", "搜索"},
		{"long", strings.Repeat("x", 100) + "a"}, {"long", strings.Repeat("x", 100) + "b"},
	}
	seen := map[string]bool{}
	for _, pair := range pairs {
		name := toolName(pair[0], pair[1])
		if !valid.MatchString(name) || seen[name] {
			t.Fatalf("invalid or colliding name: %q", name)
		}
		seen[name] = true
	}
}
