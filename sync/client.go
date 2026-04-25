package sync

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type Client struct {
	Base string // e.g. "http://10.121.21.2:8080"
}

func (c *Client) ListConversations() ([]ConversationSummary, error) {
	resp, err := http.Get(c.Base + "/api/conversations")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out []ConversationSummary
	return out, json.NewDecoder(resp.Body).Decode(&out)
}

func (c *Client) GetConversation(id string) (*ConversationFull, error) {
	resp, err := http.Get(c.Base + "/api/conversations/" + id)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
	var out ConversationFull
	return &out, json.NewDecoder(resp.Body).Decode(&out)
}
