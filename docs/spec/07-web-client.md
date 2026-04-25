## 6. Web Client Design

### Overview

Single-page app served from the Go binary via `embed.FS`. No build step — vanilla HTML/CSS/JS (or a single bundled file). The web client is a thin layer over the REST API + WebSocket.

### File Structure

```
web/
  index.html      → SPA shell, all markup
  app.js          → Client logic, WebSocket, API calls, rendering
  style.css       → Theme, layout, components
  favicon.svg     → AIOPE hexagon icon
```

### Page Layout

```
┌──────────────────────────────────────────────────┐
│  ┌─ Sidebar (260px) ─┐  ┌─ Main ──────────────┐ │
│  │ [+ New Chat]       │  │ ┌─ Header ────────┐ │ │
│  │                    │  │ │ Title  [⚙] [M]  │ │ │
│  │ Conversation List  │  │ └─────────────────┘ │ │
│  │  • Chat 1          │  │                     │ │
│  │  • Chat 2 (active) │  │ ┌─ Messages ──────┐ │ │
│  │  • Chat 3          │  │ │ User bubble      │ │ │
│  │                    │  │ │ Assistant bubble  │ │ │
│  │                    │  │ │ Tool call card    │ │ │
│  │                    │  │ │ Assistant bubble  │ │ │
│  │                    │  │ │ ...               │ │ │
│  │ ──────────────     │  │ └─────────────────┘ │ │
│  │ [Sync] [Settings]  │  │                     │ │
│  │                    │  │ ┌─ Input ──────────┐ │ │
│  └────────────────────┘  │ │ [CHAT|PLAN|BUILD]│ │ │
│                          │ │ [textarea    ][▶]│ │ │
│                          │ └─────────────────┘ │ │
│                          └─────────────────────┘ │
└──────────────────────────────────────────────────┘
```

### AIOPE2 Color Scheme

Extracted from `core-designsystem/theme/Color.kt` and component files:

#### Dark Theme (default)

```css
:root {
  /* Primary: electric cyan */
  --primary:             #00E5FF;
  --primary-container:   #003D42;

  /* Secondary: warm amber */
  --secondary:           #FFB300;
  --secondary-container: #3D2E00;

  /* Backgrounds: pure black */
  --bg:                  #000000;
  --surface:             #000000;
  --surface-variant:     #0A0A0A;

  /* Cards and elevated surfaces */
  --card-bg:             #1A1A1A;
  --card-elevated:       #222222;

  /* Text */
  --text-primary:        #FFFFFF;
  --text-secondary:      #9E9E9E;

  /* Outlines */
  --outline:             #2A2A2A;
  --divider:             #333333;

  /* Status */
  --error:               #FF1744;
  --success:             #4CAF50;

  /* Code */
  --code-bg:             #111111;
  --code-text:           #E0E0E0;
  --inline-code-text:    #FFB300;
  --inline-code-bg:      #1A1A1A;

  /* Bubbles */
  --user-bubble:         #003D42;
  --assistant-bubble:    #0A0A0A;

  /* Terminal panel */
  --terminal-bg:         #0F1729;
  --terminal-text:       #F8FAFC;

  /* Misc */
  --stop-button:         #FF1744;
  --subagent-bg:         #111111;
  --subagent-text:       #999999;
}
```

#### Light Theme

```css
:root.light {
  --primary:             #00E5FF;
  --primary-container:   #B2EBF2;
  --secondary:           #8D6E00;
  --secondary-container: #FFE082;
  --bg:                  #FAFAFA;
  --surface:             #FAFAFA;
  --surface-variant:     #E8E8EC;
  --card-bg:             #FFFFFF;
  --card-elevated:       #F5F5F5;
  --text-primary:        #1A1A1A;
  --text-secondary:      #646464;
  --outline:             #D0D0D0;
  --divider:             #E0E0E0;
  --error:               #FF1744;
  --success:             #4CAF50;
  --code-bg:             #F0F0F0;
  --code-text:           #1A1A1A;
  --inline-code-text:    #E65100;
  --inline-code-bg:      #EEEEEE;
  --user-bubble:         #B2EBF2;
  --assistant-bubble:    #E8E8EC;
}
```

### Component Styling

```css
body {
  font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
  background: var(--bg);
  color: var(--text-primary);
  margin: 0;
}

/* Sidebar */
.sidebar {
  width: 260px;
  background: var(--surface-variant);
  border-right: 1px solid var(--outline);
}

/* Conversation list item */
.conv-item {
  padding: 10px 14px;
  border-radius: 8px;
  cursor: pointer;
}
.conv-item:hover { background: var(--card-bg); }
.conv-item.active { background: var(--primary-container); color: var(--primary); }

/* Message bubbles */
.msg-user {
  background: var(--user-bubble);
  border-radius: 12px 12px 2px 12px;
  padding: 10px 14px;
  max-width: 80%;
  align-self: flex-end;
}
.msg-assistant {
  background: var(--assistant-bubble);
  border-radius: 12px 12px 12px 2px;
  padding: 10px 14px;
  max-width: 80%;
}

/* Tool call card */
.tool-card {
  background: var(--card-bg);
  border: 1px solid var(--outline);
  border-radius: 8px;
  padding: 8px 12px;
  font-size: 13px;
}
.tool-card .tool-name { color: var(--primary); font-weight: 600; }
.tool-card .tool-status { color: var(--text-secondary); }

/* Code blocks */
pre {
  background: var(--code-bg);
  color: var(--code-text);
  border-radius: 8px;
  padding: 12px;
  overflow-x: auto;
}
code {
  background: var(--inline-code-bg);
  color: var(--inline-code-text);
  padding: 2px 6px;
  border-radius: 4px;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
}

/* Input area */
.input-area {
  background: var(--surface-variant);
  border-top: 1px solid var(--outline);
  padding: 12px;
}
.input-area textarea {
  background: var(--card-bg);
  color: var(--text-primary);
  border: 1px solid var(--outline);
  border-radius: 12px;
  padding: 10px 14px;
  resize: none;
  width: 100%;
  font-size: 15px;
}
.input-area textarea:focus {
  border-color: var(--primary);
  outline: none;
}

/* Mode pills (CHAT / PLAN / BUILD) */
.mode-pills {
  display: flex;
  gap: 4px;
  margin-bottom: 8px;
}
.mode-pill {
  padding: 4px 12px;
  border-radius: 16px;
  font-size: 12px;
  font-weight: 600;
  cursor: pointer;
  background: var(--card-bg);
  color: var(--text-secondary);
  border: 1px solid var(--outline);
}
.mode-pill.active {
  background: var(--primary-container);
  color: var(--primary);
  border-color: var(--primary);
}

/* Send / Stop button */
.send-btn {
  background: var(--primary);
  color: #000;
  border: none;
  border-radius: 50%;
  width: 40px;
  height: 40px;
  cursor: pointer;
}
.send-btn.streaming {
  background: var(--stop-button);
  color: #FFF;
}

/* Markdown rendering */
.msg-content h1, .msg-content h2, .msg-content h3 { color: var(--primary); }
.msg-content a { color: var(--primary); text-decoration: none; }
.msg-content a:hover { text-decoration: underline; }
.msg-content blockquote {
  border-left: 3px solid var(--primary);
  padding-left: 12px;
  color: var(--text-secondary);
}
.msg-content table { border-collapse: collapse; width: 100%; }
.msg-content th { background: var(--card-bg); }
.msg-content th, .msg-content td {
  border: 1px solid var(--outline);
  padding: 6px 10px;
  text-align: left;
}
```

### Client JavaScript Architecture

```
app.js
├── State
│   ├── conversations[]
│   ├── activeConversationId
│   ├── messages[]
│   ├── streamingContent (accumulator for current stream)
│   ├── mode ("chat" | "plan" | "build")
│   └── isStreaming
├── API
│   ├── api.getConversations()
│   ├── api.createConversation()
│   ├── api.getConversation(id)
│   ├── api.deleteConversation(id)
│   └── api.updateConversation(id, {title})
├── WebSocket
│   ├── ws.connect()
│   ├── ws.send(type, payload)
│   ├── ws.onMessage(handler)
│   └── ws.reconnect() — auto-reconnect with exponential backoff
├── Render
│   ├── render.sidebar()
│   ├── render.messages()
│   ├── render.message(msg) — markdown → HTML
│   ├── render.toolCard(toolCall, result)
│   ├── render.streamingDelta(delta)
│   └── render.modePills()
└── Events
    ├── onNewChat()
    ├── onSelectConversation(id)
    ├── onSendMessage()
    ├── onCancelStream()
    ├── onModeChange(mode)
    └── onDeleteConversation(id)
```

### Markdown Rendering

Use a lightweight markdown library (e.g., `marked.js` loaded from CDN or embedded). Render:
- Headings, bold, italic, strikethrough
- Code blocks with syntax highlighting (highlight.js, minimal set)
- Inline code
- Tables
- Lists (ordered, unordered)
- Blockquotes
- Links, images
- LaTeX (optional, via KaTeX)

### Keyboard Shortcuts

| Key | Action |
|-----|--------|
| `Enter` | Send message |
| `Shift+Enter` | New line in input |
| `Escape` | Cancel streaming / close dialog |
| `Ctrl+N` | New conversation |
| `Ctrl+Delete` | Delete conversation |
| `1` / `2` / `3` (in mode pills) | Switch CHAT / PLAN / BUILD |

### Settings Dialog

Accessible via ⚙ icon in header. Tabs:
- **Provider** — select active provider, edit API key/base URL, choose model
- **Agent** — edit prompt sections (identity, values, preferences, context, tools)
- **Tools** — toggle built-in tools on/off
- **MCP** — manage MCP servers
- **Memories** — view/edit/delete memories
- **Sync** — configure AIOPE2 sync URL, trigger pull/push
- **Theme** — toggle dark/light mode

### Responsive Design

- Below 768px: sidebar collapses to hamburger menu
- Below 480px: full-width messages, no max-width constraint
- Touch-friendly: 44px minimum tap targets
