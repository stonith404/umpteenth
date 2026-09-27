//go:build unit || integration

package openai

import (
	"encoding/json"

	"github.com/stonith404/umpteenth/backend/internal/llm"
)

func userText(text string) llm.Message {
	return llm.Message{Role: llm.RoleUser, Parts: []llm.Part{llm.TextPart(text)}}
}

var weatherTool = llm.ToolDef{Name: "get_weather", Description: "Get the weather for a city", Schema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`)}
