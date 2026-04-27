package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/XNet-NGO/AIOPE-Headless/internal/conversation"
	"github.com/XNet-NGO/AIOPE-Headless/internal/llm"
	"github.com/XNet-NGO/AIOPE-Headless/internal/message"
	"github.com/XNet-NGO/AIOPE-Headless/internal/provider"
	"github.com/XNet-NGO/AIOPE-Headless/internal/settings"
	"github.com/XNet-NGO/AIOPE-Headless/internal/ws"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type Server struct {
	Conversations *conversation.Service
	Messages      *message.Service
	Settings      *settings.Service
	Providers     *provider.Service
	Hub           *ws.Hub
	Provider      llm.Provider
	Model         string
	WebFS         fs.FS
	DB            *sql.DB
	mu            sync.RWMutex
	cancels       sync.Map // conversationId -> context.CancelFunc
	autoRun       sync.Map // conversationId -> bool
	autoRunCount  sync.Map // conversationId -> int
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("GET /api/conversations", s.listConversations)
	mux.HandleFunc("POST /api/conversations", s.createConversation)
	mux.HandleFunc("GET /api/conversations/{id}", s.getConversation)
	mux.HandleFunc("PATCH /api/conversations/{id}", s.updateConversation)
	mux.HandleFunc("DELETE /api/conversations/{id}", s.deleteConversation)
	mux.HandleFunc("GET /api/settings", s.getSettings)
	mux.HandleFunc("PUT /api/settings/{key}", s.setSetting)

	// Provider routes
	mux.HandleFunc("GET /api/providers", s.listProviders)
	mux.HandleFunc("POST /api/providers", s.createProvider)
	mux.HandleFunc("PUT /api/providers/{id}", s.updateProvider)
	mux.HandleFunc("DELETE /api/providers/{id}", s.deleteProvider)
	mux.HandleFunc("POST /api/providers/{id}/activate", s.activateProvider)
	mux.HandleFunc("GET /api/providers/{id}/models", s.fetchModels)

	// Tool toggles
	mux.HandleFunc("GET /api/tools", s.listToolToggles)
	mux.HandleFunc("PUT /api/tools/{id}", s.setToolToggle)

	// WebSocket
	mux.HandleFunc("/ws", s.handleWS)

	// Web client
	mux.Handle("/", http.FileServer(http.FS(s.WebFS)))

	return mux
}

func (s *Server) listConversations(w http.ResponseWriter, r *http.Request) {
	convs, err := s.Conversations.List()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if convs == nil {
		convs = []conversation.Conversation{}
	}
	writeJSON(w, convs)
}

func (s *Server) createConversation(w http.ResponseWriter, r *http.Request) {
	var req struct{ Title string }
	json.NewDecoder(r.Body).Decode(&req)
	if req.Title == "" {
		req.Title = "New Chat"
	}
	c, err := s.Conversations.Create(req.Title)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.Hub.BroadcastJSON(map[string]any{"type": "conversation.created", "conversation": c})
	w.WriteHeader(201)
	writeJSON(w, c)
}

func (s *Server) getConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	c, err := s.Conversations.Get(id)
	if err != nil {
		http.Error(w, "not found", 404)
		return
	}
	msgs, _ := s.Messages.List(id)
	if msgs == nil {
		msgs = []message.Message{}
	}
	writeJSON(w, map[string]any{"conversation": c, "messages": msgs})
}

func (s *Server) updateConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct{ Title string }
	json.NewDecoder(r.Body).Decode(&req)
	if err := s.Conversations.UpdateTitle(id, req.Title); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.Hub.BroadcastJSON(map[string]any{"type": "conversation.updated", "id": id, "title": req.Title, "updatedAt": time.Now().UnixMilli()})
	w.WriteHeader(204)
}

func (s *Server) deleteConversation(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.Conversations.Delete(id)
	s.Hub.BroadcastJSON(map[string]any{"type": "conversation.deleted", "id": id})
	w.WriteHeader(204)
}

func (s *Server) getSettings(w http.ResponseWriter, r *http.Request) {
	all, _ := s.Settings.All()
	writeJSON(w, all)
}

func (s *Server) setSetting(w http.ResponseWriter, r *http.Request) {
	key := r.PathValue("key")
	var req struct{ Value string }
	json.NewDecoder(r.Body).Decode(&req)
	s.Settings.Set(key, req.Value)
	w.WriteHeader(204)
}

// WebSocket handler
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	client := &ws.Client{Conn: conn, Send: make(chan []byte, 64)}
	s.Hub.Register(client)
	defer s.Hub.Unregister(client)

	// Writer goroutine
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		for {
			select {
			case msg, ok := <-client.Send:
				if !ok {
					return
				}
				conn.Write(ctx, websocket.MessageText, msg)
			case <-ctx.Done():
				return
			}
		}
	}()

	// Reader loop
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg struct {
			Type           string `json:"type"`
			ConversationID string `json:"conversationId"`
			Content        string `json:"content"`
			Mode           string `json:"mode"`
			MessageID      string `json:"messageId"`
			AtIndex        int    `json:"atIndex"`
			Language       string `json:"language"`
			Text           string `json:"text"`
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "chat.send":
			go s.handleChatSend(ctx, client, msg.ConversationID, msg.Content, msg.Mode)
		case "chat.retry":
			go s.handleRetry(ctx, client, msg.ConversationID, msg.AtIndex, msg.Mode)
		case "chat.edit_resend":
			go s.handleEditResend(ctx, client, msg.ConversationID, msg.Text, msg.AtIndex, msg.Mode)
		case "chat.fork":
			go s.handleFork(client, msg.ConversationID, msg.AtIndex)
		case "chat.compact":
			log.Printf("chat.compact: conv=%s atIndex=%d", msg.ConversationID, msg.AtIndex)
			go s.handleCompact(client, msg.ConversationID, msg.AtIndex)
		case "chat.translate":
			go s.handleTranslate(client, msg.ConversationID, msg.MessageID, msg.Language)
		case "chat.cancel":
			if fn, ok := s.cancels.LoadAndDelete(msg.ConversationID); ok {
				fn.(context.CancelFunc)()
			}
		case "chat.auto_run":
			enabled := msg.Content == "true" || msg.Content == "1"
			s.autoRun.Store(msg.ConversationID, enabled)
			if !enabled {
				s.autoRunCount.Delete(msg.ConversationID)
			}
		}
	}
}

func (s *Server) handleChatSend(ctx context.Context, client *ws.Client, convID, content, mode string) {
	log.Printf("chat.send: conv=%s content=%q mode=%s", convID, content, mode)
	// Create conversation if needed
	if convID == "" {
		c, err := s.Conversations.Create("New Chat")
		if err != nil {
			return
		}
		convID = c.ID
		s.Hub.BroadcastJSON(map[string]any{"type": "conversation.created", "conversation": c})
	}

	// Save user message
	userMsg, err := s.Messages.Add(convID, "user", content)
	if err != nil {
		return
	}
	s.Hub.BroadcastJSON(map[string]any{"type": "message.created", "message": userMsg})

	// Build message history
	msgs, _ := s.Messages.List(convID)
	var chatMsgs []llm.ChatMessage

	// System prompt from settings
	allSettings, _ := s.Settings.All()
	sysPrompt := llm.BuildSystemPrompt(allSettings, mode)
	if sysPrompt == "" {
		sysPrompt = "You are AIOPE, a helpful AI assistant."
	}
	chatMsgs = append(chatMsgs, llm.ChatMessage{Role: "system", Content: sysPrompt})
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}

	// Determine tools (PLAN mode = no write tools, respect toggles)
	tools := s.getEnabledTools(mode)

	// Stream response via orchestrator
	assistantID := uuid.NewString()
	s.Hub.BroadcastJSON(map[string]any{"type": "stream.start", "conversationId": convID, "messageId": assistantID})

	streamCtx, streamCancel := context.WithCancel(context.Background())
	s.cancels.Store(convID, streamCancel)
	defer func() {
		streamCancel()
		s.cancels.Delete(convID)
	}()

	s.mu.RLock()
	prov := s.Provider
	model := s.Model
	s.mu.RUnlock()
	if model == "" {
		model = "gpt-4o"
	}

	// Get gateway info for search/query tools
	var gwURL, gwKey string
	if active := s.Providers.GetActive(); active != nil {
		gwURL = active.APIBase
		gwKey = active.APIKey
	}

	var hadToolCalls bool
	orch := &llm.Orchestrator{
		Provider: prov,
		Model:    model,
		Tools:    tools,
		Ctx:      streamCtx,
		ToolCtx:  &llm.ToolContext{DB: s.DB, GatewayURL: gwURL, GatewayKey: gwKey},
		OnEvent: func(ev llm.StreamEvent) {
			if ev.Delta != "" {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.delta", "conversationId": convID, "messageId": assistantID, "delta": ev.Delta})
			}
			if ev.Reasoning != "" {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.reasoning", "conversationId": convID, "messageId": assistantID, "delta": ev.Reasoning})
			}
			if len(ev.ToolCalls) > 0 {
				hadToolCalls = true
				for _, tc := range ev.ToolCalls {
					s.Hub.BroadcastJSON(map[string]any{"type": "stream.tool_call", "conversationId": convID, "messageId": assistantID, "toolCall": tc})
				}
			}
			if len(ev.ToolResults) > 0 {
				for _, tr := range ev.ToolResults {
					s.Hub.BroadcastJSON(map[string]any{"type": "stream.tool_result", "conversationId": convID, "toolCallId": tr.ID, "name": tr.Name, "result": tr.Result, "isError": tr.IsErr})
				}
			}
			if ev.Done {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.end", "conversationId": convID, "messageId": assistantID, "finishReason": ev.FinishReason})
			}
		},
	}

	fullContent, err := orch.Run(chatMsgs)
	if err != nil {
		log.Printf("LLM error: %v", err)
		s.Hub.BroadcastJSON(map[string]any{"type": "stream.error", "conversationId": convID, "error": err.Error()})
		return
	}

	// Save assistant message
	if fullContent != "" {
		aMsg, _ := s.Messages.Add(convID, "assistant", fullContent)
		if aMsg != nil {
			s.Hub.BroadcastJSON(map[string]any{"type": "message.created", "message": aMsg})
		}
	}
	s.Conversations.Touch(convID)

	// Auto-title after first exchange
	if len(msgs) <= 1 {
		go s.autoTitle(convID, content)
	}

	// Auto-run: continue after tool use
	if hadToolCalls {
		if on, ok := s.autoRun.Load(convID); ok && on.(bool) {
			cnt := 0
			if v, ok := s.autoRunCount.Load(convID); ok {
				cnt = v.(int)
			}
			if cnt < 20 {
				s.autoRunCount.Store(convID, cnt+1)
				go s.handleChatSend(ctx, client, convID, "continue", mode)
				return
			}
			s.autoRunCount.Delete(convID)
		}
	} else {
		s.autoRunCount.Delete(convID)
	}

	// Auto-compact: check if context exceeds 95% of token limit
	s.maybeAutoCompact(client, convID)
}

func (s *Server) autoTitle(convID, userMsg string) {
	prompt := []llm.ChatMessage{
		{Role: "system", Content: "Generate a short title (max 6 words) for this conversation. Reply with only the title, no quotes."},
		{Role: "user", Content: userMsg},
	}
	var title strings.Builder
	s.mu.RLock()
	prov := s.Provider
	s.mu.RUnlock()

	// Use title task model if available
	toolCtx := &llm.ToolContext{DB: s.DB}
	if active := s.Providers.GetActive(); active != nil {
		toolCtx.GatewayURL = active.APIBase
		toolCtx.GatewayKey = active.APIKey
	}
	_, model := toolCtx.ResolveTaskModel("title")
	if model == "" {
		s.mu.RLock()
		model = s.Model
		s.mu.RUnlock()
	}
	if model == "" {
		model = "gpt-4o"
	}
	prov.Stream(prompt, model, nil, func(ev llm.StreamEvent) {
		if ev.Delta != "" {
			title.WriteString(ev.Delta)
		}
	})
	t := strings.TrimSpace(title.String())
	if t != "" && len(t) < 100 {
		s.Conversations.UpdateTitle(convID, t)
		s.Hub.BroadcastJSON(map[string]any{"type": "conversation.updated", "id": convID, "title": t, "updatedAt": time.Now().UnixMilli()})
	}
}

func (s *Server) listProviders(w http.ResponseWriter, r *http.Request) {
	ps, _ := s.Providers.List()
	if ps == nil {
		ps = []provider.Profile{}
	}
	writeJSON(w, ps)
}

func (s *Server) createProvider(w http.ResponseWriter, r *http.Request) {
	var p provider.Profile
	json.NewDecoder(r.Body).Decode(&p)
	if err := s.Providers.Create(&p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.WriteHeader(201)
	writeJSON(w, p)
}

func (s *Server) updateProvider(w http.ResponseWriter, r *http.Request) {
	var p provider.Profile
	json.NewDecoder(r.Body).Decode(&p)
	p.ID = r.PathValue("id")
	if err := s.Providers.Update(&p); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.refreshProvider()
	w.WriteHeader(204)
}

func (s *Server) deleteProvider(w http.ResponseWriter, r *http.Request) {
	s.Providers.Delete(r.PathValue("id"))
	s.refreshProvider()
	w.WriteHeader(204)
}

func (s *Server) activateProvider(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Providers.SetActive(id); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	s.refreshProvider()
	w.WriteHeader(204)
}

func (s *Server) fetchModels(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	ps, _ := s.Providers.List()
	var p *provider.Profile
	for i := range ps {
		if ps[i].ID == id {
			p = &ps[i]
			break
		}
	}
	if p == nil {
		http.Error(w, "not found", 404)
		return
	}
	models, err := s.Providers.FetchModels(p)
	if err != nil {
		http.Error(w, err.Error(), 502)
		return
	}
	writeJSON(w, models)
}

func (s *Server) listToolToggles(w http.ResponseWriter, r *http.Request) {
	rows, err := s.DB.Query("SELECT toolId, enabled FROM tool_toggles")
	if err != nil {
		writeJSON(w, map[string]any{})
		return
	}
	defer rows.Close()
	toggles := map[string]bool{}
	for rows.Next() {
		var id string
		var enabled int
		rows.Scan(&id, &enabled)
		toggles[id] = enabled == 1
	}
	// Include all builtin tools with defaults
	result := make([]map[string]any, 0, len(llm.BuiltinTools))
	for _, t := range llm.BuiltinTools {
		enabled := true
		if v, ok := toggles[t.Name]; ok {
			enabled = v
		}
		result = append(result, map[string]any{"id": t.Name, "name": t.Name, "description": t.Description, "enabled": enabled})
	}
	writeJSON(w, result)
}

func (s *Server) setToolToggle(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct{ Enabled bool }
	json.NewDecoder(r.Body).Decode(&req)
	enabled := 0
	if req.Enabled {
		enabled = 1
	}
	s.DB.Exec("INSERT INTO tool_toggles(toolId,enabled) VALUES(?,?) ON CONFLICT(toolId) DO UPDATE SET enabled=?", id, enabled, enabled)
	w.WriteHeader(204)
}

func (s *Server) refreshProvider() {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Providers.GetActive()
	if p != nil {
		s.Provider = &llm.OpenAI{APIKey: p.APIKey, APIBase: p.APIBase}
		s.Model = p.SelectedModelID
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func ListenAndServe(bind string, port int, handler http.Handler) error {
	host := bind
	if host == "" {
		host = "0.0.0.0"
	}
	addr := fmt.Sprintf("%s:%d", host, port)
	log.Printf("AIOPE-Headless running on http://%s", addr)
	return http.ListenAndServe(addr, handler)
}

// --- Chat action handlers ---

func (s *Server) streamToConv(convID, assistantID, mode string, chatMsgs []llm.ChatMessage) (string, error) {
	s.Hub.BroadcastJSON(map[string]any{"type": "stream.start", "conversationId": convID, "messageId": assistantID})

	ctx, cancel := context.WithCancel(context.Background())
	s.cancels.Store(convID, cancel)
	defer func() {
		cancel()
		s.cancels.Delete(convID)
	}()

	s.mu.RLock()
	prov := s.Provider
	model := s.Model
	s.mu.RUnlock()
	if model == "" {
		model = "gpt-4o"
	}

	tools := s.getEnabledTools(mode)

	var gwURL, gwKey string
	if active := s.Providers.GetActive(); active != nil {
		gwURL = active.APIBase
		gwKey = active.APIKey
	}

	orch := &llm.Orchestrator{
		Provider: prov, Model: model, Tools: tools, Ctx: ctx,
		ToolCtx: &llm.ToolContext{DB: s.DB, GatewayURL: gwURL, GatewayKey: gwKey},
		OnEvent: func(ev llm.StreamEvent) {
			if ev.Delta != "" {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.delta", "conversationId": convID, "messageId": assistantID, "delta": ev.Delta})
			}
			if ev.Reasoning != "" {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.reasoning", "conversationId": convID, "messageId": assistantID, "delta": ev.Reasoning})
			}
			for _, tc := range ev.ToolCalls {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.tool_call", "conversationId": convID, "messageId": assistantID, "toolCall": tc})
			}
			for _, tr := range ev.ToolResults {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.tool_result", "conversationId": convID, "toolCallId": tr.ID, "name": tr.Name, "result": tr.Result, "isError": tr.IsErr})
			}
			if ev.Done {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.end", "conversationId": convID, "messageId": assistantID, "finishReason": ev.FinishReason})
			}
		},
	}
	return orch.Run(chatMsgs)
}

func (s *Server) getEnabledTools(mode string) []llm.ToolDef {
	// Load toggles from DB
	disabled := map[string]bool{}
	rows, err := s.DB.Query("SELECT toolId FROM tool_toggles WHERE enabled=0")
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id string
			rows.Scan(&id)
			disabled[id] = true
		}
	}
	var tools []llm.ToolDef
	for _, t := range llm.BuiltinTools {
		if disabled[t.Name] {
			continue
		}
		if mode == "plan" && (t.Name == "run_sh" || t.Name == "write_file") {
			continue
		}
		tools = append(tools, t)
	}
	return tools
}

func (s *Server) buildHistory(convID, mode string) []llm.ChatMessage {
	msgs, _ := s.Messages.List(convID)
	allSettings, _ := s.Settings.All()
	sysPrompt := llm.BuildSystemPrompt(allSettings, mode)
	if sysPrompt == "" {
		sysPrompt = "You are AIOPE, a helpful AI assistant."
	}
	chatMsgs := []llm.ChatMessage{{Role: "system", Content: sysPrompt}}
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}
	return chatMsgs
}

func (s *Server) handleRetry(_ context.Context, client *ws.Client, convID string, atIndex int, mode string) {
	msgs, _ := s.Messages.List(convID)
	if atIndex < 0 || atIndex >= len(msgs) {
		return
	}
	// Delete from atIndex onward
	s.Messages.DeleteAfter(convID, msgs[atIndex].Timestamp-1)
	s.Hub.BroadcastJSON(map[string]any{"type": "messages.truncated", "conversationId": convID, "atIndex": atIndex})

	chatMsgs := s.buildHistory(convID, mode)
	assistantID := uuid.NewString()
	fullContent, err := s.streamToConv(convID, assistantID, mode, chatMsgs)
	if err != nil {
		s.Hub.BroadcastJSON(map[string]any{"type": "stream.error", "conversationId": convID, "error": err.Error()})
		return
	}
	if fullContent != "" {
		aMsg, _ := s.Messages.Add(convID, "assistant", fullContent)
		if aMsg != nil {
			s.Hub.BroadcastJSON(map[string]any{"type": "message.created", "message": aMsg})
		}
	}
	s.Conversations.Touch(convID)
}

func (s *Server) handleEditResend(_ context.Context, client *ws.Client, convID, text string, atIndex int, mode string) {
	msgs, _ := s.Messages.List(convID)
	if atIndex < 0 || atIndex >= len(msgs) {
		return
	}
	// Delete from atIndex onward
	s.Messages.DeleteAfter(convID, msgs[atIndex].Timestamp-1)
	s.Hub.BroadcastJSON(map[string]any{"type": "messages.truncated", "conversationId": convID, "atIndex": atIndex})

	// Add edited user message
	userMsg, _ := s.Messages.Add(convID, "user", text)
	if userMsg != nil {
		s.Hub.BroadcastJSON(map[string]any{"type": "message.created", "message": userMsg})
	}

	chatMsgs := s.buildHistory(convID, mode)
	assistantID := uuid.NewString()
	fullContent, err := s.streamToConv(convID, assistantID, mode, chatMsgs)
	if err != nil {
		s.Hub.BroadcastJSON(map[string]any{"type": "stream.error", "conversationId": convID, "error": err.Error()})
		return
	}
	if fullContent != "" {
		aMsg, _ := s.Messages.Add(convID, "assistant", fullContent)
		if aMsg != nil {
			s.Hub.BroadcastJSON(map[string]any{"type": "message.created", "message": aMsg})
		}
	}
	s.Conversations.Touch(convID)
}

func (s *Server) handleFork(client *ws.Client, convID string, atIndex int) {
	msgs, _ := s.Messages.List(convID)
	if atIndex < 0 || atIndex > len(msgs) {
		return
	}

	// Title from first user message
	title := "Fork"
	for _, m := range msgs[:atIndex] {
		if m.Role == "user" {
			t := m.Content
			if len(t) > 30 {
				t = t[:30]
			}
			title = "Fork: " + t
			break
		}
	}

	newConv, err := s.Conversations.Create(title)
	if err != nil {
		return
	}
	for _, m := range msgs[:atIndex] {
		s.Messages.Add(newConv.ID, m.Role, m.Content)
	}
	s.Hub.BroadcastJSON(map[string]any{"type": "conversation.created", "conversation": newConv})
	client.SendJSON(map[string]any{"type": "forked", "conversationId": convID, "newConversationId": newConv.ID})
}

func (s *Server) handleCompact(client *ws.Client, convID string, atIndex int) {
	msgs, _ := s.Messages.List(convID)
	if atIndex <= 0 || atIndex > len(msgs) {
		return
	}

	// Build transcript of messages to compact
	var transcript strings.Builder
	for _, m := range msgs[:atIndex] {
		c := m.Content
		if len(c) > 2000 {
			c = c[:2000] + "..."
		}
		fmt.Fprintf(&transcript, "[%s] %s\n\n", m.Role, c)
	}

	// Use summary task model
	toolCtx := &llm.ToolContext{DB: s.DB}
	if active := s.Providers.GetActive(); active != nil {
		toolCtx.GatewayURL = active.APIBase
		toolCtx.GatewayKey = active.APIKey
	}
	_, summaryModel := toolCtx.ResolveTaskModel("summary")

	s.mu.RLock()
	prov := s.Provider
	mainModel := s.Model
	s.mu.RUnlock()
	if summaryModel == "" {
		summaryModel = mainModel
	}
	if summaryModel == "" {
		summaryModel = "gpt-4o"
	}

	prompt := []llm.ChatMessage{
		{Role: "user", Content: "Summarize this conversation concisely, preserving all key context needed to continue. Start with [Summary].\n\n" + transcript.String()},
	}

	client.SendJSON(map[string]any{"type": "compact.start", "conversationId": convID})

	var summary strings.Builder
	log.Printf("compact: using model %s for %d messages", summaryModel, atIndex)
	err := prov.Stream(prompt, summaryModel, nil, func(ev llm.StreamEvent) {
		if ev.Delta != "" {
			summary.WriteString(ev.Delta)
		}
	})
	if err != nil {
		log.Printf("compact: stream error: %v", err)
	}

	sumText := strings.TrimSpace(summary.String())
	log.Printf("compact: summary length=%d", len(sumText))
	if sumText == "" {
		client.SendJSON(map[string]any{"type": "stream.error", "conversationId": convID, "error": "Compaction failed — empty summary"})
		return
	}

	// Delete all messages, re-insert summary + remaining
	if len(msgs) > 0 {
		s.Messages.DeleteAfter(convID, 0)
	}
	s.Messages.Add(convID, "system", sumText)
	s.Messages.Add(convID, "system", "⟳ Context compacted — earlier messages summarized")
	for _, m := range msgs[atIndex:] {
		s.Messages.Add(convID, m.Role, m.Content)
	}

	// Tell client to reload messages
	newMsgs, _ := s.Messages.List(convID)
	client.SendJSON(map[string]any{"type": "compact.done", "conversationId": convID, "messages": newMsgs})
}

func (s *Server) maybeAutoCompact(client *ws.Client, convID string) {
	// Check if auto_compact is enabled in settings
	v, _ := s.Settings.Get("auto_compact")
	if v != "true" && v != "1" {
		return
	}
	// Get context token limit from active model config
	contextTokens := 128000
	if p := s.Providers.GetActive(); p != nil {
		if mc, ok := p.ModelConfigs[p.SelectedModelID]; ok && mc.ContextTokens > 0 {
			contextTokens = mc.ContextTokens
		}
	}
	msgs, _ := s.Messages.List(convID)
	if len(msgs) <= 4 {
		return
	}
	total := 0
	for _, m := range msgs {
		total += llm.EstimateTokens(m.Content)
	}
	threshold := contextTokens * 95 / 100
	if total > threshold {
		log.Printf("auto-compact: %d tokens > %d threshold, compacting conv %s", total, threshold, convID)
		s.handleCompact(client, convID, len(msgs)/2)
	}
}

func (s *Server) handleTranslate(client *ws.Client, convID, messageID, language string) {
	if language == "" {
		language = "English"
	}

	// Find message content
	msgs, _ := s.Messages.List(convID)
	var content string
	for _, m := range msgs {
		if m.ID == messageID {
			content = m.Content
			break
		}
	}
	if content == "" {
		return
	}

	toolCtx := &llm.ToolContext{DB: s.DB}
	if active := s.Providers.GetActive(); active != nil {
		toolCtx.GatewayURL = active.APIBase
		toolCtx.GatewayKey = active.APIKey
	}
	_, transModel := toolCtx.ResolveTaskModel("translation")

	s.mu.RLock()
	prov := s.Provider
	s.mu.RUnlock()
	if transModel == "" {
		transModel = s.Model
	}

	prompt := []llm.ChatMessage{
		{Role: "user", Content: "Translate the following text to " + language + ". Output ONLY the translated text, no explanations, no original text, no labels:\n\n" + content},
	}

	prov.Stream(prompt, transModel, nil, func(ev llm.StreamEvent) {
		if ev.Delta != "" {
			client.SendJSON(map[string]any{"type": "translation.delta", "conversationId": convID, "messageId": messageID, "delta": ev.Delta})
		}
		if ev.Done {
			client.SendJSON(map[string]any{"type": "translation.done", "conversationId": convID, "messageId": messageID})
		}
	})
}
