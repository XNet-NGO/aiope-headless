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
		}
		if json.Unmarshal(data, &msg) != nil {
			continue
		}
		switch msg.Type {
		case "chat.send":
			go s.handleChatSend(ctx, client, msg.ConversationID, msg.Content, msg.Mode)
		case "chat.cancel":
			// TODO: cancellation
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

	// Determine tools (PLAN mode = no write tools)
	tools := llm.BuiltinTools
	if mode == "plan" {
		var readOnly []llm.ToolDef
		for _, t := range tools {
			if t.Name != "run_sh" && t.Name != "write_file" {
				readOnly = append(readOnly, t)
			}
		}
		tools = readOnly
	}

	// Stream response via orchestrator
	assistantID := uuid.NewString()
	s.Hub.BroadcastJSON(map[string]any{"type": "stream.start", "conversationId": convID, "messageId": assistantID})

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

	orch := &llm.Orchestrator{
		Provider: prov,
		Model:    model,
		Tools:    tools,
		ToolCtx:  &llm.ToolContext{DB: s.DB, GatewayURL: gwURL, GatewayKey: gwKey},
		OnEvent: func(ev llm.StreamEvent) {
			if ev.Delta != "" {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.delta", "conversationId": convID, "messageId": assistantID, "delta": ev.Delta})
			}
			if ev.Reasoning != "" {
				s.Hub.BroadcastJSON(map[string]any{"type": "stream.reasoning", "conversationId": convID, "messageId": assistantID, "delta": ev.Reasoning})
			}
			if len(ev.ToolCalls) > 0 {
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
