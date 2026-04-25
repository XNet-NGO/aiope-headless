## 4. WebSocket Protocol

### Connection

```
ws://localhost:8090/ws
```

Single persistent connection per client. Multiplexed by conversation ID.

### Message Format

All WebSocket messages are JSON with a `type` field:

```json
{"type": "message_type", ...payload}
```

### Client → Server Messages

#### `chat.send` — Send a user message and start streaming
```json
{
  "type": "chat.send",
  "conversationId": "uuid",
  "content": "Explain quantum computing",
  "mode": "chat",
  "imagePaths": ""
}
```

If `conversationId` is empty, server creates a new conversation and sends `conversation.created`.

#### `chat.cancel` — Cancel an in-progress generation
```json
{
  "type": "chat.cancel",
  "conversationId": "uuid"
}
```

#### `chat.regenerate` — Regenerate the last assistant response
```json
{
  "type": "chat.regenerate",
  "conversationId": "uuid"
}
```

Server deletes the last assistant message (and any tool messages after the last user message), then re-runs generation.

---

### Server → Client Messages

#### `conversation.created` — New conversation was created
```json
{
  "type": "conversation.created",
  "conversation": {
    "id": "uuid",
    "title": "New Chat",
    "agentName": "default",
    "createdAt": 1745612400000,
    "updatedAt": 1745612400000
  }
}
```

#### `message.created` — A message was persisted (user or assistant)
```json
{
  "type": "message.created",
  "message": {
    "id": "msg-uuid",
    "conversationId": "uuid",
    "role": "user",
    "content": "Explain quantum computing",
    "timestamp": 1745612400000
  }
}
```

#### `stream.start` — Assistant response streaming begins
```json
{
  "type": "stream.start",
  "conversationId": "uuid",
  "messageId": "msg-uuid-2"
}
```

#### `stream.delta` — Incremental text chunk
```json
{
  "type": "stream.delta",
  "conversationId": "uuid",
  "messageId": "msg-uuid-2",
  "delta": "Quantum computing is"
}
```

Client appends `delta` to the current message content.

#### `stream.tool_call` — Agent is invoking a tool
```json
{
  "type": "stream.tool_call",
  "conversationId": "uuid",
  "messageId": "msg-uuid-2",
  "toolCall": {
    "id": "tc-uuid",
    "name": "search_web",
    "arguments": "{\"query\": \"quantum computing basics\"}"
  }
}
```

#### `stream.tool_result` — Tool execution completed
```json
{
  "type": "stream.tool_result",
  "conversationId": "uuid",
  "toolCallId": "tc-uuid",
  "result": "Search results: ...",
  "isError": false
}
```

#### `stream.reasoning` — Reasoning/thinking content (if model supports it)
```json
{
  "type": "stream.reasoning",
  "conversationId": "uuid",
  "messageId": "msg-uuid-2",
  "delta": "Let me think about this..."
}
```

#### `stream.end` — Streaming complete
```json
{
  "type": "stream.end",
  "conversationId": "uuid",
  "messageId": "msg-uuid-2",
  "finishReason": "stop",
  "usage": {
    "promptTokens": 150,
    "completionTokens": 320,
    "totalTokens": 470
  }
}
```

`finishReason`: `"stop"` | `"tool_use"` (internal, client won't see this — agent loop continues) | `"length"` | `"cancelled"`

#### `stream.error` — Error during generation
```json
{
  "type": "stream.error",
  "conversationId": "uuid",
  "error": "Provider returned 429: rate limited",
  "retryable": true
}
```

#### `conversation.updated` — Title or metadata changed
```json
{
  "type": "conversation.updated",
  "id": "uuid",
  "title": "Quantum Computing Discussion",
  "updatedAt": 1745612500000
}
```

Sent when auto-title generation completes (async, after first message).

#### `conversation.deleted` — Conversation was deleted
```json
{
  "type": "conversation.deleted",
  "id": "uuid"
}
```

---

### Streaming Lifecycle

```
Client                          Server
  │                               │
  │──── chat.send ───────────────>│
  │                               │ (persist user message)
  │<──── message.created ─────────│
  │                               │ (call LLM)
  │<──── stream.start ────────────│
  │<──── stream.delta ────────────│  ×N
  │<──── stream.delta ────────────│
  │                               │ (tool call needed?)
  │<──── stream.tool_call ────────│
  │                               │ (execute tool)
  │<──── stream.tool_result ──────│
  │                               │ (feed result back to LLM, continue)
  │<──── stream.delta ────────────│  ×N
  │<──── stream.end ──────────────│
  │                               │ (persist assistant message)
  │<──── message.created ─────────│
  │                               │ (async title generation)
  │<──── conversation.updated ────│
```

### Heartbeat

Server sends ping frames every 30s. Client must respond with pong. Connection closes after 60s without pong.
