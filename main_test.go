package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"simple-agent/mcpbridge"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

func TestAgentMCPServerProcess(t *testing.T) {
	if os.Getenv("AI_AGENT_CHAT_TEST_SERVER") != "1" {
		return
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "chat-test"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "search_browser", Description: "搜索浏览器"},
		func(_ context.Context, _ *mcp.CallToolRequest, input struct {
			Query string `json:"query"`
		}) (*mcp.CallToolResult, any, error) {
			return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "MCP 已搜索: " + input.Query}}}, nil, nil
		})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

func TestAgentUsesMCPAndKeepsToolResults(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	manager := mcpbridge.Connect(context.Background(), mcpbridge.Config{MCPServers: map[string]mcpbridge.ServerConfig{
		"browser": {Command: executable, Args: []string{"-test.run=^TestAgentMCPServerProcess$"}, Env: map[string]string{"AI_AGENT_CHAT_TEST_SERVER": "1"}},
	}})
	t.Cleanup(func() { _ = manager.Close() })
	if len(manager.Tools) != 1 {
		t.Fatalf("MCP discovery: %v", manager.Warnings)
	}
	for _, unknown := range []bool{false, true} {
		label := "browser_search"
		if unknown {
			label = "unknown_tool"
		}
		t.Run(label, func(t *testing.T) {
			name := manager.Tools[0].Name
			expected := "MCP 已搜索: 北京天气"
			if unknown {
				name = "missing_tool"
				expected = "工具不存在或未连接: missing_tool"
			}
			type message struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
			}
			var requests [][]message
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []message `json:"messages"`
					Tools    []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tools"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Errorf("decode API request: %v", err)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				requests = append(requests, request.Messages)
				w.Header().Set("Content-Type", "application/json")
				if len(requests) == 1 {
					found := false
					for _, tool := range request.Tools {
						found = found || tool.Function.Name == manager.Tools[0].Name
					}
					if !found {
						t.Error("MCP tool was not advertised to the model")
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
						"message": map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{
							"id": "call_weather", "type": "function", "function": map[string]any{"name": name, "arguments": `{"query":"北京天气"}`},
						}}},
					}}})
					return
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{
					"message": map[string]any{"role": "assistant", "content": "操作完成"},
				}}})
			}))
			t.Cleanup(api.Close)
			m := initialModel(context.Background(), manager, "test-key")
			m.client = openai.NewClient(option.WithAPIKey("test-key"), option.WithBaseURL(api.URL), option.WithMaxRetries(0))
			events := make(chan agentEvent, 16)
			m.runAgent("打开浏览器搜索北京天气", events)
			var kinds []string
			for event := range events {
				kinds = append(kinds, event.kind)
				if event.kind == "error" {
					t.Errorf("agent error: %s", event.text)
				}
			}
			if strings.Join(kinds, ",") != "tool,toolResult,result" || len(requests) != 2 {
				t.Fatalf("events=%v, API requests=%d", kinds, len(requests))
			}
			last := requests[1][len(requests[1])-1]
			if last.Role != "tool" || last.ToolCallID != "call_weather" || last.Content != expected {
				t.Fatalf("tool result missing from model context: %+v", last)
			}
			// A following user turn must preserve the previous tool exchange and answer.
			m.runAgent("谢谢", make(chan agentEvent, 16))
			if len(requests) != 3 || requests[2][len(requests[2])-2].Content != "操作完成" {
				t.Fatalf("conversation history was not preserved: %v", requests)
			}
		})
	}
}
