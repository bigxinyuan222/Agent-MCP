package mcpbridge

import (
	"encoding/json"
	"fmt"
	"os"
)

// Config uses the conventional MCP stdio server configuration format.
type Config struct {
	MCPServers map[string]ServerConfig `json:"mcpServers"`
}

type ServerConfig struct {
	Command  string            `json:"command"`
	Args     []string          `json:"args,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Disabled bool              `json:"disabled,omitempty"`
}

// LoadConfig defaults to the bundled browser when mcp.json does not exist.
// An explicitly supplied path must exist; an empty mcpServers disables MCP.
func LoadConfig(path string) (Config, error) {
	explicit := path != ""
	if !explicit {
		path = "mcp.json"
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && !explicit {
		return Config{MCPServers: map[string]ServerConfig{
			"browser": {Command: "@self", Args: []string{"--mcp-browser-server"}},
		}}, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("读取 MCP 配置 %s: %w", path, err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return Config{}, fmt.Errorf("解析 MCP 配置 %s: %w", path, err)
	}
	if config.MCPServers == nil {
		return Config{}, fmt.Errorf("MCP 配置 %s 缺少 mcpServers 对象", path)
	}
	return config, nil
}
