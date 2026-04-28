CREATE TABLE IF NOT EXISTS conversations (
    id        TEXT PRIMARY KEY,
    title     TEXT NOT NULL DEFAULT 'New Chat',
    agentName TEXT NOT NULL DEFAULT 'default',
    createdAt INTEGER NOT NULL,
    updatedAt INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversations_updated ON conversations(updatedAt DESC);

CREATE TABLE IF NOT EXISTS messages (
    id             TEXT PRIMARY KEY,
    conversationId TEXT NOT NULL,
    role           TEXT NOT NULL,
    content        TEXT NOT NULL,
    imagePaths     TEXT NOT NULL DEFAULT '',
    timestamp      INTEGER NOT NULL,
    FOREIGN KEY (conversationId) REFERENCES conversations(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_messages_conv ON messages(conversationId);

CREATE TABLE IF NOT EXISTS memories (
    key       TEXT PRIMARY KEY,
    content   TEXT NOT NULL,
    category  TEXT NOT NULL DEFAULT 'general',
    createdAt INTEGER NOT NULL,
    updatedAt INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS providers (
    id        TEXT PRIMARY KEY,
    json      TEXT NOT NULL,
    isActive  INTEGER NOT NULL DEFAULT 0,
    updatedAt INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS tool_toggles (
    toolId  TEXT PRIMARY KEY,
    enabled INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS mcp_servers (
    id   TEXT PRIMARY KEY,
    json TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS model_cache (
    builtinId TEXT PRIMARY KEY,
    json      TEXT NOT NULL,
    cachedAt  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings_kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS remote_servers (
    id            TEXT PRIMARY KEY,
    name          TEXT NOT NULL,
    host          TEXT NOT NULL,
    port          INTEGER NOT NULL DEFAULT 2222,
    user          TEXT NOT NULL DEFAULT 'root',
    bootstrapPort INTEGER NOT NULL DEFAULT 22,
    keyPath       TEXT NOT NULL DEFAULT '',
    privateKey    TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT 'offline',
    osInfo        TEXT NOT NULL DEFAULT '',
    daemonVersion TEXT NOT NULL DEFAULT '',
    lastSeen      INTEGER NOT NULL DEFAULT 0,
    createdAt     INTEGER NOT NULL
);