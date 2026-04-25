## 2. Database Schema

SQLite schema — byte-compatible with AIOPE2 Room DB v4. Column names use camelCase to match Room's default mapping.

```sql
-- AIOPE-Headless schema (compatible with AIOPE2 Room DB v4)
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

-- ─── Conversations ───────────────────────────────────────────
CREATE TABLE IF NOT EXISTS conversations (
    id        TEXT PRIMARY KEY,
    title     TEXT NOT NULL DEFAULT 'New Chat',
    agentName TEXT NOT NULL DEFAULT 'default',
    createdAt INTEGER NOT NULL,  -- epoch millis
    updatedAt INTEGER NOT NULL   -- epoch millis
);

CREATE INDEX IF NOT EXISTS idx_conversations_updated ON conversations(updatedAt DESC);

-- ─── Messages ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS messages (
    id             TEXT PRIMARY KEY,
    conversationId TEXT NOT NULL,
    role           TEXT NOT NULL,       -- 'user', 'assistant', 'tool', 'system'
    content        TEXT NOT NULL,       -- plain text or JSON (tool calls/results)
    imagePaths     TEXT NOT NULL DEFAULT '',  -- comma-separated paths (AIOPE2 compat)
    timestamp      INTEGER NOT NULL,    -- epoch millis
    FOREIGN KEY (conversationId) REFERENCES conversations(id) ON DELETE CASCADE
);

CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversationId);

-- ─── Memories ────────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS memories (
    key       TEXT PRIMARY KEY,
    content   TEXT NOT NULL,
    category  TEXT NOT NULL DEFAULT 'general',  -- general, preference, learning, error
    createdAt INTEGER NOT NULL,
    updatedAt INTEGER NOT NULL
);

-- ─── Providers ───────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS providers (
    id        TEXT PRIMARY KEY,
    json      TEXT NOT NULL,       -- Full ProviderProfile JSON blob
    isActive  INTEGER NOT NULL DEFAULT 0,  -- Room stores Boolean as INTEGER
    updatedAt INTEGER NOT NULL
);

-- ─── Tool Toggles ────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS tool_toggles (
    toolId  TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL       -- 0 or 1
);

-- ─── MCP Servers ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS mcp_servers (
    id   TEXT PRIMARY KEY,
    json TEXT NOT NULL              -- Full McpServerConfig JSON blob
);

-- ─── Model Cache ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS model_cache (
    builtinId TEXT PRIMARY KEY,
    json      TEXT NOT NULL,        -- JSON array of ModelDef
    cachedAt  INTEGER NOT NULL      -- epoch millis, 24h TTL
);

-- ─── Settings KV ─────────────────────────────────────────────
CREATE TABLE IF NOT EXISTS settings_kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
```

### Room Compatibility Notes

- Room stores `Boolean` as `INTEGER` (0/1) — schema uses `INTEGER` for `isActive`, `enabled`
- Room uses camelCase column names by default — all columns match exactly
- Room DB file is `aiope2-chat.db` — headless uses same filename for direct sync
- `messages.content` stores plain text for user/assistant messages; for tool interactions, stores JSON matching AIOPE2's format
- No Room `room_master_table` — headless creates its own; Room will re-validate on next open
- `version = 4` — headless tracks this in a `_schema_version` setting in `settings_kv`

### ProviderProfile JSON Shape (stored in `providers.json`)

```json
{
  "id": "uuid-string",
  "builtinId": "openai",
  "label": "My OpenAI",
  "apiKey": "sk-...",
  "apiBase": "https://api.openai.com/v1",
  "selectedModelId": "gpt-4o",
  "isActive": true,
  "modelConfigs": {
    "gpt-4o": {
      "modelId": "gpt-4o",
      "endpointOverride": "",
      "toolsOverride": null,
      "visionOverride": null,
      "temperature": 0.6,
      "topP": null,
      "topK": null,
      "maxTokens": null,
      "reasoningEffort": null,
      "contextTokens": 128000,
      "autoCompact": false,
      "systemPromptOverride": null,
      "shellOutputLimit": 4000,
      "fetchLimit": 12000,
      "fileReadLimit": 50000
    }
  }
}
```

### McpServerConfig JSON Shape (stored in `mcp_servers.json`)

```json
{
  "id": "abc12345",
  "name": "My MCP Server",
  "url": "https://mcp.example.com/sse",
  "enabled": true,
  "transport": "sse",
  "headers": {"Authorization": "Bearer xxx"},
  "toolCount": 5,
  "status": "idle",
  "error": null
}
```

### Settings KV Keys

| Key | Description | Default |
|-----|-------------|---------|
| `agent_identity_name_role` | Agent identity | "You are AIOPE, a personal intelligent agent" |
| `agent_identity_personality` | Personality | "Competent, efficient, quietly confident." |
| `agent_identity_tone` | Tone | "Concise and professional." |
| `agent_values_principles` | Core values | "Privacy first, efficiency, autonomy" |
| `agent_values_constraints` | Constraints | "Confirm significant actions" |
| `agent_preferences_response_style` | Response style | "Markdown code blocks, tables, bullets" |
| `agent_preferences_formatting` | Formatting rules | "" |
| `agent_context_user_info` | User info | "" |
| `agent_context_environment` | Environment context | "" |
| `agent_context_projects` | Project context | "" |
| `agent_tools_tool_guidance` | Tool usage guidance | "" |
| `agent_tools_dynamic_ui` | Dynamic UI spec | (full aiope-ui spec) |
| `agent_tools_mcp_notes` | MCP notes | "" |
| `dynamic_ui_enabled` | Enable dynamic UI | "true" |
| `_schema_version` | DB schema version | "4" |
