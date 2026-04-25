package llm

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var ParallelSafe = map[string]bool{
	"read_file": true, "list_directory": true, "fetch_url": true,
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
}

func ExecuteTool(name string, args map[string]any) (string, error) {
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

	default:
		return "", fmt.Errorf("unknown tool: %s", name)
	}
}
