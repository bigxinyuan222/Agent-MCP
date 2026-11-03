// main.go：bubbletea v2 的聊天式 TUI（viewport 滚动消息区 + 单行 textinput 输入框），
// 后端接千问 DashScope，支持工具调用。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"simple-agent/core"
	"simple-agent/mcpbridge"
	"simple-agent/tools"

	"charm.land/bubbles/v2/textinput"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

const MaxLoop = 10

// agentEvent：后台 agent 循环每一步发出的事件，作为 tea.Msg 回传给 Update
type agentEvent struct {
	kind string // "tool" | "toolResult" | "result" | "error" | "done"
	text string
}

type model struct {
	// —— 界面组件 ——
	viewport    viewport.Model  // 可滚动消息显示区
	messages    []string        // 显示用的对话行
	input       textinput.Model // 底部单行输入框
	senderStyle lipgloss.Style  // “你:” 的着色
	err         error

	// —— agent 状态 ——
	session     *core.Session                         // 多轮对话上下文
	client      openai.Client                         // 千问客户端
	llmTools    map[string]*tools.ToolDef             // 本地工具表
	openaiTools []openai.ChatCompletionToolUnionParam // 给模型看的工具声明
	waiting     bool                                  // 是否正在等模型
	currentChan chan agentEvent                       // 当前轮 agent 事件 channel
	ctx         context.Context
}

func getLlmTools() map[string]*tools.ToolDef {
	llmTools := make(map[string]*tools.ToolDef)
	timeTool := tools.NewGetCurrentTime()
	llmTools[timeTool.Name] = timeTool
	return llmTools
}

func initialModel(ctx context.Context, mcpTools *mcpbridge.Manager, apiKey string) model {
	// 单行输入框，CHAT模板
	ti := textinput.New()
	ti.Placeholder = "问点什么，回车发送（Ctrl+C / Esc 退出）"
	ti.Focus()

	// 消息滚动区
	vp := viewport.New(viewport.WithWidth(80), viewport.WithHeight(20))
	vp.SetContent(`欢迎使用 AI-Agent（千问 qwen-plus）！`)
	vp.KeyMap.Left.SetEnabled(false)
	vp.KeyMap.Right.SetEnabled(false)

	m := model{
		input:       ti,
		messages:    []string{},
		viewport:    vp,
		senderStyle: lipgloss.NewStyle().Foreground(lipgloss.Color("5")), // 紫色
		ctx:         ctx,
	}
	//，CHAT模板
	// 系统提示词
	m.session = core.NewSession()
	m.session.AddMessage(openai.ChatCompletionMessageParamUnion{
		OfSystem: &openai.ChatCompletionSystemMessageParam{
			Content: openai.ChatCompletionSystemMessageParamContentUnion{
				OfString: openai.String("你是专业的助手，回答简洁干练，只回答与用户问题相关的内容。能用工具就用工具，根据工具返回的内容如实回答。" +
					"用户要求打开浏览器搜索天气或其他关键词时，调用 MCP 的 search_browser 工具；要求打开网址时调用 MCP 的 open_browser 工具。" +
					"未提供城市时可以直接搜索天气，不要猜测用户所在地。只有工具成功后才能说已发送打开请求。" +
					"浏览器工具只能打开网页，不能读取网页或取得实时天气，不得根据打开成功编造天气数据。工具不可用或调用失败时如实告知。"),
			},
		},
	})

	m.llmTools = getLlmTools()
	for _, tool := range mcpTools.Tools {
		m.llmTools[tool.Name] = tool
	}
	m.openaiTools = make([]openai.ChatCompletionToolUnionParam, 0, len(m.llmTools))
	toolNames := make([]string, 0, len(m.llmTools))
	for name := range m.llmTools {
		toolNames = append(toolNames, name)
	}
	sort.Strings(toolNames)
	for _, name := range toolNames {
		m.openaiTools = append(m.openaiTools, m.llmTools[name].ToParam())
	}
	m.messages = append(m.messages, fmt.Sprintf("欢迎使用 AI-Agent（千问 qwen-plus）！已加载 %d 个 MCP 工具。", len(mcpTools.Tools)))
	for _, warning := range mcpTools.Warnings {
		m.messages = append(m.messages, warning)
	}
	m.refreshViewport()

	m.client = openai.NewClient(
		option.WithAPIKey(apiKey),
		option.WithBaseURL("https://dashscope.aliyuncs.com/compatible-mode/v1"),
	)
	return m
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

// refreshViewport：把 messages 渲染进 viewport 并滚到底
func (m *model) refreshViewport() {
	m.viewport.SetContent(
		lipgloss.NewStyle().
			Width(m.viewport.Width()).
			Render(strings.Join(m.messages, "\n")),
	)
	m.viewport.GotoBottom()
}

func waitEvent(ch chan agentEvent) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return agentEvent{kind: "done"}
		}
		return ev
	}
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	// 窗口大小变化
	case tea.WindowSizeMsg:
		m.viewport.SetWidth(msg.Width)
		m.input.SetWidth(msg.Width)
		m.viewport.SetHeight(msg.Height - 1)
		if len(m.messages) > 0 {
			m.refreshViewport()
		}
		m.viewport.GotoBottom()

	// 按键
	case tea.KeyPressMsg:
		switch msg.String() {
		case "ctrl+c", "esc":
			return m, tea.Quit
		case "enter":
			if m.waiting {
				return m, nil
			}
			q := strings.TrimSpace(m.input.Value())
			if q == "" {
				return m, nil
			}
			m.messages = append(m.messages, m.senderStyle.Render("You: ")+q)
			m.input.SetValue("")
			m.refreshViewport()
			m.waiting = true

			evCh := make(chan agentEvent, 16)
			m.currentChan = evCh
			go m.runAgent(q, evCh)
			return m, waitEvent(evCh)
		}
		// 其它按键交给输入框
		var cmd tea.Cmd
		m.input, cmd = m.input.Update(msg)
		return m, cmd

	// 后台 agent 事件
	case agentEvent:
		switch msg.kind {
		case "tool":
			m.messages = append(m.messages, "  → 调用工具: "+msg.text)
		case "toolResult":
			m.messages = append(m.messages, "  ← 工具结果: "+msg.text)
		case "result":
			m.messages = append(m.messages, lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Render("Assistant: ")+msg.text)
			m.waiting = false
		case "error":
			m.messages = append(m.messages, lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Render("出错: ")+msg.text)
			m.waiting = false
		case "done":
			m.waiting = false
		}
		m.refreshViewport()
		if m.waiting && m.currentChan != nil {
			return m, waitEvent(m.currentChan)
		}
		return m, nil
	}

	// 其它消息（如光标闪烁）转发给输入框
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(msg)
	return m, cmd
}

// runAgent：后台多轮 agent 循环，每步发事件，不直接碰 UI
func (m model) runAgent(question string, evCh chan<- agentEvent) {
	defer close(evCh)
	ctx, cancel := context.WithTimeout(m.ctx, 3*time.Minute)
	defer cancel()

	m.session.AddMessage(openai.ChatCompletionMessageParamUnion{
		OfUser: &openai.ChatCompletionUserMessageParam{
			Content: openai.ChatCompletionUserMessageParamContentUnion{
				OfString: openai.String(question),
			},
		},
	})

	for i := 0; i <= MaxLoop; i++ {
		resp, err := m.client.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Messages: m.session.GetMessages(),
			Tools:    m.openaiTools,
			Model:    "qwen-plus",
		})
		if err != nil {
			evCh <- agentEvent{kind: "error", text: err.Error()}
			return
		}
		if len(resp.Choices) == 0 {
			evCh <- agentEvent{kind: "error", text: "模型未返回任何回答"}
			return
		}
		msg := resp.Choices[0].Message

		if len(msg.ToolCalls) > 0 {
			m.session.AddMessage(msg.ToParam())
			for _, tc := range msg.ToolCalls {
				fc := tc.AsFunction()
				evCh <- agentEvent{kind: "tool", text: fc.Function.Name + " " + fc.Function.Arguments}
				toolRe := "工具不存在或未连接: " + fc.Function.Name
				if v, ok := m.llmTools[fc.Function.Name]; ok {
					var terr error
					toolRe, terr = v.Call(ctx, fc.Function.Arguments)
					if terr != nil {
						toolRe = fc.Function.Name + ": " + terr.Error()
					}
				}
				evCh <- agentEvent{kind: "toolResult", text: toolRe}
				m.session.AddMessage(openai.ToolMessage(toolRe, fc.ID))
			}
			continue
		}

		// 没有工具调用，就是最终回答：把这条 assistant 回复也写回会话，
		// 否则下一轮对话模型看不到上一轮的回答，无法引用上下文
		m.session.AddMessage(msg.ToParam())
		evCh <- agentEvent{kind: "result", text: msg.Content}
		return
	}
	evCh <- agentEvent{kind: "error", text: "超过最大轮次 " + fmt.Sprint(MaxLoop)}
}

// View：v2 返回 tea.View，全屏 AltScreen
func (m model) View() tea.View {
	viewportView := m.viewport.View()
	v := tea.NewView(viewportView + "\n" + m.input.View())
	c := m.input.Cursor()
	if c != nil {
		c.Y += lipgloss.Height(viewportView)
	}
	v.Cursor = c
	v.AltScreen = true
	return v
}

func run() error {
	browserServer := flag.Bool("mcp-browser-server", false, "以 stdio 模式运行内置浏览器 MCP 服务")
	mcpConfig := flag.String("mcp-config", "", "MCP 配置路径（默认读取 mcp.json，不存在时使用内置浏览器）")
	flag.Parse()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if *browserServer {
		return mcpbridge.RunBrowserServer(ctx)
	}
	apiKey := strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY"))
	if apiKey == "" {
		return fmt.Errorf("请先设置环境变量 DASHSCOPE_API_KEY，再启动 AI-Agent")
	}
	config, err := mcpbridge.LoadConfig(*mcpConfig)
	if err != nil {
		return err
	}
	mcpTools := mcpbridge.Connect(ctx, config)
	defer func() {
		cancel()
		_ = mcpTools.Close()
	}()
	p := tea.NewProgram(initialModel(ctx, mcpTools, apiKey))
	if _, err := p.Run(); err != nil {
		return err
	}
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "AI-Agent: %v\n", err)
		os.Exit(1)
	}
}
