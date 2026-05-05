package llm

import (
	"context"
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
	ToolCtx  *ToolContext
	OnEvent  func(StreamEvent)
	Ctx      context.Context
}

func (o *Orchestrator) Run(messages []ChatMessage) (string, error) {
	raw := make([]ChatMessage, len(messages))
	copy(raw, messages)

	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}

	var fullContent strings.Builder
	var fullReasoning strings.Builder
	var lastToolKey string
	var sameToolCount int

	for round := 0; round < 140; round++ {
		select {
		case <-ctx.Done():
			o.OnEvent(StreamEvent{Done: true, FinishReason: "cancelled"})
			return fullContent.String(), nil
		default:
		}
		// Trim older tool results (keep last 3 full, truncate older to 500 chars)
		trimToolResults(raw)

		var roundContent strings.Builder
		var toolCalls []ToolCallInfo
		var streamErr error

		err := o.Provider.Stream(ctx, raw, o.Model, o.Tools, func(ev StreamEvent) {
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

		// Detect tool call loops (same tool+args 3x in a row = bail)
		toolKey := fmt.Sprintf("%s:%s", toolCalls[0].Name, toolCalls[0].RawArgs)
		if toolKey == lastToolKey {
			sameToolCount++
			if sameToolCount >= 3 {
				log.Printf("orchestrator: breaking loop on %s after %d repeats", toolCalls[0].Name, sameToolCount)
				o.OnEvent(StreamEvent{Done: true, FinishReason: "tool_loop"})
				return fullContent.String(), nil
			}
		} else {
			lastToolKey = toolKey
			sameToolCount = 1
		}

		// Execute tools (parallel if all safe, else sequential)
		results := executeCalls(toolCalls, o.ToolCtx)

		// Emit results to client
		for _, r := range results {
			if r.IsErr || len(r.Result) < 50 {
				log.Printf("tool result: %s err=%v content=%q", r.Name, r.IsErr, r.Result)
			} else {
				log.Printf("tool result: %s err=%v len=%d", r.Name, r.IsErr, len(r.Result))
			}
		}
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

		names := make([]string, len(toolCalls))
		for i, tc := range toolCalls {
			names[i] = tc.Name
			log.Printf("tool call: %s args=%s", tc.Name, tc.RawArgs)
		}
		log.Printf("orchestrator: round %d, tools=%v, continuing", round+1, names)
	}

	o.OnEvent(StreamEvent{Done: true, FinishReason: "max_rounds"})
	return fullContent.String(), nil
}

func executeCalls(calls []ToolCallInfo, ctx *ToolContext) []ToolResultInfo {
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
				out, err := ExecuteTool(c.Name, c.Arguments, ctx)
				r := ToolResultInfo{ID: c.ID, Name: c.Name, Result: out}
				if err != nil {
					r.Result = "Error: " + err.Error()
					r.IsErr = true
				}
				if r.Result == "" {
					r.Result = "(empty)"
				}
				results[i] = r
			}(i, c)
		}
		wg.Wait()
		return results
	}

	results := make([]ToolResultInfo, len(calls))
	for i, c := range calls {
		out, err := ExecuteTool(c.Name, c.Arguments, ctx)
		r := ToolResultInfo{ID: c.ID, Name: c.Name, Result: out}
		if err != nil {
			r.Result = "Error: " + err.Error()
			r.IsErr = true
		}
		if r.Result == "" {
			r.Result = "(empty)"
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

// EstimateTokens approximates BPE token count.
// Counts words (split on whitespace/punctuation) and adds overhead for subword splits.
// ~1.3 tokens per word for English, plus 1 token per standalone punctuation/number group.
func EstimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	tokens := 0
	inWord := false
	wordLen := 0
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' || c == '\n' || c == '\t' || c == '\r' {
			if inWord {
				// Short words ~1 token, longer words get split into subwords
				if wordLen <= 4 {
					tokens++
				} else {
					tokens += (wordLen + 3) / 4
				}
				inWord = false
				wordLen = 0
			}
		} else {
			if !inWord {
				inWord = true
				wordLen = 0
			}
			wordLen++
			// Punctuation/special chars are often their own token
			if c == '.' || c == ',' || c == '!' || c == '?' || c == ':' || c == ';' || c == '{' || c == '}' || c == '[' || c == ']' || c == '(' || c == ')' || c == '"' || c == '\'' {
				if wordLen > 1 {
					tokens += (wordLen - 1 + 3) / 4
				}
				tokens++
				inWord = false
				wordLen = 0
			}
		}
	}
	if inWord {
		if wordLen <= 4 {
			tokens++
		} else {
			tokens += (wordLen + 3) / 4
		}
	}
	// Add ~3% overhead for BPE special tokens
	return tokens + tokens/30 + 3
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
