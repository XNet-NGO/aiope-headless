package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/XNet-NGO/AIOPE-Headless/internal/config"
	"github.com/XNet-NGO/AIOPE-Headless/internal/conversation"
	"github.com/XNet-NGO/AIOPE-Headless/internal/db"
	"github.com/XNet-NGO/AIOPE-Headless/internal/llm"
	"github.com/XNet-NGO/AIOPE-Headless/internal/message"
	"github.com/XNet-NGO/AIOPE-Headless/internal/server"
	"github.com/XNet-NGO/AIOPE-Headless/internal/settings"
	"github.com/XNet-NGO/AIOPE-Headless/internal/ws"
)

//go:embed web/*
var webEmbed embed.FS

func main() {
	cfg := config.Load()

	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatal("db:", err)
	}
	defer database.Close()

	// Load provider from DB
	var provider llm.Provider
	var model string
	var row struct{ json string }
	err = database.QueryRow("SELECT json FROM providers WHERE isActive=1 LIMIT 1").Scan(&row.json)
	if err == nil {
		provider, model = llm.ProviderFromJSON(row.json)
	}
	if provider == nil {
		provider = &llm.OpenAI{}
	}

	webFS, _ := fs.Sub(webEmbed, "web")

	srv := &server.Server{
		Conversations: &conversation.Service{DB: database},
		Messages:      &message.Service{DB: database},
		Settings:      &settings.Service{DB: database},
		Hub:           ws.NewHub(),
		Provider:      provider,
		Model:         model,
		WebFS:         webFS,
	}

	log.Fatal(server.ListenAndServe(cfg.Port, srv.Handler()))
}
