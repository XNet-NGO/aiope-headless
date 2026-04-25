## 3. HTTP API Specification

Base URL: `http://localhost:8090/api`

All responses use `Content-Type: application/json`. Errors return:
```json
{"error": "description"}
```

Timestamps are Unix epoch milliseconds (int64).

---

### 3.1 Conversations

#### `GET /api/conversations`
List all conversations, newest first.

**Query params:** `?limit=50&offset=0`

**Response 200:**
```json
{
  "conversations": [
    {
      "id": "uuid",
      "title": "New Chat",
      "agentName": "default",
      "createdAt": 1745612400000,
      "updatedAt": 1745612400000,
      "messageCount": 12
    }
  ],
  "total": 42
}
```

#### `POST /api/conversations`
Create a new conversation.

**Request:**
```json
{
  "title": "Optional title",
  "agentName": "default"
}
```

**Response 201:**
```json
{
  "id": "uuid",
  "title": "New Chat",
  "agentName": "default",
  "createdAt": 1745612400000,
  "updatedAt": 1745612400000
}
```

#### `GET /api/conversations/:id`
Get conversation with messages.

**Response 200:**
```json
{
  "id": "uuid",
  "title": "My Chat",
  "agentName": "default",
  "createdAt": 1745612400000,
  "updatedAt": 1745612400000,
  "messages": [
    {
      "id": "msg-uuid",
      "role": "user",
      "content": "Hello",
      "imagePaths": "",
      "timestamp": 1745612400000
    },
    {
      "id": "msg-uuid-2",
      "role": "assistant",
      "content": "Hi! How can I help?",
      "imagePaths": "",
      "timestamp": 1745612401000
    }
  ]
}
```

#### `PATCH /api/conversations/:id`
Update conversation title.

**Request:**
```json
{"title": "Renamed Chat"}
```

**Response 200:**
```json
{"id": "uuid", "title": "Renamed Chat", "updatedAt": 1745612500000}
```

#### `DELETE /api/conversations/:id`
Delete conversation and all its messages (CASCADE).

**Response 204:** (no body)

---

### 3.2 Chat (Send Message + Stream Response)

#### `POST /api/conversations/:id/messages`
Send a user message and get an AI response. For streaming, use WebSocket (section 4). This endpoint returns the final response synchronously.

**Request:**
```json
{
  "content": "What is the capital of France?",
  "mode": "chat",
  "imagePaths": ""
}
```

`mode`: `"chat"` | `"plan"` | `"build"` — maps to AIOPE2 AgentMode.

**Response 200:**
```json
{
  "userMessage": {
    "id": "msg-uuid",
    "role": "user",
    "content": "What is the capital of France?",
    "timestamp": 1745612400000
  },
  "assistantMessage": {
    "id": "msg-uuid-2",
    "role": "assistant",
    "content": "The capital of France is Paris.",
    "timestamp": 1745612401000
  },
  "toolCalls": []
}
```

For streaming responses, connect via WebSocket (see section 4).

#### `DELETE /api/conversations/:id/messages?after=1745612400000`
Delete messages after a timestamp (for regeneration).

**Response 204:** (no body)

---

### 3.3 Sync (AIOPE2 ↔ Headless)

#### `POST /api/sync/pull`
Pull conversations from AIOPE2 Android app.

**Request:**
```json
{
  "syncUrl": "http://10.121.21.2:8080",
  "conversationIds": ["uuid1", "uuid2"]
}
```

If `conversationIds` is empty/omitted, pulls all.

**Response 200:**
```json
{
  "imported": 3,
  "skipped": 1,
  "errors": []
}
```

#### `POST /api/sync/push`
Push conversations back to AIOPE2.

**Request:**
```json
{
  "syncUrl": "http://10.121.21.2:8080",
  "conversationIds": ["uuid1"]
}
```

**Response 200:**
```json
{
  "pushed": 1,
  "errors": []
}
```

#### `GET /api/sync/status`
Get sync endpoint availability.

**Request query:** `?syncUrl=http://10.121.21.2:8080`

**Response 200:**
```json
{
  "reachable": true,
  "conversations": 42,
  "lastSync": 1745612400000
}
```

#### `GET /api/sync/diff`
Compare local vs remote conversations.

**Request query:** `?syncUrl=http://10.121.21.2:8080`

**Response 200:**
```json
{
  "onlyLocal": ["uuid1"],
  "onlyRemote": ["uuid2", "uuid3"],
  "bothModified": [
    {"id": "uuid4", "localUpdated": 1745612400000, "remoteUpdated": 1745612500000}
  ],
  "inSync": ["uuid5"]
}
```
