# AIOPE-Headless — Design Specification

**Version:** 1.0  
**Date:** 2026-04-25  
**Status:** Draft  

## Summary

AIOPE-Headless is a Go server that manages AI conversations with thin client interfaces (web SPA, CLI, TUI). It shares a SQLite database schema with the AIOPE2 Android app, enabling bidirectional sync over ZeroTier networks.

## Specification Documents

| # | Document | Description |
|---|----------|-------------|
| 1 | [01-architecture.md](01-architecture.md) | Server structure, package layout, layering, config |
| 2 | [02-database.md](02-database.md) | SQLite schema (AIOPE2 Room DB compatible), JSON shapes, settings keys |
| 3 | [03-api-conversations-chat-sync.md](03-api-conversations-chat-sync.md) | REST API: conversations, chat, sync endpoints |
| 4 | [04-api-providers-tools-memories-settings.md](04-api-providers-tools-memories-settings.md) | REST API: providers, tools/MCP, memories, settings |
| 5 | [05-websocket-protocol.md](05-websocket-protocol.md) | WebSocket streaming protocol, message types, lifecycle |
| 6 | [06-llm-backend.md](06-llm-backend.md) | Provider interface, OpenAI/Anthropic implementations, agent loop |
| 7 | [07-web-client.md](07-web-client.md) | SPA design, AIOPE2 theme colors, component styling |

## API Endpoint Summary

| Method | Path | Description |
|--------|------|-------------|
| GET | `/api/conversations` | List conversations |
| POST | `/api/conversations` | Create conversation |
| GET | `/api/conversations/:id` | Get conversation + messages |
| PATCH | `/api/conversations/:id` | Update title |
| DELETE | `/api/conversations/:id` | Delete conversation |
| POST | `/api/conversations/:id/messages` | Send message (sync response) |
| DELETE | `/api/conversations/:id/messages?after=` | Delete messages after timestamp |
| POST | `/api/sync/pull` | Pull from AIOPE2 |
| POST | `/api/sync/push` | Push to AIOPE2 |
| GET | `/api/sync/status` | Check sync endpoint |
| GET | `/api/sync/diff` | Compare local vs remote |
| GET | `/api/providers` | List providers |
| POST | `/api/providers` | Create provider |
| GET | `/api/providers/:id` | Get provider detail |
| PUT | `/api/providers/:id` | Update provider |
| DELETE | `/api/providers/:id` | Delete provider |
| POST | `/api/providers/:id/activate` | Set active provider |
| GET | `/api/providers/:id/models` | List models |
| GET | `/api/tools` | List tools |
| PUT | `/api/tools/:toolId/toggle` | Toggle tool |
| GET | `/api/mcp` | List MCP servers |
| POST | `/api/mcp` | Add MCP server |
| PUT | `/api/mcp/:id` | Update MCP server |
| DELETE | `/api/mcp/:id` | Delete MCP server |
| POST | `/api/mcp/:id/connect` | Connect to MCP server |
| POST | `/api/mcp/import` | Import MCP config JSON |
| GET | `/api/memories` | List memories |
| GET | `/api/memories/search?q=` | Search memories |
| PUT | `/api/memories/:key` | Upsert memory |
| DELETE | `/api/memories/:key` | Delete memory |
| GET | `/api/settings` | Get all settings |
| GET | `/api/settings/:key` | Get setting |
| PUT | `/api/settings/:key` | Set setting |
| PUT | `/api/settings` | Batch update settings |
| GET | `/api/settings/agent` | Get agent prompt config |
| WS | `/ws` | WebSocket streaming |

## Key Design Principles

1. **AIOPE2 compatibility** — Same SQLite schema, same JSON shapes, same agent modes
2. **Single binary** — Go binary embeds web assets, schema, migrations
3. **Thin clients** — Server owns all state; clients are pure UI
4. **Streaming first** — WebSocket for real-time chat; REST for CRUD
5. **Provider agnostic** — OpenAI-compatible API covers most providers; Anthropic gets native support
6. **Sync, not replicate** — Pull/push specific conversations between headless and AIOPE2 Android
