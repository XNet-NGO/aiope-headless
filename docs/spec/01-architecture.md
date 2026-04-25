# AIOPE-Headless Specification

## 1. Server Architecture

### Overview

Go monolith serving HTTP/WebSocket API. Thin clients (web SPA, CLI, TUI) connect over the network. Syncs bidirectionally with AIOPE2 Android app over ZeroTier.

### Package Layout

```
main.go                         → Entry point, flag parsing, server start
internal/
  server/
    server.go                   → HTTP router, middleware, WebSocket upgrader
    routes.go                   → Route registration
  config/
    config.go                   → Viper config (JSON file + env vars)
  db/
    db.go                       → SQLite open, pragmas, migrations
    schema.sql                  → Embedded schema (AIOPE2-compatible)
    queries.sql                 → sqlc query definitions
    models.go                   → sqlc-generated types
    queries.go                  → sqlc-generated query functions
  conversation/
    service.go                  → Conversation CRUD + pub/sub
  message/
    service.go                  → Message CRUD + pub/sub
    content.go                  → Content part types (text, tool_call, tool_result)
  memory/
    service.go                  → Memory CRUD + search
  provider/
    service.go                  → Provider profile CRUD, active switching
  tool/
    service.go                  → Tool toggle CRUD
    registry.go                 → Built-in tool registry
    executor.go                 → Tool dispatch + execution
  mcp/
    service.go                  → MCP server config CRUD
    client.go                   → MCP protocol client (SSE/HTTP transport)
  llm/
    provider.go                 → Provider interface (SendMessages, StreamResponse)
    openai.go                   → OpenAI-compatible provider (covers OpenAI, Ollama, custom)
    anthropic.go                → Anthropic native API
    agent.go                    → Agentic tool-use loop
    prompt.go                   → System prompt builder (from settings_kv agent_ keys)
    models.go                   → Model definitions, cost tracking
  settings/
    service.go                  → settings_kv CRUD, agent prompt sections
  sync/
    client.go                   → Pull from AIOPE2 Android sync API
    push.go                     → Push conversations back to AIOPE2
  pubsub/
    broker.go                   → Generic typed pub/sub broker
  ws/
    hub.go                      → WebSocket connection hub
    client.go                   → Per-connection reader/writer
    protocol.go                 → Message type definitions
web/
  index.html                    → SPA shell
  app.js                        → Client application
  style.css                     → Theme (AIOPE2 colors)
```

### Layering

```
┌─────────────────────────────────────┐
│  Clients (Web SPA, CLI, TUI)        │
├─────────────────────────────────────┤
│  HTTP REST API + WebSocket          │  ← server/
├─────────────────────────────────────┤
│  Services (conversation, message,   │  ← domain services
│  memory, provider, tool, mcp,       │
│  settings, sync)                    │
├─────────────────────────────────────┤
│  LLM Engine (agent loop, providers) │  ← llm/
├─────────────────────────────────────┤
│  SQLite (AIOPE2-compatible schema)  │  ← db/
└─────────────────────────────────────┘
```

### Key Design Decisions

- **Single binary** — embed web assets, schema, migrations via `embed.FS`
- **SQLite with WAL** — same schema as AIOPE2 Room DB for direct file-level sync
- **sqlc** for type-safe queries, **goose** for migrations (matching OpenCode pattern)
- **Pub/sub broker** — all state changes published as events, WebSocket hub subscribes and fans out to connected clients
- **No CGO** — use `github.com/ncruces/go-sqlite3` (pure Go SQLite)
- **Agent modes** — CHAT, PLAN, BUILD matching AIOPE2's AgentMode enum
- **Config** — `~/.aiope-headless/config.json` merged with env vars (`AIOPE_DB_PATH`, `AIOPE_PORT`, `AIOPE_SYNC_URL`)

### Default Config

```json
{
  "port": 8090,
  "db_path": "~/.aiope-headless/aiope2-chat.db",
  "sync_url": "",
  "auto_compact": true,
  "log_level": "info"
}
```
