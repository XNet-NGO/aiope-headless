package provider

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/google/uuid"
)

type ModelConfig struct {
	ModelID         string   `json:"modelId"`
	Temperature     *float64 `json:"temperature,omitempty"`
	MaxTokens       *int     `json:"maxTokens,omitempty"`
	ContextTokens   int      `json:"contextTokens"`
	ReasoningEffort *string  `json:"reasoningEffort,omitempty"`
}

type Profile struct {
	ID              string                 `json:"id"`
	BuiltinID       string                 `json:"builtinId"`
	Label           string                 `json:"label"`
	APIKey          string                 `json:"apiKey"`
	APIBase         string                 `json:"apiBase"`
	SelectedModelID string                 `json:"selectedModelId"`
	IsActive        bool                   `json:"isActive"`
	ModelConfigs    map[string]ModelConfig  `json:"modelConfigs,omitempty"`
}

type Service struct{ DB *sql.DB }

func (s *Service) List() ([]Profile, error) {
	rows, err := s.DB.Query("SELECT id,json,isActive FROM providers ORDER BY updatedAt DESC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Profile
	for rows.Next() {
		var id, j string
		var active int
		rows.Scan(&id, &j, &active)
		var p Profile
		json.Unmarshal([]byte(j), &p)
		p.ID = id
		p.IsActive = active == 1
		out = append(out, p)
	}
	return out, nil
}

func (s *Service) GetActive() *Profile {
	var j string
	var id string
	err := s.DB.QueryRow("SELECT id,json FROM providers WHERE isActive=1 LIMIT 1").Scan(&id, &j)
	if err != nil {
		return nil
	}
	var p Profile
	json.Unmarshal([]byte(j), &p)
	p.ID = id
	p.IsActive = true
	return &p
}

func (s *Service) Create(p *Profile) error {
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	j, _ := json.Marshal(p)
	_, err := s.DB.Exec("INSERT INTO providers(id,json,isActive,updatedAt) VALUES(?,?,0,?)", p.ID, string(j), time.Now().UnixMilli())
	return err
}

func (s *Service) Update(p *Profile) error {
	j, _ := json.Marshal(p)
	_, err := s.DB.Exec("UPDATE providers SET json=?,updatedAt=? WHERE id=?", string(j), time.Now().UnixMilli(), p.ID)
	return err
}

func (s *Service) Delete(id string) error {
	_, err := s.DB.Exec("DELETE FROM providers WHERE id=?", id)
	return err
}

func (s *Service) SetActive(id string) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	tx.Exec("UPDATE providers SET isActive=0")
	tx.Exec("UPDATE providers SET isActive=1 WHERE id=?", id)
	return tx.Commit()
}

type ModelDef struct {
	ID      string `json:"id"`
	Object  string `json:"object,omitempty"`
	OwnedBy string `json:"owned_by,omitempty"`
}

func (s *Service) FetchModels(p *Profile) ([]ModelDef, error) {
	base := p.APIBase
	if base == "" {
		base = "https://api.openai.com/v1"
	}
	req, _ := http.NewRequest("GET", base+"/models", nil)
	if p.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+p.APIKey)
	}
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("status %d: %s", resp.StatusCode, string(b))
	}
	var result struct {
		Data []ModelDef `json:"data"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Data, nil
}
