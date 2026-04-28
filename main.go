package main

import (
	"embed"
	"database/sql"
	"io/fs"
	"log"
	"os"

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

	// Load provider from DB, seed defaults on first run
	provSvc := &provider.Service{DB: database}
	if ps, _ := provSvc.List(); len(ps) == 0 {
		seedDefaults(provSvc, database)
	}
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
	}

	log.Fatal(server.ListenAndServe(cfg.Bind, cfg.Port, srv.Handler()))
}

func seedDefaults(svc *provider.Service, database *sql.DB) {
	key := "63b282b9-2952-4c88-84e4-91a1eb91c007"
	if v := os.Getenv("AIOPE_GATEWAY_KEY"); v != "" {
		key = v
	}
	boolPtr := func(b bool) *bool { return &b }
	f64Ptr := func(f float64) *float64 { return &f }
	strPtr := func(s string) *string { return &s }
	mc := func(id string, tools, vision bool, ctx int, reasoning *string) provider.ModelConfig {
		return provider.ModelConfig{
			ModelID: id, ToolsOverride: boolPtr(tools), VisionOverride: boolPtr(vision),
			Temperature: f64Ptr(0.6), ReasoningEffort: reasoning,
			ContextTokens: ctx, AutoCompact: true,
		}
	}
	auto := strPtr("auto")
	p := &provider.Profile{
		ID:              "default_gateway",
		BuiltinID:       "aiope_gateway",
		Label:           "AIOPE Gateway",
		APIKey:          key,
		APIBase:         "https://inf.xnet.ngo/v1",
		SelectedModelID: "google-ai-studio/models-gemma-4-31b-it",
		IsActive:        true,
		ModelConfigs: map[string]provider.ModelConfig{
			"google-ai-studio/models-gemma-4-31b-it":    mc("google-ai-studio/models-gemma-4-31b-it", true, true, 256000, auto),
			"google-ai-studio/models-gemma-4-26b-a4b-it": mc("google-ai-studio/models-gemma-4-26b-a4b-it", true, true, 256000, auto),
			"google-ai-studio/models-gemma-3-27b-it":    mc("google-ai-studio/models-gemma-3-27b-it", false, true, 128000, auto),
			"google-ai-studio/models-gemma-3-12b-it":    mc("google-ai-studio/models-gemma-3-12b-it", false, true, 128000, nil),
			"google-ai-studio/models-gemma-3-4b-it":     mc("google-ai-studio/models-gemma-3-4b-it", false, true, 128000, nil),
			"google-ai-studio/models-gemma-3-1b-it":     mc("google-ai-studio/models-gemma-3-1b-it", false, false, 32000, nil),
			"google-ai-studio/models-gemma-3n-e2b-it":   mc("google-ai-studio/models-gemma-3n-e2b-it", false, true, 128000, auto),
			"google-ai-studio/models-gemma-3n-e4b-it":   mc("google-ai-studio/models-gemma-3n-e4b-it", false, true, 128000, auto),
			"cline/minimax-minimax-m2.5":                mc("cline/minimax-minimax-m2.5", true, false, 200000, auto),
			"zen/minimax-m2.5-free":                     mc("zen/minimax-m2.5-free", true, false, 200000, auto),
			"zen/nemotron-3-super-free":                 mc("zen/nemotron-3-super-free", true, false, 1000000, auto),
			"zen/big-pickle":                            mc("zen/big-pickle", true, false, 200000, auto),
			"cline/z-ai-glm-5":                         mc("cline/z-ai-glm-5", true, false, 200000, auto),
			"openrouter/openrouter-free":                mc("openrouter/openrouter-free", false, true, 128000, auto),
			"pollinations/openai":                       mc("pollinations/openai", true, false, 128000, auto),
			"pollinations/openai-fast":                  mc("pollinations/openai-fast", true, false, 128000, auto),
		},
	}
	svc.Create(p)
	// If provider exists but key/base got wiped, restore them
	if existing, err := svc.Get(p.ID); err == nil {
		changed := false
		if existing.APIKey == "" {
			existing.APIKey = p.APIKey
			changed = true
		}
		if existing.APIBase == "" {
			existing.APIBase = p.APIBase
			changed = true
		}
		if existing.Label == "" {
			existing.Label = p.Label
			changed = true
		}
		if existing.ModelConfigs == nil || len(existing.ModelConfigs) == 0 {
			existing.ModelConfigs = p.ModelConfigs
			changed = true
		}
		if changed {
			log.Printf("seed: restoring provider %s (key=%d base=%s)", p.ID, len(existing.APIKey), existing.APIBase)
			svc.Update(existing)
		}
	}
	svc.SetActive(p.ID)

	// Seed task model defaults
	for task, model := range llm.TaskModelDefaults {
		database.Exec("INSERT OR IGNORE INTO settings_kv(key,value) VALUES(?,?)", "task_model_"+task, model)
	}

	log.Println("Seeded default AIOPE Gateway provider")
}
