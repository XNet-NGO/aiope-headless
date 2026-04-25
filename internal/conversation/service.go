package conversation

import (
	"database/sql"
	"time"

	"github.com/google/uuid"
)

type Conversation struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	AgentName string `json:"agentName"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
}

type Service struct{ DB *sql.DB }

func (s *Service) List() ([]Conversation, error) {
	rows, err := s.DB.Query("SELECT id,title,agentName,createdAt,updatedAt FROM conversations ORDER BY updatedAt DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		rows.Scan(&c.ID, &c.Title, &c.AgentName, &c.CreatedAt, &c.UpdatedAt)
		out = append(out, c)
	}
	return out, nil
}

func (s *Service) Get(id string) (*Conversation, error) {
	var c Conversation
	err := s.DB.QueryRow("SELECT id,title,agentName,createdAt,updatedAt FROM conversations WHERE id=?", id).
		Scan(&c.ID, &c.Title, &c.AgentName, &c.CreatedAt, &c.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Service) Create(title string) (*Conversation, error) {
	now := time.Now().UnixMilli()
	c := &Conversation{ID: uuid.NewString(), Title: title, AgentName: "default", CreatedAt: now, UpdatedAt: now}
	_, err := s.DB.Exec("INSERT INTO conversations(id,title,agentName,createdAt,updatedAt) VALUES(?,?,?,?,?)",
		c.ID, c.Title, c.AgentName, c.CreatedAt, c.UpdatedAt)
	return c, err
}

func (s *Service) UpdateTitle(id, title string) error {
	_, err := s.DB.Exec("UPDATE conversations SET title=?,updatedAt=? WHERE id=?", title, time.Now().UnixMilli(), id)
	return err
}

func (s *Service) Delete(id string) error {
	_, err := s.DB.Exec("DELETE FROM conversations WHERE id=?", id)
	return err
}

func (s *Service) Touch(id string) {
	s.DB.Exec("UPDATE conversations SET updatedAt=? WHERE id=?", time.Now().UnixMilli(), id)
}
