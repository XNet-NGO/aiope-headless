package main

import (
	"embed"
	"io/fs"
	"log"

	"github.com/XNet-NGO/AIOPE-Headless/internal/config"
	"github.com/XNet-NGO/AIOPE-Headless/internal/conversation"
	"github.com/XNet-NGO/AIOPE-Headless/internal/db"
	"github.com/XNet-NGO/AIOPE-Headless/internal/llm"
	"github.com/XNet-NGO/AIOPE-Headless/internal/mcp"
	"github.com/XNet-NGO/AIOPE-Headless/internal/message"
	"github.com/XNet-NGO/AIOPE-Headless/internal/provider"
	"github.com/XNet-NGO/AIOPE-Headless/internal/remote"
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
	provSvc := &provider.Service{DB: database}
	var prov llm.Provider
	var model string
	if active := provSvc.GetActive(); active != nil {
		prov = &llm.OpenAI{APIKey: active.APIKey, APIBase: active.APIBase}
		model = active.SelectedModelID
	}
	if prov == nil {
		prov = &llm.OpenAI{}
	}

	webFS, _ := fs.Sub(webEmbed, "web")

	remoteSvc := remote.NewService(database)
	remoteSvc.SeedFromSSHConfig()

	srv := &server.Server{
		Conversations: &conversation.Service{DB: database},
		Messages:      &message.Service{DB: database},
		Settings:      &settings.Service{DB: database},
		Providers:     provSvc,
		Hub:           ws.NewHub(),
		Provider:      prov,
		Model:         model,
		WebFS:         webFS,
		DB:            database,
		MCP:           mcp.NewManager(database),
		Remote:        remoteSvc,
		Password:      cfg.Password,
		BasePath:      cfg.BasePath,
	}

	log.Fatal(server.ListenAndServe(cfg.Bind, cfg.Port, srv.Handler()))
}
