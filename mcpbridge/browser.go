package mcpbridge

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"runtime"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type searchInput struct {
	Query string `json:"query" jsonschema:"搜索关键词，例如天气、北京今天天气；用户未指定城市时直接搜索天气，不要猜测所在地"`
}

type openInput struct {
	URL string `json:"url" jsonschema:"需要在本机默认浏览器打开的完整 http 或 https 网址"`
}

type browserOutput struct {
	URL     string `json:"url"`
	Message string `json:"message"`
}

// RunBrowserServer is a standalone stdio MCP server in the same executable.
func RunBrowserServer(ctx context.Context) error {
	return newBrowserServer(openBrowser).Run(ctx, &mcp.StdioTransport{})
}

func newBrowserServer(open func(context.Context, string) error) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "desktop-browser", Version: "1.0.0"}, nil)
	mcp.AddTool(server, &mcp.Tool{
		Name:        "search_browser",
		Description: "在用户电脑的默认浏览器打开必应搜索页面。用户说打开浏览器搜索天气、搜索某个主题时使用此工具。只打开页面，不读取搜索结果或获取实时天气。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input searchInput) (*mcp.CallToolResult, browserOutput, error) {
		query := strings.TrimSpace(input.Query)
		if query == "" {
			return nil, browserOutput{}, fmt.Errorf("搜索关键词不能为空")
		}
		target := "https://www.bing.com/search?" + url.Values{"q": {query}}.Encode()
		return browserResult(ctx, open, target)
	})
	mcp.AddTool(server, &mcp.Tool{
		Name:        "open_browser",
		Description: "在用户电脑的默认浏览器打开指定的 http/https 网页。用户要求打开某个网站时使用此工具。只打开页面，不读取页面内容。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, input openInput) (*mcp.CallToolResult, browserOutput, error) {
		target, err := validateBrowserURL(input.URL)
		if err != nil {
			return nil, browserOutput{}, err
		}
		return browserResult(ctx, open, target)
	})
	return server
}

func browserResult(ctx context.Context, open func(context.Context, string) error, target string) (*mcp.CallToolResult, browserOutput, error) {
	if err := open(ctx, target); err != nil {
		return nil, browserOutput{}, fmt.Errorf("打开本机浏览器失败: %w", err)
	}
	return nil, browserOutput{
		URL: target, Message: "已向系统默认浏览器发送打开请求。此工具不读取网页内容，请勿据此编造天气或搜索结果。",
	}, nil
}

func validateBrowserURL(raw string) (string, error) {
	target := strings.TrimSpace(raw)
	parsed, err := url.Parse(target)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", fmt.Errorf("只支持不含用户名和密码的完整 http/https 网址")
	}
	if strings.ContainsAny(target, "\x00\r\n") {
		return "", fmt.Errorf("网址包含无效字符")
	}
	return parsed.String(), nil
}

func openBrowser(ctx context.Context, target string) error {
	// Pass the URL as a single argument without interpolating it into a shell.
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.CommandContext(ctx, "rundll32.exe", "url.dll,FileProtocolHandler", target)
	case "darwin":
		cmd = exec.CommandContext(ctx, "open", target)
	case "linux":
		cmd = exec.CommandContext(ctx, "xdg-open", target)
	default:
		return fmt.Errorf("不支持的操作系统: %s", runtime.GOOS)
	}
	return cmd.Run()
}
