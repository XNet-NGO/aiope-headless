package llm

import "encoding/json"

type ProviderProfile struct {
	BuiltinID       string `json:"builtinId"`
	APIKey          string `json:"apiKey"`
	APIBase         string `json:"apiBase"`
	SelectedModelID string `json:"selectedModelId"`
}

func ProviderFromJSON(data string) (Provider, string) {
	var p ProviderProfile
	if json.Unmarshal([]byte(data), &p) != nil {
		return nil, ""
	}
	return &OpenAI{APIKey: p.APIKey, APIBase: p.APIBase}, p.SelectedModelID
}
