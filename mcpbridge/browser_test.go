package mcpbridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBrowserToolsOverMCP(t *testing.T) {
	ctx := context.Background()
	var opened []string
	var openErr error
	server := newBrowserServer(func(_ context.Context, target string) error {
		if openErr != nil {
			return openErr
		}
		opened = append(opened, target)
		return nil
	})
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	ss, err := server.Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ss.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	list, err := session.ListTools(ctx, nil)
	if err != nil || len(list.Tools) != 2 {
		t.Fatalf("ListTools: %v, %v", list, err)
	}
	for _, remote := range list.Tools {
		if remote.Name != "search_browser" {
			continue
		}
		tool, err := adaptTool("browser", session, remote)
		if err != nil {
			t.Fatal(err)
		}
		query := "北京天气 & 空气质量 \"今天\""
		args, _ := json.Marshal(map[string]string{"query": query})
		text, err := tool.Call(ctx, string(args))
		if err != nil || !strings.Contains(text, "已向系统默认浏览器发送打开请求") {
			t.Fatalf("search result = %q, %v", text, err)
		}
		if len(opened) != 1 {
			t.Fatalf("opened %d pages, want 1", len(opened))
		}
		parsed, err := url.Parse(opened[0])
		if err != nil || parsed.Host != "www.bing.com" || parsed.Query().Get("q") != query {
			t.Fatalf("search did not preserve query: %q", opened[0])
		}
		openErr = errors.New("desktop unavailable")
		if _, err := tool.Call(ctx, `{"query":"天气"}`); err == nil || !strings.Contains(err.Error(), "desktop unavailable") {
			t.Fatalf("browser failure was not propagated: %v", err)
		}
		openErr = nil
	}
	for _, args := range []map[string]any{
		{"url": "javascript:alert(1)"},
		{"url": "file:///C:/Windows/notepad.exe"},
		{"url": "https://user:password@example.com"},
		{"url": "https://example.com\r\ninvalid"},
		{"url": "example.com"},
		{},
	} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "open_browser", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid URL accepted: %v", args)
		}
	}
	for _, args := range []map[string]any{{"query": "  "}, {}, {"query": 42}} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "search_browser", Arguments: args})
		if err == nil && !result.IsError {
			t.Fatalf("invalid query accepted: %v", args)
		}
	}
	if len(opened) != 1 {
		t.Fatalf("invalid arguments launched a browser: %v", opened)
	}
	result, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "open_browser", Arguments: map[string]any{"url": "https://example.com/?a=1&b=2"},
	})
	if err != nil || result.IsError || len(opened) != 2 || opened[1] != "https://example.com/?a=1&b=2" {
		t.Fatalf("valid URL failed: %v, %v, %v", result, err, opened)
	}
}
