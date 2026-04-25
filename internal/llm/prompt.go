package llm

import "strings"

var agentSections = []struct {
	Prefix string
	Title  string
	Keys   []string
}{
	{"identity", "Identity", []string{"name_role", "personality", "tone"}},
	{"values", "Values & Rules", []string{"principles", "constraints"}},
	{"preferences", "Preferences", []string{"response_style", "formatting"}},
	{"context", "Context", []string{"user_info", "environment", "projects"}},
	{"tools", "Tools", []string{"tool_guidance"}},
}

var modePrefixes = map[string]string{
	"plan":  "You are in PLAN mode. Analyze the request, explore relevant context, and produce a clear numbered plan. Do NOT execute changes.\n\n",
	"build": "Execute autonomously. Do not ask for confirmation. Chain tools to complete the goal.\n\n",
}

func BuildSystemPrompt(settings map[string]string, mode string) string {
	var b strings.Builder
	if prefix, ok := modePrefixes[mode]; ok {
		b.WriteString(prefix)
	}
	for _, sec := range agentSections {
		var parts []string
		for _, key := range sec.Keys {
			if v := settings["agent_"+sec.Prefix+"_"+key]; v != "" {
				parts = append(parts, v)
			}
		}
		if len(parts) > 0 {
			b.WriteString("## " + sec.Title + "\n")
			b.WriteString(strings.Join(parts, "\n\n"))
			b.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(b.String())
}
