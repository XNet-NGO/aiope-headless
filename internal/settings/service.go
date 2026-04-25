package settings

import "database/sql"

type Service struct{ DB *sql.DB }

func (s *Service) Get(key string) (string, error) {
	var v string
	err := s.DB.QueryRow("SELECT value FROM settings_kv WHERE key=?", key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *Service) Set(key, value string) error {
	_, err := s.DB.Exec("INSERT INTO settings_kv(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=?", key, value, value)
	return err
}

func (s *Service) All() (map[string]string, error) {
	rows, err := s.DB.Query("SELECT key,value FROM settings_kv")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var k, v string
		rows.Scan(&k, &v)
		out[k] = v
	}
	return out, nil
}
