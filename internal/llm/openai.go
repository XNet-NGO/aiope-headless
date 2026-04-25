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
	Role    string `json:"role"`
	Content string `json:"content"`
}

type StreamEvent struct {
	Delta      string // text chunk
	Done       bool
	FinishReason string
}

type Provider interface {
	Stream(messages []ChatMessage, model string, onEvent func(StreamEvent)) error
}

// OpenAI-compatible provider (works with OpenAI, Ollama, any compatible API)
type OpenAI struct {
	APIKey  string
	APIBase string
}

type oaiReq struct {
	Model    string        `json:"model"`
	Messages []ChatMessage `json:"messages"`
	Stream   bool          `json:"stream"`
}

func (o *OpenAI) Stream(messages []ChatMessage, model string, onEvent func(StreamEvent)) error {
	base := o.APIBase
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	body, _ := json.Marshal(oaiReq{Model: model, Messages: messages, Stream: true})
	req, _ := http.NewRequest("POST", base+"/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
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

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := line[6:]
		if data == "[DONE]" {
			onEvent(StreamEvent{Done: true, FinishReason: "stop"})
			return nil
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
				FinishReason *string `json:"finish_reason"`
			} `json:"choices"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
			continue
		}
		c := chunk.Choices[0]
		if c.Delta.Content != "" {
			onEvent(StreamEvent{Delta: c.Delta.Content})
		}
		if c.FinishReason != nil {
			onEvent(StreamEvent{Done: true, FinishReason: *c.FinishReason})
			return nil
		}
	}
	return scanner.Err()
}
