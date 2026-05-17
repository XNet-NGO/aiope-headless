package message

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type Message struct {
	ID             string   `json:"id"`
	ConversationID string   `json:"conversationId"`
	Role           string   `json:"role"`
	Content        string   `json:"content"`
	Reasoning      string   `json:"reasoning,omitempty"`
	ImagePaths     []string `json:"imagePaths,omitempty"`
	Timestamp      int64    `json:"timestamp"`
}

type Service struct{ DB *sql.DB }

func (s *Service) List(convID string) ([]Message, error) {
	rows, err := s.DB.Query("SELECT id,conversationId,role,content,imagePaths,timestamp,reasoning FROM messages WHERE conversationId=? ORDER BY timestamp ASC", convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var imgJSON string
		rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &imgJSON, &m.Timestamp, &m.Reasoning)
		if imgJSON != "" {
			json.Unmarshal([]byte(imgJSON), &m.ImagePaths)
		}
		out = append(out, m)
	}
	return out, nil
}

func (s *Service) Add(convID, role, content string, imagePaths ...string) (*Message, error) {
	return s.AddWithReasoning(convID, role, content, "", imagePaths...)
}

func (s *Service) AddWithReasoning(convID, role, content, reasoning string, imagePaths ...string) (*Message, error) {
	now := time.Now().UnixMilli()
	imgJSON := ""
	if len(imagePaths) > 0 {
		b, _ := json.Marshal(imagePaths)
		imgJSON = string(b)
	}
	m := &Message{ID: uuid.NewString(), ConversationID: convID, Role: role, Content: content, Reasoning: reasoning, ImagePaths: imagePaths, Timestamp: now}
	_, err := s.DB.Exec("INSERT INTO messages(id,conversationId,role,content,imagePaths,timestamp,reasoning) VALUES(?,?,?,?,?,?,?)",
		m.ID, m.ConversationID, m.Role, m.Content, imgJSON, m.Timestamp, m.Reasoning)
	return m, err
}

func (s *Service) Update(id, content string) error {
	_, err := s.DB.Exec("UPDATE messages SET content=? WHERE id=?", content, id)
	return err
}

func (s *Service) DeleteAfter(convID string, afterTimestamp int64) error {
	_, err := s.DB.Exec("DELETE FROM messages WHERE conversationId=? AND timestamp>=?", convID, afterTimestamp)
	return err
}
