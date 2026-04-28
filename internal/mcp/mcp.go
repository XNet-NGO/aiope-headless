package mcp

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Transport string

const (
	TransportHTTP Transport = "http"
	TransportSSE  Transport = "sse"
)

type ServerConfig struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	URL       string            `json:"url"`
	Transport Transport         `json:"transport"`
	Headers   map[string]string `json:"headers,omitempty"`
	Enabled   bool              `json:"enabled"`
	ToolCount int               `json:"toolCount"`
	Status    string            `json:"status"` // idle, connecting, connected, error
	Error     string            `json:"error,omitempty"`
}

type ToolMeta struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

type Manager struct {
	DB        *sql.DB
	mu        sync.RWMutex
	sessions  map[string]string     // serverId -> sessionId
	toolCache map[string][]ToolMeta // serverId -> tools
	toolMap   map[string]string     // toolName -> serverId
	reqID     atomic.Int64
	stopHB    chan struct{}
}

func NewManager(db *sql.DB) *Manager {
	m := &Manager{
		DB:        db,
		sessions:  map[string]string{},
		toolCache: map[string][]ToolMeta{},
		toolMap:   map[string]string{},
		stopHB:    make(chan struct{}),
	}
	go m.heartbeatLoop()
	return m
}

func (m *Manager) heartbeatLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.mu.RLock()
			var connected []ServerConfig
			for _, s := range m.ListServers() {
				if _, ok := m.sessions[s.ID]; ok && s.Enabled {
					connected = append(connected, s)
				}
			}
			m.mu.RUnlock()
			for _, s := range connected {
				m.sendNotification(s, "notifications/ping")
			}
		case <-m.stopHB:
			return
		}
	}
}

func (m *Manager) ListServers() []ServerConfig {
	rows, err := m.DB.Query("SELECT id, json FROM mcp_servers")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []ServerConfig
	for rows.Next() {
		var id, j string
		rows.Scan(&id, &j)
		var s ServerConfig
		json.Unmarshal([]byte(j), &s)
		s.ID = id
		out = append(out, s)
	}
	return out
}

func (m *Manager) SaveServer(s *ServerConfig) error {
	j, _ := json.Marshal(s)
	_, err := m.DB.Exec("INSERT INTO mcp_servers(id,json) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET json=?", s.ID, string(j), string(j))
	return err
}

func (m *Manager) DeleteServer(id string) {
	m.DB.Exec("DELETE FROM mcp_servers WHERE id=?", id)
	m.clearSession(id)
}

func (m *Manager) DiscoverTools(server ServerConfig) ([]ToolMeta, error) {
	m.clearSession(server.ID)

	// Initialize
	params := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "AIOPE-Headless", "version": "1.0"},
	}
	if _, err := m.sendRequest(server, "initialize", params); err != nil {
		m.updateStatus(server.ID, "error", err.Error())
		return nil, err
	}
	m.sendNotification(server, "notifications/initialized")

	// List tools
	resp, err := m.sendRequest(server, "tools/list", nil)
	if err != nil {
		m.updateStatus(server.ID, "error", err.Error())
		return nil, err
	}

	result, _ := resp["result"].(map[string]any)
	toolsRaw, _ := result["tools"].([]any)
	var tools []ToolMeta
	for _, t := range toolsRaw {
		tm, ok := t.(map[string]any)
		if !ok {
			continue
		}
		name, _ := tm["name"].(string)
		desc, _ := tm["description"].(string)
		schema, _ := tm["inputSchema"].(map[string]any)
		if schema == nil {
			schema = map[string]any{"type": "object", "properties": map[string]any{}}
		}
		tools = append(tools, ToolMeta{Name: name, Description: desc, InputSchema: schema})
	}

	m.mu.Lock()
	m.toolCache[server.ID] = tools
	for _, t := range tools {
		m.toolMap[t.Name] = server.ID
	}
	m.mu.Unlock()

	server.ToolCount = len(tools)
	server.Status = "connected"
	server.Error = ""
	m.SaveServer(&server)
	return tools, nil
}

func (m *Manager) ExecuteTool(name string, args map[string]any) (string, bool) {
	m.mu.RLock()
	serverID, ok := m.toolMap[name]
	m.mu.RUnlock()
	if !ok {
		return "", false
	}

	servers := m.ListServers()
	var server *ServerConfig
	for i := range servers {
		if servers[i].ID == serverID {
			server = &servers[i]
			break
		}
	}
	if server == nil {
		return "", false
	}

	params := map[string]any{"name": name, "arguments": args}
	resp, err := m.sendRequest(*server, "tools/call", params)
	if err != nil {
		return "MCP error: " + err.Error(), true
	}

	result, _ := resp["result"].(map[string]any)
	content, _ := result["content"].([]any)
	var parts []string
	for _, c := range content {
		cm, _ := c.(map[string]any)
		if text, ok := cm["text"].(string); ok {
			parts = append(parts, text)
		}
	}
	if len(parts) == 0 {
		b, _ := json.Marshal(result)
		return string(b), true
	}
	return strings.Join(parts, "\n"), true
}

func (m *Manager) GetToolDefs() []ToolMeta {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var all []ToolMeta
	for _, tools := range m.toolCache {
		all = append(all, tools...)
	}
	return all
}

func (m *Manager) sendRequest(server ServerConfig, method string, params any) (map[string]any, error) {
	body := map[string]any{
		"jsonrpc": "2.0",
		"id":      m.reqID.Add(1),
		"method":  method,
	}
	if params != nil {
		body["params"] = params
	}
	data, _ := json.Marshal(body)

	req, _ := http.NewRequest("POST", server.URL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	accept := "application/json, text/event-stream"
	if server.Transport == TransportSSE {
		accept = "text/event-stream"
	}
	req.Header.Set("Accept", accept)
	m.mu.RLock()
	if sid, ok := m.sessions[server.ID]; ok {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	m.mu.RUnlock()
	for k, v := range server.Headers {
		req.Header.Set(k, v)
	}

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(b))
	}

	if sid := resp.Header.Get("Mcp-Session-Id"); sid != "" {
		m.mu.Lock()
		m.sessions[server.ID] = sid
		m.mu.Unlock()
	}

	ct := resp.Header.Get("Content-Type")
	var respText string
	if strings.Contains(ct, "text/event-stream") {
		scanner := bufio.NewScanner(resp.Body)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "data: ") {
				respText = line[6:]
			}
		}
	} else {
		b, _ := io.ReadAll(resp.Body)
		respText = string(b)
	}

	var result map[string]any
	if err := json.Unmarshal([]byte(respText), &result); err != nil {
		return nil, fmt.Errorf("invalid JSON response: %s", respText[:min(len(respText), 200)])
	}
	return result, nil
}

func (m *Manager) sendNotification(server ServerConfig, method string) {
	body := map[string]any{"jsonrpc": "2.0", "method": method}
	data, _ := json.Marshal(body)
	req, _ := http.NewRequest("POST", server.URL, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json")
	m.mu.RLock()
	if sid, ok := m.sessions[server.ID]; ok {
		req.Header.Set("Mcp-Session-Id", sid)
	}
	m.mu.RUnlock()
	for k, v := range server.Headers {
		req.Header.Set(k, v)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err == nil {
		resp.Body.Close()
	}
}

func (m *Manager) clearSession(serverID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, serverID)
	if tools, ok := m.toolCache[serverID]; ok {
		for _, t := range tools {
			delete(m.toolMap, t.Name)
		}
	}
	delete(m.toolCache, serverID)
}

func (m *Manager) updateStatus(serverID, status, errMsg string) {
	servers := m.ListServers()
	for i := range servers {
		if servers[i].ID == serverID {
			servers[i].Status = status
			servers[i].Error = errMsg
			m.SaveServer(&servers[i])
			return
		}
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
