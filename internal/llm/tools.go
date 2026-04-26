package llm

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ParallelSafe = map[string]bool{
	"read_file": true, "list_directory": true, "fetch_url": true,
	"search_web": true, "search_images": true, "query_data": true,
	"memory_recall": true, "ssh_exec": true,
}

var BuiltinTools = []ToolDef{
	{
		Name: "run_sh", Description: "Execute a shell command and return stdout+stderr",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
			},
			"required": []string{"command"},
		},
	},
	{
		Name: "read_file", Description: "Read the contents of a file",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "File path to read"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name: "write_file", Description: "Write content to a file, creating directories as needed",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path":    map[string]any{"type": "string", "description": "File path to write"},
				"content": map[string]any{"type": "string", "description": "Content to write"},
			},
			"required": []string{"path", "content"},
		},
	},
	{
		Name: "list_directory", Description: "List files and directories at a path",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"path": map[string]any{"type": "string", "description": "Directory path to list"},
			},
			"required": []string{"path"},
		},
	},
	{
		Name: "fetch_url", Description: "Fetch the text content of a URL",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"url": map[string]any{"type": "string", "description": "URL to fetch"},
			},
			"required": []string{"url"},
		},
	},
	{
		Name: "search_web", Description: "Search the web for current information, news, answers, or any topic. Returns results with titles, URLs, and snippets.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search query"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "search_images", Description: "Search for images on the web. Returns image URLs with titles.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Image search query"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "query_data", Description: "Query live real-time data from the AIOPE Gateway. Returns JSON. Available categories: air_quality, alerts, apod, asteroids, astronauts, cat, cat_breed, cat_breeds, cme, earth_events, earth_image, earthquakes, earthquakes_significant, epic, fires, geomagnetic, impact_risk, ip_location, iss, nasa_media, nasa_tech, ocean_temp, solar, solar_flares, sunrise_sunset, tides, time, uv, weather, weather_hourly",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"category": map[string]any{"type": "string", "description": "Data category"},
				"extra":    map[string]any{"type": "string", "description": "Optional: search query, station ID, or breed ID depending on category"},
			},
			"required": []string{"category"},
		},
	},
	{
		Name: "memory_store", Description: "Store a fact or preference to remember across conversations. Use a short descriptive key.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key":      map[string]any{"type": "string", "description": "Short key like 'user_name' or 'preferred_language'"},
				"content":  map[string]any{"type": "string", "description": "The fact to remember"},
				"category": map[string]any{"type": "string", "description": "Optional: general, preference, learning, error"},
			},
			"required": []string{"key", "content"},
		},
	},
	{
		Name: "memory_recall", Description: "Search persistent memory for stored facts. Empty query lists all memories.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"query": map[string]any{"type": "string", "description": "Search term, or empty to list all"},
			},
			"required": []string{"query"},
		},
	},
	{
		Name: "memory_forget", Description: "Delete a specific memory by its key.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"key": map[string]any{"type": "string", "description": "Key of the memory to delete"},
			},
			"required": []string{"key"},
		},
	},
	{
		Name: "image_generate", Description: "Generate an image from a text prompt. Returns a URL to the generated image. Include the URL in your response as ![description](url) to display it.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{"type": "string", "description": "Detailed image generation prompt"},
			},
			"required": []string{"prompt"},
		},
	},
	{
		Name: "ssh_start", Description: "Open persistent SSH session to a remote server. Resolves from ~/.ssh/config or connects by hostname. Returns connection status.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "Server name (from ~/.ssh/config) or hostname"},
			},
			"required": []string{"server"},
		},
	},
	{
		Name: "ssh_exec", Description: "Execute a command on an active remote SSH session. Returns stdout+stderr. Use ssh_start first.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server":  map[string]any{"type": "string", "description": "Server name"},
				"command": map[string]any{"type": "string", "description": "Shell command to execute"},
				"timeout": map[string]any{"type": "integer", "description": "Timeout in seconds (default 30)"},
			},
			"required": []string{"server", "command"},
		},
	},
	{
		Name: "ssh_exit", Description: "Close an active SSH session.",
		Parameters: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"server": map[string]any{"type": "string", "description": "Server name"},
			},
			"required": []string{"server"},
		},
	},
}

type ToolContext struct {
	DB         *sql.DB
	GatewayURL string
	GatewayKey string
}

// Task model resolution — different models for different tasks
var TaskModelDefaults = map[string]string{
	"title":       "google-ai-studio/models-gemma-3-1b-it",
	"summary":     "google-ai-studio/models-gemma-3-27b-it",
	"image_gen":   "pollinations-pollen/klein",
	"subagent":    "google-ai-studio/models-gemma-4-26b-a4b-it",
	"translation": "google-ai-studio/models-gemma-3-12b-it",
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
		cmd := exec.Command("bash", "-c", str("command"))
		cmd.Env = os.Environ()
		out, err := cmd.CombinedOutput()
		result := string(out)
		if len(result) > 4000 {
			result = result[:4000] + "\n...(truncated)"
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
		if len(s) > 50000 {
			s = s[:50000] + "\n...(truncated)"
		}
		return s, nil

	case "write_file":
		p := str("path")
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
		resp, err := client.Get(str("url"))
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 12000))
		return string(data), nil

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

	case "ssh_start":
		server := str("server")
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
		return sshExec(server, command, timeout)

	case "ssh_exit":
		server := str("server")
		sshDisconnect(server)
		return fmt.Sprintf(`{"status":"disconnected","server":"%s"}`, server), nil

	default:
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

	body := fmt.Sprintf(`{"model":"%s","prompt":"%s","response_format":"url","seed":%d}`,
		model, strings.ReplaceAll(prompt, `"`, `\"`), time.Now().UnixMilli())

	req, _ := http.NewRequest("POST", base+"/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if ctx.GatewayKey != "" {
		req.Header.Set("Authorization", "Bearer "+ctx.GatewayKey)
	}

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("image gen %d: %s", resp.StatusCode, string(data))
	}

	var result struct {
		Data []struct {
			URL     string `json:"url"`
			B64JSON string `json:"b64_json"`
		} `json:"data"`
	}
	json.Unmarshal(data, &result)
	if len(result.Data) > 0 && result.Data[0].URL != "" {
		return fmt.Sprintf("Image generated!\n![generated image](%s)", result.Data[0].URL), nil
	}
	return "", fmt.Errorf("no image in response: %s", string(data)[:200])
}
