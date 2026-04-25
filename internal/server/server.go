package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/XNet-NGO/AIOPE-Headless/internal/conversation"
	"github.com/XNet-NGO/AIOPE-Headless/internal/llm"
	"github.com/XNet-NGO/AIOPE-Headless/internal/message"
	"github.com/XNet-NGO/AIOPE-Headless/internal/settings"
	"github.com/XNet-NGO/AIOPE-Headless/internal/ws"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

type Server struct {
	Conversations *conversation.Service
	Messages      *message.Service
	Settings      *settings.Service
	Hub           *ws.Hub
	Provider      llm.Provider
	Model         string
	WebFS         fs.FS
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
	prompt, _ := s.Settings.Get("agent_identity_name_role")
	if prompt != "" {
		chatMsgs = append(chatMsgs, llm.ChatMessage{Role: "system", Content: prompt})
	}
	for _, m := range msgs {
		chatMsgs = append(chatMsgs, llm.ChatMessage{Role: m.Role, Content: m.Content})
	}

	// Stream response
	assistantID := uuid.NewString()
	s.Hub.BroadcastJSON(map[string]any{"type": "stream.start", "conversationId": convID, "messageId": assistantID})

	var fullContent strings.Builder
	model := s.Model
	if model == "" {
		model = "gpt-4o"
	}

	err = s.Provider.Stream(chatMsgs, model, func(ev llm.StreamEvent) {
		if ev.Delta != "" {
			fullContent.WriteString(ev.Delta)
			s.Hub.BroadcastJSON(map[string]any{"type": "stream.delta", "conversationId": convID, "messageId": assistantID, "delta": ev.Delta})
		}
		if ev.Done {
			s.Hub.BroadcastJSON(map[string]any{"type": "stream.end", "conversationId": convID, "messageId": assistantID, "finishReason": ev.FinishReason})
		}
	})

	if err != nil {
		s.Hub.BroadcastJSON(map[string]any{"type": "stream.error", "conversationId": convID, "error": err.Error()})
		return
	}

	// Save assistant message
	aMsg, _ := s.Messages.Add(convID, "assistant", fullContent.String())
	if aMsg != nil {
		s.Hub.BroadcastJSON(map[string]any{"type": "message.created", "message": aMsg})
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
	model := s.Model
	if model == "" {
		model = "gpt-4o"
	}
	s.Provider.Stream(prompt, model, func(ev llm.StreamEvent) {
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

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func ListenAndServe(port int, handler http.Handler) error {
	addr := fmt.Sprintf(":%d", port)
	log.Printf("AIOPE-Headless running on http://localhost%s", addr)
	return http.ListenAndServe(addr, handler)
}
