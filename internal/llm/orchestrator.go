package llm

import (
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"sync"
)

type Orchestrator struct {
	Provider Provider
	Model    string
	Tools    []ToolDef
	OnEvent  func(StreamEvent)
}

func (o *Orchestrator) Run(messages []ChatMessage) (string, error) {
	raw := make([]ChatMessage, len(messages))
	copy(raw, messages)

	var fullContent strings.Builder
	var fullReasoning strings.Builder

	for round := 0; round < 50; round++ {
		// Trim older tool results (keep last 3 full, truncate older to 500 chars)
		trimToolResults(raw)

		var roundContent strings.Builder
		var toolCalls []ToolCallInfo
		var streamErr error

		err := o.Provider.Stream(raw, o.Model, o.Tools, func(ev StreamEvent) {
			if ev.Delta != "" {
				roundContent.WriteString(ev.Delta)
				fullContent.WriteString(ev.Delta)
				o.OnEvent(StreamEvent{Delta: ev.Delta})
			}
			if ev.Reasoning != "" {
				fullReasoning.WriteString(ev.Reasoning)
				o.OnEvent(StreamEvent{Reasoning: ev.Reasoning})
			}
			if ev.Error != "" {
				streamErr = fmt.Errorf("%s", ev.Error)
				o.OnEvent(ev)
			}
			if len(ev.ToolCalls) > 0 {
				toolCalls = ev.ToolCalls
			}
			if ev.Done {
				o.OnEvent(ev)
			}
		})

		if err != nil {
			return fullContent.String(), err
		}
		if streamErr != nil {
			return fullContent.String(), streamErr
		}

		// No tool calls — done
		if len(toolCalls) == 0 {
			return fullContent.String(), nil
		}

		// Emit tool calls to client
		o.OnEvent(StreamEvent{ToolCalls: toolCalls})

		// Build assistant message with tool_calls
		tcJSON := make([]any, len(toolCalls))
		for i, tc := range toolCalls {
			tcJSON[i] = map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": tc.RawArgs,
				},
			}
		}
		raw = append(raw, ChatMessage{Role: "assistant", ToolCalls: tcJSON})

		// Execute tools (parallel if all safe, else sequential)
		results := executeCalls(toolCalls)

		// Emit results to client
		o.OnEvent(StreamEvent{ToolResults: results})

		// Append tool results to messages
		for _, r := range results {
			content := r.Result
			if len(content) > 16000 {
				content = content[:16000] + "\n...(truncated)"
			}
			raw = append(raw, ChatMessage{
				Role:       "tool",
				Content:    content,
				ToolCallID: r.ID,
			})
		}

		log.Printf("orchestrator: round %d, %d tool calls, continuing", round+1, len(toolCalls))
	}

	o.OnEvent(StreamEvent{Done: true, FinishReason: "max_rounds"})
	return fullContent.String(), nil
}

func executeCalls(calls []ToolCallInfo) []ToolResultInfo {
	allSafe := true
	for _, c := range calls {
		if !ParallelSafe[c.Name] {
			allSafe = false
			break
		}
	}

	if len(calls) > 1 && allSafe {
		results := make([]ToolResultInfo, len(calls))
		var wg sync.WaitGroup
		for i, c := range calls {
			wg.Add(1)
			go func(i int, c ToolCallInfo) {
				defer wg.Done()
				out, err := ExecuteTool(c.Name, c.Arguments)
				r := ToolResultInfo{ID: c.ID, Name: c.Name, Result: out}
				if err != nil {
					r.Result = "Error: " + err.Error()
					r.IsErr = true
				}
				results[i] = r
			}(i, c)
		}
		wg.Wait()
		return results
	}

	results := make([]ToolResultInfo, len(calls))
	for i, c := range calls {
		out, err := ExecuteTool(c.Name, c.Arguments)
		r := ToolResultInfo{ID: c.ID, Name: c.Name, Result: out}
		if err != nil {
			r.Result = "Error: " + err.Error()
			r.IsErr = true
		}
		results[i] = r
	}
	return results
}

func trimToolResults(msgs []ChatMessage) {
	var toolIdxs []int
	for i, m := range msgs {
		if m.Role == "tool" {
			toolIdxs = append(toolIdxs, i)
		}
	}
	if len(toolIdxs) <= 3 {
		return
	}
	for _, i := range toolIdxs[:len(toolIdxs)-3] {
		if s, ok := msgs[i].Content.(string); ok && len(s) > 500 {
			msgs[i].Content = s[:500] + "...(truncated)"
		}
	}
}

// MarshalJSON for ChatMessage to handle nil content for tool_calls messages
func (m ChatMessage) MarshalJSON() ([]byte, error) {
	type Alias ChatMessage
	if m.Role == "assistant" && len(m.ToolCalls) > 0 && m.Content == nil {
		return json.Marshal(struct {
			Role      string `json:"role"`
			Content   any    `json:"content"`
			ToolCalls []any  `json:"tool_calls"`
		}{m.Role, nil, m.ToolCalls})
	}
	return json.Marshal(struct{ Alias }{Alias(m)})
}
