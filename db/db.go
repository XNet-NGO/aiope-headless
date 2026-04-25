package db

import (
	"database/sql"
	_ "embed"
	"time"

	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

//go:embed schema.sql
var schema string

type DB struct{ *sql.DB }

type Conversation struct {
	ID        string
	Title     string
	AgentName string
	CreatedAt int64
	UpdatedAt int64
}

type Message struct {
	ID             string
	ConversationID string
	Role           string
	Content        string
	Timestamp      int64
}

func Open(path string) (*DB, error) {
	d, err := sql.Open("sqlite3", path+"?_journal_mode=WAL&_foreign_keys=on")
	if err != nil {
		return nil, err
	}
	if _, err := d.Exec(schema); err != nil {
		return nil, err
	}
	return &DB{d}, nil
}

func (d *DB) NewConversation(title string) (*Conversation, error) {
	now := time.Now().UnixMilli()
	c := &Conversation{ID: uuid.NewString(), Title: title, AgentName: "default", CreatedAt: now, UpdatedAt: now}
	_, err := d.Exec("INSERT INTO conversations(id,title,agentName,createdAt,updatedAt) VALUES(?,?,?,?,?)", c.ID, c.Title, c.AgentName, c.CreatedAt, c.UpdatedAt)
	return c, err
}

func (d *DB) ListConversations() ([]Conversation, error) {
	rows, err := d.Query("SELECT id,title,agentName,createdAt,updatedAt FROM conversations ORDER BY updatedAt DESC")
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

func (d *DB) GetMessages(convID string) ([]Message, error) {
	rows, err := d.Query("SELECT id,conversationId,role,content,timestamp FROM messages WHERE conversationId=? ORDER BY timestamp ASC", convID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		rows.Scan(&m.ID, &m.ConversationID, &m.Role, &m.Content, &m.Timestamp)
		out = append(out, m)
	}
	return out, nil
}

func (d *DB) AddMessage(convID, role, content string) (*Message, error) {
	now := time.Now().UnixMilli()
	m := &Message{ID: uuid.NewString(), ConversationID: convID, Role: role, Content: content, Timestamp: now}
	_, err := d.Exec("INSERT INTO messages(id,conversationId,role,content,imagePaths,timestamp) VALUES(?,?,?,?,'',?)", m.ID, m.ConversationID, m.Role, m.Content, m.Timestamp)
	if err != nil {
		return nil, err
	}
	d.Exec("UPDATE conversations SET updatedAt=? WHERE id=?", now, convID)
	return m, err
}

func (d *DB) UpdateMessage(id, content string) error {
	_, err := d.Exec("UPDATE messages SET content=? WHERE id=?", content, id)
	return err
}

func (d *DB) ImportConversation(c *Conversation, msgs []Message) error {
	tx, err := d.Begin()
	if err != nil {
		return err
	}
	tx.Exec("INSERT OR REPLACE INTO conversations(id,title,agentName,createdAt,updatedAt) VALUES(?,?,?,?,?)", c.ID, c.Title, c.AgentName, c.CreatedAt, c.UpdatedAt)
	for _, m := range msgs {
		tx.Exec("INSERT OR REPLACE INTO messages(id,conversationId,role,content,imagePaths,timestamp) VALUES(?,?,?,?,'',?)", m.ID, m.ConversationID, m.Role, m.Content, m.Timestamp)
	}
	return tx.Commit()
}
