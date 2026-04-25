### 3.4 Providers

#### `GET /api/providers`
List all provider profiles.

**Response 200:**
```json
{
  "providers": [
    {
      "id": "uuid",
      "builtinId": "openai",
      "label": "My OpenAI",
      "apiBase": "https://api.openai.com/v1",
      "selectedModelId": "gpt-4o",
      "isActive": true,
      "updatedAt": 1745612400000,
      "hasApiKey": true
    }
  ]
}
```

Note: `apiKey` is never returned in list responses. `hasApiKey` indicates if one is set.

#### `POST /api/providers`
Create a provider profile.

**Request:**
```json
{
  "builtinId": "openai",
  "label": "My OpenAI",
  "apiKey": "sk-...",
  "apiBase": "https://api.openai.com/v1",
  "selectedModelId": "gpt-4o",
  "modelConfigs": {}
}
```

**Response 201:**
```json
{
  "id": "uuid",
  "builtinId": "openai",
  "label": "My OpenAI",
  "isActive": false,
  "updatedAt": 1745612400000
}
```

#### `GET /api/providers/:id`
Get full provider profile (includes apiKey masked as `"sk-...xxxx"`).

**Response 200:**
```json
{
  "id": "uuid",
  "builtinId": "openai",
  "label": "My OpenAI",
  "apiKey": "sk-...B4xQ",
  "apiBase": "https://api.openai.com/v1",
  "selectedModelId": "gpt-4o",
  "isActive": true,
  "modelConfigs": {
    "gpt-4o": {
      "modelId": "gpt-4o",
      "temperature": 0.6,
      "contextTokens": 128000,
      "autoCompact": false
    }
  },
  "updatedAt": 1745612400000
}
```

#### `PUT /api/providers/:id`
Update a provider profile.

**Request:** (partial update — only include fields to change)
```json
{
  "label": "Renamed",
  "apiKey": "sk-new-key",
  "selectedModelId": "gpt-4o-mini",
  "modelConfigs": {
    "gpt-4o-mini": {
      "modelId": "gpt-4o-mini",
      "temperature": 0.3,
      "maxTokens": 4096
    }
  }
}
```

**Response 200:**
```json
{"id": "uuid", "updatedAt": 1745612500000}
```

#### `DELETE /api/providers/:id`
Delete a provider profile.

**Response 204:** (no body)

#### `POST /api/providers/:id/activate`
Set this provider as the active one (deactivates all others).

**Response 200:**
```json
{"id": "uuid", "isActive": true}
```

#### `GET /api/providers/:id/models`
List available models for a provider. Uses model_cache (24h TTL) or fetches from API.

**Response 200:**
```json
{
  "models": [
    {
      "id": "gpt-4o",
      "displayName": "GPT-4o",
      "contextWindow": 128000,
      "supportsTools": true,
      "supportsVision": true,
      "supportsAudio": false,
      "supportsVideo": false,
      "supportsReasoning": false,
      "maxOutput": 16384
    }
  ],
  "cached": true,
  "cachedAt": 1745612400000
}
```

---

### 3.5 Tools & MCP

#### `GET /api/tools`
List all available tools with their enabled/disabled state.

**Response 200:**
```json
{
  "tools": [
    {
      "id": "search_web",
      "name": "search_web",
      "description": "Search the web for current information",
      "enabled": true,
      "source": "builtin"
    },
    {
      "id": "myserver_query",
      "name": "query",
      "description": "Query the database",
      "enabled": true,
      "source": "mcp:myserver"
    }
  ]
}
```

#### `PUT /api/tools/:toolId/toggle`
Enable or disable a tool.

**Request:**
```json
{"enabled": false}
```

**Response 200:**
```json
{"toolId": "search_web", "enabled": false}
```

#### `GET /api/mcp`
List MCP server configurations.

**Response 200:**
```json
{
  "servers": [
    {
      "id": "abc12345",
      "name": "My MCP Server",
      "url": "https://mcp.example.com/sse",
      "transport": "sse",
      "headers": {},
      "enabled": true,
      "toolCount": 5,
      "status": "connected",
      "error": null
    }
  ]
}
```

#### `POST /api/mcp`
Add an MCP server.

**Request:**
```json
{
  "name": "My MCP Server",
  "url": "https://mcp.example.com/sse",
  "transport": "sse",
  "headers": {"Authorization": "Bearer xxx"},
  "enabled": true
}
```

**Response 201:**
```json
{"id": "abc12345", "name": "My MCP Server", "status": "idle"}
```

#### `PUT /api/mcp/:id`
Update an MCP server config.

**Request:**
```json
{"name": "Renamed", "url": "https://new-url.com/sse"}
```

**Response 200:**
```json
{"id": "abc12345", "updatedAt": 1745612500000}
```

#### `DELETE /api/mcp/:id`
Remove an MCP server.

**Response 204:** (no body)

#### `POST /api/mcp/:id/connect`
Connect to an MCP server (initialize + list tools).

**Response 200:**
```json
{
  "id": "abc12345",
  "status": "connected",
  "tools": [
    {"name": "query", "description": "Query the database", "parameters": {"type": "object", "properties": {"sql": {"type": "string"}}}}
  ]
}
```

#### `POST /api/mcp/import`
Import MCP servers from JSON (AIOPE2 format).

**Request:**
```json
{
  "mcpServers": {
    "myserver": {
      "type": "sse",
      "baseUrl": "https://mcp.example.com/sse",
      "headers": {}
    }
  }
}
```

**Response 200:**
```json
{"imported": 1}
```

---

### 3.6 Memories

#### `GET /api/memories`
List all memories.

**Query params:** `?category=preference`

**Response 200:**
```json
{
  "memories": [
    {
      "key": "user_name",
      "content": "Alex",
      "category": "preference",
      "createdAt": 1745612400000,
      "updatedAt": 1745612400000
    }
  ]
}
```

#### `GET /api/memories/search?q=python`
Search memories by key or content.

**Response 200:**
```json
{
  "memories": [
    {
      "key": "preferred_language",
      "content": "Python for scripts, Go for servers",
      "category": "preference",
      "createdAt": 1745612400000,
      "updatedAt": 1745612400000
    }
  ]
}
```

#### `PUT /api/memories/:key`
Create or update a memory.

**Request:**
```json
{
  "content": "Prefers dark mode",
  "category": "preference"
}
```

**Response 200:**
```json
{"key": "ui_preference", "updatedAt": 1745612500000}
```

#### `DELETE /api/memories/:key`
Delete a memory.

**Response 204:** (no body)

---

### 3.7 Settings

#### `GET /api/settings`
Get all settings.

**Query params:** `?prefix=agent_identity_` (optional, filter by prefix)

**Response 200:**
```json
{
  "settings": {
    "agent_identity_name_role": "You are AIOPE, a personal intelligent agent",
    "agent_identity_personality": "Competent, efficient, quietly confident.",
    "agent_identity_tone": "Concise and professional.",
    "dynamic_ui_enabled": "true"
  }
}
```

#### `GET /api/settings/:key`
Get a single setting.

**Response 200:**
```json
{"key": "agent_identity_tone", "value": "Concise and professional."}
```

**Response 404:**
```json
{"error": "setting not found"}
```

#### `PUT /api/settings/:key`
Set a setting value.

**Request:**
```json
{"value": "Friendly and casual."}
```

**Response 200:**
```json
{"key": "agent_identity_tone", "value": "Friendly and casual."}
```

#### `PUT /api/settings`
Batch update settings.

**Request:**
```json
{
  "settings": {
    "agent_identity_tone": "Friendly and casual.",
    "agent_identity_personality": "Warm and helpful."
  }
}
```

**Response 200:**
```json
{"updated": 2}
```

#### `GET /api/settings/agent`
Convenience endpoint: returns the full agent prompt configuration grouped by section.

**Response 200:**
```json
{
  "identity": {
    "name_role": "You are AIOPE, a personal intelligent agent",
    "personality": "Competent, efficient, quietly confident.",
    "tone": "Concise and professional."
  },
  "values": {
    "principles": "Privacy first, efficiency, autonomy",
    "constraints": "Confirm significant actions"
  },
  "preferences": {
    "response_style": "Markdown code blocks, tables, bullets",
    "formatting": ""
  },
  "context": {
    "user_info": "",
    "environment": "",
    "projects": ""
  },
  "tools": {
    "tool_guidance": "",
    "dynamic_ui": "...",
    "mcp_notes": ""
  }
}
```
