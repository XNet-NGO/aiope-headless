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
	"search_web": true, "search_images": true,
	"memory_recall": true, "memory_store": true, "ssh_exec": true,
	"analyze_image": true, "task": true,
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
		Name: "fetch_url", Description: "Fetch a URL and return its content. Supports three modes: 'text' (default) extracts readable text, 'md' converts HTML to markdown preserving links/headings/lists, 'raw' returns the raw response body.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url":  map[string]any{"type": "string"},
				"mode": map[string]any{"type": "string", "enum": []string{"text", "md", "raw"}, "description": "text (default): extracted readable text. md: HTML→markdown with links/headings. raw: raw response body."},
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
	APIBase          string
	APIKey           string
	SearxURL         string
	ShellOutputLimit int
	FetchLimit       int
	FileReadLimit    int
	McpExecutor      func(name string, args map[string]any) (string, bool)
	SSHStart         func(server string) (string, error)
	SSHExec          func(server, command string, timeout int) (string, error)
	SSHExit          func(server string) error
	OnProgress       func(toolName, status string) // stream tool progress to client
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
var TaskModelDefaults = map[string]string{}

func (ctx *ToolContext) ResolveTaskModel(task string) (string, string) {
	// Check settings_kv for override: task_model_{task} = "model_id"
	if ctx.DB != nil {
		var model string
		if err := ctx.DB.QueryRow("SELECT value FROM settings_kv WHERE key=?", "task_model_"+task).Scan(&model); err == nil && model != "" {
			return ctx.APIBase, model
		}
	}
	if m, ok := TaskModelDefaults[task]; ok {
		return ctx.APIBase, m
	}
	return ctx.APIBase, ""
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
		if mode == "" {
			mode = "text"
		}
		if mode == "raw" || !strings.Contains(ct, "html") {
			if len(body) > lim {
				body = body[:lim] + "\n...(truncated)"
			}
			return body, nil
		}
		var result string
		if mode == "md" {
			result = htmlToMarkdown(str("url"), body)
		} else {
			result = stripHTML(str("url"), body)
		}
		if len(result) > lim {
			result = result[:lim] + "\n...(truncated)"
		}
		return result, nil

	case "search_web":
		return searxQuery(ctx, str("query"), "")

	case "search_images":
		return searxQuery(ctx, str("query"), "images")

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
		gwURL = ctx.APIBase
		model = "gpt-4o"
	}

	// Build multimodal message
	msgs := []ChatMessage{{
		Role: "user",
		Content: []any{
			map[string]any{"type": "text", "text": question},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/jpeg;base64," + b64}},
		},
	}}

	prov := &OpenAI{APIKey: ctx.APIKey, APIBase: gwURL}
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
	"search_web": true, "search_images": true,
	"fetch_url": true, "read_file": true, "list_directory": true,
	"memory_recall": true,
}

func executeTask(ctx *ToolContext, description, prompt string) (string, error) {
	if prompt == "" {
		return "", fmt.Errorf("prompt required")
	}

	// Resolve subagent model
	gwURL, model := ctx.ResolveTaskModel("subagent")
	if model == "" {
		gwURL = ctx.APIBase
		model = "gpt-4o"
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
		DB:      ctx.DB,
		APIBase: ctx.APIBase,
		APIKey:  ctx.APIKey,
	}

	msgs := []ChatMessage{
		{Role: "system", Content: "You are a research subagent. Use your tools to search, read, and explore. Summarize your findings concisely. Do not ask questions."},
		{Role: "user", Content: prompt},
	}

	prov := &OpenAI{APIKey: ctx.APIKey, APIBase: gwURL}
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

const defaultSearxURL = "https://search.xnet.ngo"

func (ctx *ToolContext) resolveSearxURL() string {
	// 1. Check ToolContext field
	if ctx.SearxURL != "" {
		return ctx.SearxURL
	}
	// 2. Check settings_kv
	if ctx.DB != nil {
		var v string
		if err := ctx.DB.QueryRow("SELECT value FROM settings_kv WHERE key='search_provider_url'").Scan(&v); err == nil && v != "" {
			return v
		}
	}
	// 3. Env var
	if v := os.Getenv("SEARCH_PROVIDER_URL"); v != "" {
		return v
	}
	return defaultSearxURL
}

func searxQuery(ctx *ToolContext, query, categories string) (string, error) {
	if query == "" {
		return "", fmt.Errorf("query required")
	}
	base := strings.TrimRight(ctx.resolveSearxURL(), "/")
	u := fmt.Sprintf("%s/search?q=%s&format=json", base, url.QueryEscape(query))
	if categories != "" {
		u += "&categories=" + url.QueryEscape(categories)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Get(u)
	if err != nil {
		return "", fmt.Errorf("search: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 500))
		return "", fmt.Errorf("search %d: %s", resp.StatusCode, string(body))
	}
	var data struct {
		Results []struct {
			Title        string `json:"title"`
			URL          string `json:"url"`
			Content      string `json:"content"`
			ImgSrc       string `json:"img_src"`
			ThumbnailSrc string `json:"thumbnail_src"`
		} `json:"results"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 50000)).Decode(&data); err != nil {
		return "", fmt.Errorf("search parse: %v", err)
	}
	if len(data.Results) == 0 {
		return "No results found.", nil
	}
	var b strings.Builder
	limit := 10
	if categories == "images" {
		limit = 20
	}
	if len(data.Results) < limit {
		limit = len(data.Results)
	}
	for _, r := range data.Results[:limit] {
		if categories == "images" {
			img := r.ImgSrc
			if img == "" {
				img = r.ThumbnailSrc
			}
			if img != "" {
				fmt.Fprintf(&b, "- %s\n  %s\n  %s\n", r.Title, img, r.URL)
			}
		} else {
			fmt.Fprintf(&b, "- %s\n  %s\n  %s\n", r.Title, r.URL, r.Content)
		}
	}
	return b.String(), nil
}

var reTag = regexp.MustCompile(`<[^>]+>`)
var reSpaces = regexp.MustCompile(`[ \t]+`)
var reBlankLines = regexp.MustCompile(`\n{3,}`)
var reImg = regexp.MustCompile(`(?i)<img[^>]+src=["']([^"']+)["'][^>]*(?:alt=["']([^"']*)["'])?`)
var reOG = regexp.MustCompile(`(?i)<meta[^>]+property=["']og:image["'][^>]+content=["']([^"']+)["']`)

func htmlToMarkdown(rawURL, body string) string {
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

	// Remove script/style/nav/footer
	text := body
	for _, tag := range []string{"script", "style", "nav", "footer"} {
		re := regexp.MustCompile(`(?is)<` + tag + `\b[^>]*>.*?</` + tag + `>`)
		text = re.ReplaceAllString(text, "")
	}

	// Convert headings
	for i := 6; i >= 1; i-- {
		prefix := strings.Repeat("#", i)
		re := regexp.MustCompile(fmt.Sprintf(`(?is)<h%d[^>]*>(.*?)</h%d>`, i, i))
		text = re.ReplaceAllStringFunc(text, func(m string) string {
			inner := re.FindStringSubmatch(m)
			if len(inner) > 1 {
				return "\n" + prefix + " " + reTag.ReplaceAllString(inner[1], "") + "\n"
			}
			return m
		})
	}

	// Convert links
	reLink := regexp.MustCompile(`(?is)<a\b[^>]*href="([^"]*)"[^>]*>(.*?)</a>`)
	text = reLink.ReplaceAllStringFunc(text, func(m string) string {
		parts := reLink.FindStringSubmatch(m)
		if len(parts) > 2 {
			href := absURL(parts[1])
			label := strings.TrimSpace(reTag.ReplaceAllString(parts[2], ""))
			if label == "" {
				label = href
			}
			return "[" + label + "](" + href + ")"
		}
		return m
	})

	// Convert images
	reImgTag := regexp.MustCompile(`(?is)<img\b[^>]*src="([^"]*)"[^>]*>`)
	text = reImgTag.ReplaceAllStringFunc(text, func(m string) string {
		parts := reImgTag.FindStringSubmatch(m)
		if len(parts) > 1 {
			src := absURL(parts[1])
			alt := "image"
			if altMatch := regexp.MustCompile(`alt="([^"]*)"`).FindStringSubmatch(m); len(altMatch) > 1 {
				alt = altMatch[1]
			}
			return "![" + alt + "](" + src + ")"
		}
		return m
	})

	// Convert lists
	text = regexp.MustCompile(`(?is)<li[^>]*>`).ReplaceAllString(text, "\n- ")
	text = regexp.MustCompile(`(?is)</li>`).ReplaceAllString(text, "")
	text = regexp.MustCompile(`(?is)</?[ou]l[^>]*>`).ReplaceAllString(text, "\n")

	// Convert paragraphs and breaks
	text = regexp.MustCompile(`(?is)<br\s*/?\s*>`).ReplaceAllString(text, "\n")
	text = regexp.MustCompile(`(?is)</?p[^>]*>`).ReplaceAllString(text, "\n")
	text = regexp.MustCompile(`(?is)</?div[^>]*>`).ReplaceAllString(text, "\n")

	// Convert bold/italic
	text = regexp.MustCompile(`(?is)<(?:b|strong)[^>]*>(.*?)</(?:b|strong)>`).ReplaceAllString(text, "**$1**")
	text = regexp.MustCompile(`(?is)<(?:i|em)[^>]*>(.*?)</(?:i|em)>`).ReplaceAllString(text, "*$1*")

	// Convert code
	text = regexp.MustCompile(`(?is)<code[^>]*>(.*?)</code>`).ReplaceAllString(text, "`$1`")
	text = regexp.MustCompile(`(?is)<pre[^>]*>(.*?)</pre>`).ReplaceAllString(text, "\n```\n$1\n```\n")

	// Strip remaining tags
	text = reTag.ReplaceAllString(text, "")

	// Decode entities
	text = strings.ReplaceAll(text, "&nbsp;", " ")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	text = strings.ReplaceAll(text, "&quot;", "\"")
	text = strings.ReplaceAll(text, "&#39;", "'")

	// Clean up whitespace
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
	return text
}

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
