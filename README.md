# aiope-headless

AIOPE Headless — terminal AI assistant with web UI.

## Overview

Go backend with a vanilla JS frontend (single `index.html`). Uses SQLite for persistence and WebSocket for streaming responses.

## Features

- Multi-provider LLM support
- Tool calling: shell, file ops, web search/fetch, image analysis
- MCP server integration
- Subagents
- Auto-compact and memory system
- Remote server management

## Build

```sh
go build -o aiope-headless .
```

## Run

```sh
AIOPE_BIND=10.121.21.25 ./aiope-headless
```

Accessible over XNet at `http://10.121.21.25:8090`.

## License

Apache 2.0
