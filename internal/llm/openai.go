package llm

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type ChatMessage struct {
	Role       string `json:"role"`
	Content    any    `json:"content"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolCalls  []any  `json:"tool_calls,omitempty"`
}

type ToolCallInfo struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Arguments map[string]any    `json:"arguments"`
	RawArgs   string            `json:"-"`
}

type ToolResultInfo struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Result string `json:"result"`
	IsErr  bool   `json:"isError"`
}

type StreamEvent struct {
	Delta        string
	Reasoning    string
	ToolCalls    []ToolCallInfo
	ToolResults  []ToolResultInfo
	Done         bool
	FinishReason string
	Error        string
}

type ToolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	Parameters  map[string]any `json:"parameters"`
}

type Provider interface {
	Stream(messages []ChatMessage, model string, tools []ToolDef, onEvent func(StreamEvent)) error
}

type OpenAI struct {
	APIKey          string
	APIBase         string
	EndpointOverride string
	Temperature     *float64
	TopP            *float64
	MaxTokens       *int
	ReasoningEffort *string
}

func (o *OpenAI) Stream(messages []ChatMessage, model string, tools []ToolDef, onEvent func(StreamEvent)) error {
	base := o.APIBase
	if base == "" {
		base = "https://api.openai.com/v1"
	}

	body := map[string]any{
		"model":    model,
		"stream":   true,
		"messages": messages,
	}
	if o.Temperature != nil {
		body["temperature"] = *o.Temperature
	}
	if o.TopP != nil {
		body["top_p"] = *o.TopP
	}
	if o.MaxTokens != nil {
		body["max_tokens"] = *o.MaxTokens
	}
	if o.ReasoningEffort != nil && *o.ReasoningEffort != "" {
		body["reasoning_effort"] = *o.ReasoningEffort
	}
	if len(tools) > 0 {
		tdefs := make([]map[string]any, len(tools))
		for i, t := range tools {
			tdefs[i] = map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name,
					"description": t.Description,
					"parameters":  t.Parameters,
				},
			}
		}
		body["tools"] = tdefs
	}

	data, _ := json.Marshal(body)
	endpoint := "/chat/completions"
	if o.EndpointOverride != "" {
		endpoint = o.EndpointOverride
	}
	req, _ := http.NewRequest("POST", strings.TrimRight(base, "/")+endpoint, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("LLM API %d: %s", resp.StatusCode, string(b))
	}

	// SSE parsing with tool call accumulation and think tag handling
	toolAcc := map[int]*struct{ id, name, args string }{}
	inThinkTag := false
	thinkTagName := "think"

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 256*1024), 256*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		d := line[6:]
		if d == "[DONE]" {
			if len(toolAcc) > 0 {
				onEvent(StreamEvent{ToolCalls: buildToolCalls(toolAcc), FinishReason: "tool_calls"})
			} else {
				onEvent(StreamEvent{Done: true, FinishReason: "stop"})
			}
			return nil
		}

		var chunk struct {
			Choices []struct {
				Delta struct {
					Content          *string `json:"content"`
					ReasoningContent string  `json:"reasoning_content"`
					Reasoning        string  `json:"reasoning"`
					ToolCalls        []struct {
						Index    int    `json:"index"`
						ID       string `json:"id"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(d), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		c := chunk.Choices[0]
		delta := c.Delta

		// Accumulate tool calls
		for _, tc := range delta.ToolCalls {
			acc, ok := toolAcc[tc.Index]
			if !ok {
				acc = &struct{ id, name, args string }{}
				toolAcc[tc.Index] = acc
			}
			if tc.ID != "" {
				acc.id = tc.ID
			}
			if tc.Function.Name != "" {
				acc.name = tc.Function.Name
			}
			acc.args += tc.Function.Arguments
		}

		// Check finish reason
		if c.FinishReason != nil {
			fr := *c.FinishReason
			if fr == "tool_calls" || (fr == "stop" && len(toolAcc) > 0) {
				onEvent(StreamEvent{ToolCalls: buildToolCalls(toolAcc), FinishReason: "tool_calls"})
				return nil
			}
			if fr == "stop" {
				onEvent(StreamEvent{Done: true, FinishReason: "stop"})
				return nil
			}
		}

		// Extract content and reasoning
		content := ""
		if delta.Content != nil {
			content = *delta.Content
		}
		reasoning := delta.ReasoningContent
		if reasoning == "" {
			reasoning = delta.Reasoning
		}

		// Handle <think>/<thought> tags
		if !inThinkTag {
			if idx := strings.Index(content, "<think>"); idx >= 0 {
				inThinkTag = true
				thinkTagName = "think"
				content = content[idx+7:]
			} else if idx := strings.Index(content, "<thought>"); idx >= 0 {
				inThinkTag = true
				thinkTagName = "thought"
				content = content[idx+9:]
			}
		}
		if inThinkTag {
			closeTag := "</" + thinkTagName + ">"
			if idx := strings.Index(content, closeTag); idx >= 0 {
				reasoning = content[:idx]
				content = content[idx+len(closeTag):]
				inThinkTag = false
			} else {
				reasoning = content
				content = ""
			}
		}

		if content != "" || reasoning != "" {
			ev := StreamEvent{Delta: content}
			if reasoning != "" {
				ev.Reasoning = reasoning
			}
			onEvent(ev)
		}
	}
	return scanner.Err()
}

func buildToolCalls(acc map[int]*struct{ id, name, args string }) []ToolCallInfo {
	calls := make([]ToolCallInfo, 0, len(acc))
	for _, a := range acc {
		var args map[string]any
		json.Unmarshal([]byte(a.args), &args)
		if args == nil {
			args = map[string]any{}
		}
		calls = append(calls, ToolCallInfo{ID: a.id, Name: a.name, Arguments: args, RawArgs: a.args})
	}
	return calls
}
