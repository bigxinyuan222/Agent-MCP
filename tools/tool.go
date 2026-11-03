package tools

import (
	"context"
	"encoding/json"

	"github.com/openai/openai-go/v3"
)

// ToolDef 描述一个可供 LLM 调用的工具
type ToolDef struct {
	Name        string
	Description string
	Params      openai.FunctionParameters
	Fn          func(ctx context.Context, args map[string]any) (string, error)
}

// ToParam 转成 Chat Completions API 的工具声明格式
func (t ToolDef) ToParam() openai.ChatCompletionToolUnionParam {
	return openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
		Name:        t.Name,
		Description: openai.String(t.Description),
		Parameters:  t.Params,
	})
}

// Call 解析 JSON 参数并调用底层函数
func (t ToolDef) Call(ctx context.Context, argsJSON string) (string, error) {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsJSON), &args); err != nil {
		return "", err
	}
	return t.Fn(ctx, args)
}
