package llm

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var ParallelSafe = map[string]bool{
	"read_file": true, "list_directory": true, "fetch_url": true,
	"search_web": true, "search_images": true, "query_data": true,
	"memory_recall": true, "ssh_exec": true, "analyze_image": true,
	"task": true,
}

var BuiltinTools = []ToolDef{
	{
		Name: "run_sh", Description: "Execute shell command",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string"},
			},
			"required": []string{"command"},
		},
	},
	{
		Name: "read_file", Description: "Read file contents",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name: "write_file", Description: "Write file",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string"},
				"content": map[string]any{"type": "string"},
			},
			"required": []string{"path", "content"},
		},
	},
	{
		Name: "list_directory", Description: "List directory",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name: "fetch_url", Description: "Fetch a URL. Returns extracted text and images as ![alt](url) markdown from HTML pages.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":  map[string]any{"type": "string"},
				"mode": map[string]any{"type": "string", "description": "Optional: 'raw' for raw response, 'text' (default) for extracted text+images"},
			},
			"required": []string{"url"},
		},
	},
	{
		Name: "search_web", Description: "Search the web for current information, news, answers, or any topic. Returns results with titles, URLs, and snippets.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "search_images", Description: "Search for images on the web. Returns image URLs with titles.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "query_data", Description: "Query live real-time data. Returns JSON and images as ![alt](url) markdown. Pass 'extra' for searches or station/breed IDs. Available categories: air_quality, alerts, apod, asteroids, astronauts, cat, cat_breed, cat_breeds, cme, earth_events, earth_image, earthquakes, earthquakes_significant, epic, fires, geomagnetic, impact_risk, ip_location, iss, nasa_media, nasa_tech, ocean_temp, solar, solar_flares, sunrise_sunset, tides, time, uv, weather, weather_hourly",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{"type": "string"},
				"extra":    map[string]any{"type": "string", "description": "Optional: search query, station ID, or breed ID depending on category"},
			},
			"required": []string{"category"},
		},
	},
	{
		Name: "memory_store", Description: "Store a fact or preference to remember across conversations",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":      map[string]any{"type": "string", "description": "Short key like 'user_name'"},
				"content":  map[string]any{"type": "string", "description": "The fact to remember"},
				"category": map[string]any{"type": "string", "description": "Optional: general, preference, learning, error"},
			},
			"required": []string{"key", "content"},
		},
	},
	{
		Name: "memory_recall", Description: "Search persistent memory. Empty query lists all.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "memory_forget", Description: "Delete a memory by key",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string"},
			},
			"required": []string{"key"},
		},
	},
	{
		Name: "image_generate", Description: "Generate an image from a text prompt. Returns a URL. Include as ![desc](url) to display.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string"},
			},
			"required": []string{"prompt"},
		},
	},
	{
		Name: "task", Description: "Spawn a subagent to research a topic. Has read-only tools (search, fetch, read). Returns a text summary. Use for parallel research — call multiple tasks at once for different topics.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"description": map[string]any{"type": "string", "description": "Short 3-5 word description"},
				"prompt":      map[string]any{"type": "string", "description": "Detailed instructions for the subagent"},
			},
			"required": []string{"description", "prompt"},
		},
	},
	{
		Name: "analyze_image", Description: "Analyze an image from a URL or file path using vision",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":      map[string]any{"type": "string", "description": "URL or local file path"},
				"question": map[string]any{"type": "string", "description": "What to look for in the image"},
			},
			"required": []string{"url"},
		},
	},
	{
		Name: "ssh_start", Description: "Open SSH session to a remote server",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{"type": "string"},
			},
			"required": []string{"server"},
		},
	},
	{
		Name: "ssh_exec", Description: "Run command on active SSH session. Use ssh_start first.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server":  map[string]any{"type": "string"},
				"command": map[string]any{"type": "string"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 30)"},
			},
			"required": []string{"server", "command"},
		},
	},
	{
		Name: "ssh_exit", Description: "Close SSH session",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{"type": "string"},
			},
			"required": []string{"server"},
		},
	},
}

type ToolContext struct {
	DB               *sql.DB
	GatewayURL       string
	GatewayKey       string
	ShellOutputLimit int
	FetchLimit       int
	FileReadLimit    int
	McpExecutor      func(name string, args map[string]any) (string, bool)
	SSHStart         func(server string) (string, error)
	SSHExec          func(server, command string, timeout int) (string, error)
	SSHExit          func(server string) error
	OnProgress       func(toolName, status string) // stream tool progress to client
	GeneratedImages  []string                     // local paths of generated images
}

func (ctx *ToolContext) shellLimit() int {
	if ctx.ShellOutputLimit > 0 {
		return ctx.ShellOutputLimit
	}
	return 8000
}

func (ctx *ToolContext) fetchLim() int {
	if ctx.FetchLimit > 0 {
		return ctx.FetchLimit
	}
	return 12000
}

func (ctx *ToolContext) fileReadLim() int {
	if ctx.FileReadLimit > 0 {
		return ctx.FileReadLimit
	}
	return 50000
}

// Task model resolution — different models for different tasks
var TaskModelDefaults = map[string]string{
	"title":             "google-ai-studio/models-gemma-3-4b-it",
	"summary":           "google-ai-studio/models-gemma-3-27b-it",
	"image_gen":         "pollinations-pollen/klein",
	"image_recognition": "google-ai-studio/models-gemma-3-27b-it",
	"subagent":          "google-ai-studio/models-gemma-4-26b-a4b-it",
	"translation":       "google-ai-studio/models-gemma-3-12b-it",
}

func (ctx *ToolContext) ResolveTaskModel(task string) (string, string) {
	// Check settings_kv for override: task_model_{task} = "model_id"
	if ctx.DB != nil {
		var model string
		if err := ctx.DB.QueryRow("SELECT value FROM settings_kv WHERE key=?", "task_model_"+task).Scan(&model); err == nil && model != "" {
			return ctx.GatewayURL, model
		}
	}
	if m, ok := TaskModelDefaults[task]; ok {
		return ctx.GatewayURL, m
	}
	return ctx.GatewayURL, ""
}

func ExecuteTool(name string, args map[string]any, ctx *ToolContext) (string, error) {
	str := func(key string) string {
		v, _ := args[key].(string)
		return v
	}
	switch name {
	case "run_sh":
		cmdCtx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		cmd := exec.CommandContext(cmdCtx, "bash", "-c", str("command"))
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		result := string(out)
		lim := ctx.shellLimit()
		if len(result) > lim {
			result = result[:lim] + "\n...(truncated)"
		}
		if cmdCtx.Err() == context.DeadlineExceeded {
			return result + "\n[timeout after 120s]", nil
		}
		if err != nil && result == "" {
			return "", fmt.Errorf("command failed: %v", err)
		}
		return result, nil

	case "read_file":
		data, err := os.ReadFile(str("path"))
		if err != nil {
			return "", err
		}
		s := string(data)
		lim := ctx.fileReadLim()
		if len(s) > lim {
			s = s[:lim] + "\n...(truncated)"
		}
		return s, nil

	case "write_file":
		p := str("path")
		if p == "" {
			return "Error: path is required", nil
		}
		os.MkdirAll(filepath.Dir(p), 0755)
		if err := os.WriteFile(p, []byte(str("content")), 0644); err != nil {
			return "", err
		}
		return fmt.Sprintf("Wrote %d bytes to %s", len(str("content")), p), nil

	case "list_directory":
		entries, err := os.ReadDir(str("path"))
		if err != nil {
			return "", err
		}
		var b strings.Builder
		for _, e := range entries {
			if e.IsDir() {
				b.WriteString(e.Name() + "/\n")
			} else {
				b.WriteString(e.Name() + "\n")
			}
		}
		return b.String(), nil

	case "fetch_url":
		client := &http.Client{Timeout: 15 * time.Second}
		req, _ := http.NewRequest("GET", str("url"), nil)
		req.Header.Set("User-Agent", "Mozilla/5.0 (Linux) AIOPE/2.0")
		resp, err := client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		lim := ctx.fetchLim()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, int64(lim*2)))
		body := string(data)
		ct := resp.Header.Get("Content-Type")
		mode := str("mode")
		if mode == "raw" || !strings.Contains(ct, "html") {
			if len(body) > lim {
				body = body[:lim] + "\n...(truncated)"
			}
			return body, nil
		}
		result := stripHTML(str("url"), body)
		if len(result) > lim {
			result = result[:lim] + "\n...(truncated)"
		}
		return result, nil

	case "search_web":
		return queryGateway(ctx, "search_web", str("query"))

	case "search_images":
		return queryGateway(ctx, "image_search", str("query"))

	case "query_data":
		return queryGateway(ctx, str("category"), str("extra"))

	case "memory_store":
		if ctx.DB == nil {
			return "", fmt.Errorf("no database")
		}
		key, content := str("key"), str("content")
		cat := str("category")
		if cat == "" {
			cat = "general"
		}
		now := time.Now().UnixMilli()
		_, err := ctx.DB.Exec("INSERT INTO memories(key,content,category,createdAt,updatedAt) VALUES(?,?,?,?,?) ON CONFLICT(key) DO UPDATE SET content=?,category=?,updatedAt=?",
			key, content, cat, now, now, content, cat, now)
		if err != nil {
			return "", err
		}
		return "Stored memory: " + key, nil

	case "memory_recall":
		if ctx.DB == nil {
			return "", fmt.Errorf("no database")
		}
		q := str("query")
		var rows *sql.Rows
		var err error
		if q == "" {
			rows, err = ctx.DB.Query("SELECT key,content,category FROM memories ORDER BY updatedAt DESC")
		} else {
			rows, err = ctx.DB.Query("SELECT key,content,category FROM memories WHERE key LIKE '%'||?||'%' OR content LIKE '%'||?||'%' ORDER BY updatedAt DESC", q, q)
		}
		if err != nil {
			return "", err
		}
		defer rows.Close()
		var b strings.Builder
		for rows.Next() {
			var k, c, cat string
			rows.Scan(&k, &c, &cat)
			fmt.Fprintf(&b, "- %s: %s [%s]\n", k, c, cat)
		}
		if b.Len() == 0 {
			return "No memories found.", nil
		}
		return b.String(), nil

	case "memory_forget":
		if ctx.DB == nil {
			return "", fmt.Errorf("no database")
		}
		key := str("key")
		ctx.DB.Exec("DELETE FROM memories WHERE key=?", key)
		return "Deleted memory: " + key, nil

	case "image_generate":
		return generateImage(ctx, str("prompt"))

	case "analyze_image":
		return analyzeImage(ctx, str("url"), str("question"))

	case "task":
		return executeTask(ctx, str("description"), str("prompt"))

	case "ssh_start":
		server := str("server")
		if ctx.SSHStart != nil {
			return ctx.SSHStart(server)
		}
		client, err := sshConnect(server)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf(`{"status":"connected","server":"%s","remote":"%s"}`, server, client.RemoteAddr()), nil

	case "ssh_exec":
		server := str("server")
		command := str("command")
		timeout := 30
		if t, ok := args["timeout"].(float64); ok && t > 0 {
			timeout = int(t)
		}
		if ctx.SSHExec != nil {
			return ctx.SSHExec(server, command, timeout)
		}
		return sshExec(server, command, timeout)

	case "ssh_exit":
		server := str("server")
		if ctx.SSHExit != nil {
			ctx.SSHExit(server)
			return fmt.Sprintf(`{"status":"disconnected","server":"%s"}`, server), nil
		}
		sshDisconnect(server)
		return fmt.Sprintf(`{"status":"disconnected","server":"%s"}`, server), nil

	default:
		if ctx.McpExecutor != nil {
			if result, handled := ctx.McpExecutor(name, args); handled {
				return result, nil
			}
		}
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}

func queryGateway(ctx *ToolContext, category, extra string) (string, error) {
	if ctx.GatewayURL == "" {
		return "", fmt.Errorf("no gateway configured")
	}
	base := strings.TrimRight(ctx.GatewayURL, "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	base = strings.TrimSuffix(base, "/v1")
	u := fmt.Sprintf("%s/v1/data?q=%s&extra=%s", base, url.QueryEscape(category), url.QueryEscape(extra))
	req, _ := http.NewRequest("GET", u, nil)
	if ctx.GatewayKey != "" {
		req.Header.Set("Authorization", "Bearer "+ctx.GatewayKey)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 12000))
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("gateway %d: %s", resp.StatusCode, string(data))
	}
	return string(data), nil
}

func generateImage(ctx *ToolContext, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt required")
	}
	_, model := ctx.ResolveTaskModel("image_gen")
	if model == "" {
		model = "pollinations-pollen/klein"
	}
	base := strings.TrimRight(ctx.GatewayURL, "/")
	base = strings.TrimSuffix(base, "/chat/completions")
	base = strings.TrimSuffix(base, "/v1")

	body := fmt.Sprintf(`{"model":"%s","prompt":"%s","response_format":"b64_json","seed":%d}`,
		model, strings.ReplaceAll(prompt, `"`, `\"`), time.Now().UnixMilli())

	req, _ := http.NewRequest("POST", base+"/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ctx.GatewayKey != "" {
		req.Header.Set("Authorization", "Bearer "+ctx.GatewayKey)
	}

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("image gen %d: %s", resp.StatusCode, string(data))
	}

	// Parse response — try b64_json first, then URL download
	var result struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	json.Unmarshal(data, &result)
	if len(result.Data) == 0 {
		return "", fmt.Errorf("no image in response: %s", string(data)[:min(len(data), 200)])
	}

	var imgBytes []byte
	if b64 := result.Data[0].B64JSON; b64 != "" {
		imgBytes, _ = base64.StdEncoding.DecodeString(b64)
	} else if u := result.Data[0].URL; u != "" {
		dlReq, _ := http.NewRequest("GET", u, nil)
		if ctx.GatewayKey != "" {
			dlReq.Header.Set("Authorization", "Bearer "+ctx.GatewayKey)
		}
		dlResp, err := client.Do(dlReq)
		if err == nil {
			defer dlResp.Body.Close()
			if dlResp.StatusCode == 200 {
				imgBytes, _ = io.ReadAll(dlResp.Body)
			}
		}
	}

	if len(imgBytes) == 0 {
		return "", fmt.Errorf("failed to download generated image")
	}

	dir := filepath.Join(os.Getenv("HOME"), ".aiope-headless", "generated")
	os.MkdirAll(dir, 0755)
	fname := fmt.Sprintf("img_%d.png", time.Now().UnixMilli())
	fpath := filepath.Join(dir, fname)
	os.WriteFile(fpath, imgBytes, 0644)
	// Copy to user-visible location — model can move/rename this freely
	userDir := filepath.Join(os.Getenv("HOME"), "generated-images")
	os.MkdirAll(userDir, 0755)
	userPath := filepath.Join(userDir, fname)
	os.WriteFile(userPath, imgBytes, 0644)
	ctx.GeneratedImages = append(ctx.GeneratedImages, fpath)
	localURL := fmt.Sprintf("/api/upload?path=%s", fpath)
	return fmt.Sprintf("Image generated and saved to %s\nDisplay with: ![image](%s)", userPath, localURL), nil
}

func analyzeImage(ctx *ToolContext, imgURL, question string) (string, error) {
	if imgURL == "" {
		return "", fmt.Errorf("url required")
	}
	if question == "" {
		question = "Describe this image in detail."
	}

	// Fetch image and base64 encode
	var imgData []byte
	var err error
	if strings.HasPrefix(imgURL, "http://") || strings.HasPrefix(imgURL, "https://") {
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Get(imgURL)
		if err != nil {
			return "", fmt.Errorf("fetch image: %v", err)
		}
		defer resp.Body.Close()
		imgData, err = io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
		if err != nil {
			return "", fmt.Errorf("read image: %v", err)
		}
	} else {
		imgData, err = os.ReadFile(imgURL)
		if err != nil {
			return "", fmt.Errorf("read file: %v", err)
		}
	}
	b64 := base64.StdEncoding.EncodeToString(imgData)

	// Resolve vision model
	gwURL, model := ctx.ResolveTaskModel("image_recognition")
	if model == "" {
		gwURL = ctx.GatewayURL
		model = "google-ai-studio/models-gemma-3-27b-it"
	}

	// Build multimodal message
	msgs := []ChatMessage{{
		Role: "user",
		Content: []any{
			map[string]any{"type": "text", "text": question},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + b64}},
		},
	}}

	prov := &OpenAI{APIKey: ctx.GatewayKey, APIBase: gwURL}
	var result strings.Builder
	err = prov.Stream(context.Background(), msgs, model, nil, func(ev StreamEvent) {
		if ev.Delta != "" {
			result.WriteString(ev.Delta)
		}
	})
	if err != nil {
		return "", fmt.Errorf("vision: %v", err)
	}
	text := result.String()
	if text == "" {
		return "No description returned.", nil
	}
	return fmt.Sprintf("Image analysis complete.\nSource: %s\nResult: %s", imgURL, text), nil
}

var subagentReadOnly = map[string]bool{
	"search_web": true, "search_images": true, "fetch_url": true,
	"read_file": true, "list_directory": true, "query_data": true,
	"memory_recall": true,
}

func executeTask(ctx *ToolContext, description, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt required")
	}

	// Resolve subagent model
	gwURL, model := ctx.ResolveTaskModel("subagent")
	if model == "" {
		gwURL = ctx.GatewayURL
		model = "google-ai-studio/models-gemma-4-26b-a4b-it"
	}

	// Build read-only tool set
	var tools []ToolDef
	for _, t := range BuiltinTools {
		if subagentReadOnly[t.Name] {
			tools = append(tools, t)
		}
	}

	// Build subagent context (read-only)
	subCtx := &ToolContext{
		DB:         ctx.DB,
		GatewayURL: ctx.GatewayURL,
		GatewayKey: ctx.GatewayKey,
	}

	msgs := []ChatMessage{
		{Role: "system", Content: "You are a research subagent. Use your tools to search, read, and explore. Summarize your findings concisely. Do not ask questions."},
		{Role: "user", Content: prompt},
	}

	prov := &OpenAI{APIKey: ctx.GatewayKey, APIBase: gwURL}
	orch := &Orchestrator{
		Provider: prov,
		Model:    model,
		Tools:    tools,
		ToolCtx:  subCtx,
		OnEvent: func(ev StreamEvent) {
			if ctx.OnProgress != nil {
				if ev.Delta != "" {
					ctx.OnProgress("task", "streaming")
				} else if len(ev.ToolCalls) > 0 {
					ctx.OnProgress("task", "tool:"+ev.ToolCalls[0].Name)
				}
			}
		},
	}

	result, err := orch.Run(msgs)
	if err != nil {
		return fmt.Sprintf("<task_error>%s</task_error>", err.Error()), nil
	}
	if result == "" {
		return "<task_result>No results found.</task_result>", nil
	}
	return fmt.Sprintf("<task_result>\n%s\n</task_result>", result), nil
}

var reTag = regexp.MustCompile(`<[^>]+>`)
var reSpaces = regexp.MustCompile(`[ \t]+`)
var reBlankLines = regexp.MustCompile(`\n{3,}`)
var reImg = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["'][^>]*(?:alt=["']([^"']*)["'])?`)
var reOG = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)["']`)

func stripHTML(rawURL, body string) string {
	u, _ := url.Parse(rawURL)
	base := ""
	if u != nil {
		base = u.Scheme + "://" + u.Host
	}
	absURL := func(src string) string {
		if strings.HasPrefix(src, "http") {
			return src
		}
		if strings.HasPrefix(src, "/") {
			return base + src
		}
		return base + "/" + src
	}
	// Extract images
	var imgs []string
	seen := map[string]bool{}
	for _, m := range reImg.FindAllStringSubmatch(body, -1) {
		src := absURL(m[1])
		if seen[src] {
			continue
		}
		seen[src] = true
		alt := "image"
		if len(m) > 2 && m[2] != "" {
			alt = m[2]
			if len(alt) > 80 {
				alt = alt[:80]
			}
		}
		imgs = append(imgs, fmt.Sprintf("![%s](%s)", alt, src))
	}
	for _, m := range reOG.FindAllStringSubmatch(body, -1) {
		src := absURL(m[1])
		if !seen[src] {
			imgs = append(imgs, fmt.Sprintf("![og:image](%s)", src))
			seen[src] = true
		}
	}
	if len(imgs) > 20 {
		imgs = imgs[:20]
	}
	// Strip tags
	text := body
	// Remove script/style/nav/footer/header blocks
	for _, tag := range []string{"script", "style", "nav", "footer", "header"} {
		re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `>`)
		text = re.ReplaceAllString(text, "")
	}
	text = reTag.ReplaceAllString(text, " ")
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")
	text = reSpaces.ReplaceAllString(text, " ")
	lines := strings.Split(text, "\n")
	var out []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l != "" {
			out = append(out, l)
		}
	}
	text = strings.Join(out, "\n")
	text = reBlankLines.ReplaceAllString(text, "\n\n")
	if len(imgs) > 0 {
		return strings.Join(imgs, "\n") + "\n\n" + text
	}
	return text
}
