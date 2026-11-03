package mcpbridge

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"simple-agent/tools"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/openai/openai-go/v3"
)

const (
	startupTimeout = 15 * time.Second
	toolTimeout    = 30 * time.Second
)

type Manager struct {
	Tools    []*tools.ToolDef
	Warnings []string
	sessions []*mcp.ClientSession
}

// Connect discovers tools over real MCP sessions. One failed server does not
// prevent the remaining servers or the local tools from being used.
func Connect(ctx context.Context, config Config) *Manager {
	manager := &Manager{}
	names := make([]string, 0, len(config.MCPServers))
	for name := range config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		server := config.MCPServers[name]
		if server.Disabled {
			continue
		}
		if err := manager.connectServer(ctx, name, server); err != nil {
			manager.Warnings = append(manager.Warnings, fmt.Sprintf("MCP %s 连接失败: %v", name, err))
		}
	}
	return manager
}

func (m *Manager) connectServer(ctx context.Context, name string, config ServerConfig) error {
	command := config.Command
	if strings.TrimSpace(command) == "" {
		return errors.New("command 不能为空")
	}
	if command == "@self" {
		var err error
		command, err = os.Executable()
		if err != nil {
			return err
		}
	}
	cmd := exec.Command(command, config.Args...)
	cmd.Env = os.Environ()
	keys := make([]string, 0, len(config.Env))
	for key := range config.Env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+config.Env[key])
	}
	// A server's stdout belongs exclusively to the MCP protocol.
	cmd.Stderr = os.Stderr
	client := mcp.NewClient(&mcp.Implementation{Name: "simple-agent", Version: "1.0.0"}, nil)
	startupCtx, cancel := context.WithTimeout(ctx, startupTimeout)
	defer cancel()
	session, err := client.Connect(startupCtx, &mcp.CommandTransport{
		Command: cmd, TerminateDuration: time.Second,
	}, nil)
	if err != nil {
		return err
	}
	var discovered []*tools.ToolDef
	for remote, err := range session.Tools(startupCtx, nil) {
		if err == nil {
			var tool *tools.ToolDef
			tool, err = adaptTool(name, session, remote)
			if err == nil {
				discovered = append(discovered, tool)
			}
		}
		if err != nil {
			_ = session.Close()
			return fmt.Errorf("发现工具: %w", err)
		}
	}
	m.sessions = append(m.sessions, session)
	m.Tools = append(m.Tools, discovered...)
	return nil
}

func (m *Manager) Close() error {
	var errs []error
	for _, session := range m.sessions {
		errs = append(errs, session.Close())
	}
	return errors.Join(errs...)
}

// Keep names compatible with Chat Completions, including servers whose MCP
// tool names contain dots or are longer than the 64-character API limit.
func toolName(server, remote string) string {
	raw := "mcp_" + server + "_" + remote
	name := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, raw)
	// Hash the unambiguous pair as well as keeping a readable prefix, avoiding
	// collisions after sanitizing, truncating or joining server/tool names.
	sum := sha256.Sum256([]byte(server + "\x00" + remote))
	if len(name) > 47 {
		name = name[:47]
	}
	return fmt.Sprintf("%s_%x", name, sum[:8])
}

func adaptTool(server string, session *mcp.ClientSession, remote *mcp.Tool) (*tools.ToolDef, error) {
	schema, err := json.Marshal(remote.InputSchema)
	if err != nil {
		return nil, err
	}
	var params openai.FunctionParameters
	if err := json.Unmarshal(schema, &params); err != nil {
		return nil, err
	}
	if params == nil {
		return nil, fmt.Errorf("工具 %s 缺少参数 schema", remote.Name)
	}
	return &tools.ToolDef{
		Name:        toolName(server, remote.Name),
		Description: fmt.Sprintf("[MCP %s/%s] %s", server, remote.Name, remote.Description),
		Params:      params,
		Fn: func(ctx context.Context, args map[string]any) (string, error) {
			callCtx, cancel := context.WithTimeout(ctx, toolTimeout)
			defer cancel()
			result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: remote.Name, Arguments: args})
			if err != nil {
				return "", fmt.Errorf("MCP %s/%s: %w", server, remote.Name, err)
			}
			return formatResult(result)
		},
	}, nil
}

func formatResult(result *mcp.CallToolResult) (string, error) {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if result.StructuredContent != nil {
		data, err := json.Marshal(result.StructuredContent)
		if err != nil {
			return "", fmt.Errorf("编码 MCP 结果: %w", err)
		}
		parts = append(parts, string(data))
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		text = "MCP 工具未返回可读取的文本结果"
	}
	if result.IsError {
		return "", errors.New(text)
	}
	return text, nil
}
