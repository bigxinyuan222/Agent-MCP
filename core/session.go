package core

import (
	"github.com/openai/openai-go/v3"
)

// Session 维护整个 agent 循环中的消息上下文（Chat Completions 格式）
type Session struct {
	Messages []openai.ChatCompletionMessageParamUnion
}

func NewSession() *Session {
	return &Session{}
}

func (s *Session) GetMessages() []openai.ChatCompletionMessageParamUnion {
	return s.Messages
}

func (s *Session) AddMessage(item ...openai.ChatCompletionMessageParamUnion) {
	s.Messages = append(s.Messages, item...)
}
