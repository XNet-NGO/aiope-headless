## 5. LLM Backend Interface

### Provider Interface

```go
// Provider abstracts all LLM backends.
type Provider interface {
    // ID returns the provider profile ID.
    ID() string

    // SendMessages sends a complete request and returns the full response.
    // Used for title generation, summarization, and non-streaming calls.
    SendMessages(ctx context.Context, req Request) (*Response, error)

    // StreamResponse sends a request and streams back events.
    // The channel is closed when the response is complete or ctx is cancelled.
    StreamResponse(ctx context.Context, req Request) (<-chan StreamEvent, error)

    // ListModels fetches available models from the provider API.
    ListModels(ctx context.Context) ([]ModelDef, error)
}
```

### Request/Response Types

```go
type Request struct {
    Model          string
    Messages       []Message
    Tools          []ToolDef
    Temperature    *float64
    TopP           *float64
    TopK           *int
    MaxTokens      *int
    ReasoningEffort *string  // "auto", "low", "medium", "high"
    SystemPrompt   string
}

type Message struct {
    Role    string  // "system", "user", "assistant", "tool"
    Content string
    // For tool calls (assistant role)
    ToolCalls []ToolCall
    // For tool results (tool role)
    ToolCallID string
}

type ToolDef struct {
    Name        string
    Description string
    Parameters  json.RawMessage  // JSON Schema
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments string  // JSON string
}

type Response struct {
    Content      string
    ToolCalls    []ToolCall
    FinishReason string  // "stop", "tool_use", "length"
    Usage        Usage
}

type Usage struct {
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}

type StreamEvent struct {
    Type string  // "delta", "tool_call", "reasoning", "usage", "done", "error"
    // For "delta" and "reasoning":
    Delta string
    // For "tool_call":
    ToolCall *ToolCall
    // For "usage" and "done":
    Usage       *Usage
    FinishReason string
    // For "error":
    Error error
}
```

### Provider Implementations

#### OpenAI-Compatible (`openai.go`)
Covers: OpenAI, Ollama, OpenRouter, Groq, AIOPE Gateway, any OpenAI-compatible API.

- Endpoint: `{apiBase}/chat/completions`
- Auth: `Authorization: Bearer {apiKey}`
- Streaming: `"stream": true` with SSE (`data: {...}` lines)
- Tool calls: OpenAI function calling format
- Model list: `GET {apiBase}/models`

#### Anthropic (`anthropic.go`)
- Endpoint: `https://api.anthropic.com/v1/messages`
- Auth: `x-api-key: {apiKey}`, `anthropic-version: 2023-06-01`
- Streaming: `"stream": true` with SSE events (`message_start`, `content_block_delta`, `message_stop`)
- Tool calls: Anthropic native tool use format (content blocks with `type: "tool_use"`)
- Reasoning: `thinking` content blocks when extended thinking is enabled
- Model list: hardcoded (Anthropic has no list endpoint)

### Provider Resolution

```go
func NewProvider(profile ProviderProfile) (Provider, error) {
    switch profile.BuiltinID {
    case "anthropic":
        return NewAnthropicProvider(profile), nil
    default:
        // Everything else uses OpenAI-compatible API
        return NewOpenAIProvider(profile), nil
    }
}
```

The `builtinId` field determines which implementation to use. Only Anthropic needs a custom implementation; everything else (OpenAI, Ollama, Groq, OpenRouter, AIOPE Gateway, custom) uses the OpenAI-compatible provider with different `apiBase` and `apiKey`.

### Agent Loop

```go
func (a *Agent) Run(ctx context.Context, convID string, userContent string, mode AgentMode) error {
    // 1. Persist user message
    // 2. Build system prompt from settings_kv agent_ keys + mode prefix
    // 3. Load conversation history
    // 4. Resolve active provider + model config
    // 5. Build tool list (enabled built-in tools + enabled MCP tools)
    //    - If mode == PLAN: filter out write tools
    //    - If mode == BUILD: add autonomy system prefix
    // 6. Agent loop:
    //    a. Call provider.StreamResponse() with messages + tools
    //    b. Fan out StreamEvents to WebSocket hub
    //    c. If finish_reason == "tool_use":
    //       - Execute each tool call via tool.Executor
    //       - Append tool result messages
    //       - Loop back to (a)
    //    d. If finish_reason == "stop" or "length":
    //       - Persist assistant message
    //       - Break
    // 7. If first message: async generate title via separate LLM call
    // 8. If auto_compact enabled and tokens > 95% context: summarize
}
```

### Agent Modes (matching AIOPE2)

| Mode | System Prefix | Tool Filter |
|------|--------------|-------------|
| `chat` | (none) | All enabled tools |
| `plan` | "Analyze, explore, produce numbered plan. Do NOT execute changes." | Read-only tools only (disable: `run_sh`, `write_file`, `image_generate`, etc.) |
| `build` | "Execute autonomously. Do not ask for confirmation. Chain tools." | All enabled tools |

### System Prompt Assembly

Built from `settings_kv` entries with `agent_` prefix, in order:

```
[Identity]
{agent_identity_name_role}
{agent_identity_personality}
{agent_identity_tone}

[Values & Rules]
{agent_values_principles}
{agent_values_constraints}

[Preferences]
{agent_preferences_response_style}
{agent_preferences_formatting}

[Context]
{agent_context_user_info}
{agent_context_environment}
{agent_context_projects}

[Tools]
{agent_tools_tool_guidance}
{agent_tools_mcp_notes}

[Mode Prefix — prepended if plan/build]
```

### Context Compaction

Following OpenCode's pattern:
1. Track token usage per conversation (from provider usage responses)
2. When usage exceeds 95% of `modelConfig.contextTokens`:
   - Send all messages to a summarizer call with prompt: "Summarize this conversation concisely, preserving key decisions, code, and context."
   - Store summary as a system message
   - On next request, only send messages from the summary forward
3. Store summary reference in `settings_kv` as `compact_{conversationId}` → message ID
