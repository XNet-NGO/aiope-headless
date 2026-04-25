package sync

// Sync API — served by AIOPE2 on Android over ZT network
// Headless client pulls from this to import conversations
//
// GET  /api/conversations              → []ConversationSummary
// GET  /api/conversations/:id          → ConversationFull (with messages)
// POST /api/conversations              → import a conversation back to app
// POST /api/conversations/:id/messages → push new messages to app

type ConversationSummary struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	AgentName string `json:"agentName"`
	CreatedAt int64  `json:"createdAt"`
	UpdatedAt int64  `json:"updatedAt"`
	MsgCount  int    `json:"msgCount"`
}

type MessagePayload struct {
	ID        string `json:"id"`
	Role      string `json:"role"`
	Content   string `json:"content"`
	Timestamp int64  `json:"timestamp"`
}

type ConversationFull struct {
	ConversationSummary
	Messages []MessagePayload `json:"messages"`
}
