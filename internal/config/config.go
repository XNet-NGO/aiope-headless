package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type Config struct {
	Port     int    `json:"port"`
	Bind     string `json:"bind"`
	DBPath   string `json:"db_path"`
	SyncURL  string `json:"sync_url"`
	Password string `json:"password"`
	BasePath string `json:"base_path"`
}

func Load() *Config {
	c := &Config{Port: 8090}
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".aiope-headless")
	os.MkdirAll(dir, 0755)
	c.DBPath = filepath.Join(dir, "aiope2-chat.db")

	f, err := os.Open(filepath.Join(dir, "config.json"))
	if err == nil {
		defer f.Close()
		json.NewDecoder(f).Decode(c)
	}
	if v := os.Getenv("AIOPE_PORT"); v != "" {
		json.Unmarshal([]byte(v), &c.Port)
	}
	if v := os.Getenv("AIOPE_BIND"); v != "" {
		c.Bind = v
	}
	if v := os.Getenv("AIOPE_DB_PATH"); v != "" {
		c.DBPath = v
	}
	if v := os.Getenv("AIOPE_SYNC_URL"); v != "" {
		c.SyncURL = v
	}
	if v := os.Getenv("AIOPE_PASSWORD"); v != "" {
		c.Password = v
	}
	if v := os.Getenv("AIOPE_BASE_PATH"); v != "" {
		c.BasePath = v
	}
	return c
}
