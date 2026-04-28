package remote

import (
	"database/sql"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type Server struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Host          string `json:"host"`
	Port          int    `json:"port"`
	User          string `json:"user"`
	BootstrapPort int    `json:"bootstrapPort"`
	KeyPath       string `json:"keyPath"`
	PrivateKey    string `json:"privateKey,omitempty"`
	Status        string `json:"status"`
	OsInfo        string `json:"osInfo"`
	DaemonVersion string `json:"daemonVersion"`
	LastSeen      int64  `json:"lastSeen"`
	CreatedAt     int64  `json:"createdAt"`
}

type Service struct {
	DB       *sql.DB
	mu       sync.Mutex
	sessions map[string]*ssh.Client
}

func NewService(db *sql.DB) *Service {
	return &Service{DB: db, sessions: map[string]*ssh.Client{}}
}

// CRUD

func (s *Service) List() ([]Server, error) {
	rows, err := s.DB.Query("SELECT id,name,host,port,user,bootstrapPort,keyPath,privateKey,status,osInfo,daemonVersion,lastSeen,createdAt FROM remote_servers ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Server
	for rows.Next() {
		var srv Server
		rows.Scan(&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.User, &srv.BootstrapPort, &srv.KeyPath, &srv.PrivateKey, &srv.Status, &srv.OsInfo, &srv.DaemonVersion, &srv.LastSeen, &srv.CreatedAt)
		out = append(out, srv)
	}
	return out, nil
}

func (s *Service) Get(id string) *Server {
	var srv Server
	err := s.DB.QueryRow("SELECT id,name,host,port,user,bootstrapPort,keyPath,privateKey,status,osInfo,daemonVersion,lastSeen,createdAt FROM remote_servers WHERE id=?", id).
		Scan(&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.User, &srv.BootstrapPort, &srv.KeyPath, &srv.PrivateKey, &srv.Status, &srv.OsInfo, &srv.DaemonVersion, &srv.LastSeen, &srv.CreatedAt)
	if err != nil {
		return nil
	}
	return &srv
}

func (s *Service) GetByName(name string) *Server {
	var srv Server
	err := s.DB.QueryRow("SELECT id,name,host,port,user,bootstrapPort,keyPath,privateKey,status,osInfo,daemonVersion,lastSeen,createdAt FROM remote_servers WHERE name=? COLLATE NOCASE", name).
		Scan(&srv.ID, &srv.Name, &srv.Host, &srv.Port, &srv.User, &srv.BootstrapPort, &srv.KeyPath, &srv.PrivateKey, &srv.Status, &srv.OsInfo, &srv.DaemonVersion, &srv.LastSeen, &srv.CreatedAt)
	if err != nil {
		return nil
	}
	return &srv
}

func (s *Service) Upsert(srv *Server) error {
	if srv.CreatedAt == 0 {
		srv.CreatedAt = time.Now().UnixMilli()
	}
	_, err := s.DB.Exec(`INSERT INTO remote_servers(id,name,host,port,user,bootstrapPort,keyPath,privateKey,status,osInfo,daemonVersion,lastSeen,createdAt)
		VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET name=?,host=?,port=?,user=?,bootstrapPort=?,keyPath=?,privateKey=?,status=?,osInfo=?,daemonVersion=?,lastSeen=?`,
		srv.ID, srv.Name, srv.Host, srv.Port, srv.User, srv.BootstrapPort, srv.KeyPath, srv.PrivateKey, srv.Status, srv.OsInfo, srv.DaemonVersion, srv.LastSeen, srv.CreatedAt,
		srv.Name, srv.Host, srv.Port, srv.User, srv.BootstrapPort, srv.KeyPath, srv.PrivateKey, srv.Status, srv.OsInfo, srv.DaemonVersion, srv.LastSeen)
	return err
}

func (s *Service) Delete(id string) {
	s.Disconnect(id)
	s.DB.Exec("DELETE FROM remote_servers WHERE id=?", id)
}

func (s *Service) UpdateStatus(id, status string) {
	s.DB.Exec("UPDATE remote_servers SET status=?, lastSeen=? WHERE id=?", status, time.Now().UnixMilli(), id)
}

func (s *Service) UpdateHealth(id, osInfo, version string) {
	s.DB.Exec("UPDATE remote_servers SET osInfo=?, daemonVersion=? WHERE id=?", osInfo, version, id)
}

// SSH Sessions

func (s *Service) Connect(srv *Server) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.sessions[srv.ID]; ok {
		if _, _, err := c.SendRequest("keepalive@openssh.com", true, nil); err == nil {
			return nil
		}
		c.Close()
		delete(s.sessions, srv.ID)
	}

	signer, err := s.loadKey(srv)
	if err != nil {
		return err
	}

	home, _ := os.UserHomeDir()
	khPath := filepath.Join(home, ".ssh", "known_hosts")
	var hkCb ssh.HostKeyCallback
	if cb, err := knownhosts.New(khPath); err == nil {
		hkCb = func(hostname string, addr net.Addr, key ssh.PublicKey) error {
			err := cb(hostname, addr, key)
			if err == nil {
				return nil
			}
			// If it's a KeyError with Want entries, the key changed — reject
			var keyErr *knownhosts.KeyError
			if errors.As(err, &keyErr) && len(keyErr.Want) > 0 {
				return err
			}
			return nil // unknown host — accept
		}
	} else {
		hkCb = ssh.InsecureIgnoreHostKey()
	}

	cfg := &ssh.ClientConfig{
		User:            srv.User,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hkCb,
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(srv.Host, fmt.Sprintf("%d", srv.Port))
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return fmt.Errorf("connect %s: %v", addr, err)
	}
	s.sessions[srv.ID] = client
	s.UpdateStatus(srv.ID, "online")
	return nil
}

func (s *Service) Exec(id, command string, timeout int) (string, error) {
	s.mu.Lock()
	client, ok := s.sessions[id]
	s.mu.Unlock()
	if !ok {
		return "", fmt.Errorf("no active session — use ssh_start first")
	}

	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	done := make(chan struct{})
	var out []byte
	var execErr error
	go func() { out, execErr = sess.CombinedOutput(command); close(done) }()

	select {
	case <-done:
	case <-time.After(time.Duration(timeout) * time.Second):
		sess.Close()
		return "", fmt.Errorf("timeout after %ds", timeout)
	}

	result := string(out)
	if len(result) > 4000 {
		result = result[:4000] + "\n...(truncated)"
	}
	if execErr != nil && result == "" {
		return "", execErr
	}
	s.UpdateStatus(id, "online")
	return result, nil
}

func (s *Service) Disconnect(id string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.sessions[id]; ok {
		c.Close()
		delete(s.sessions, id)
	}
	s.UpdateStatus(id, "offline")
}

func (s *Service) IsConnected(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.sessions[id]
	return ok
}

func (s *Service) loadKey(srv *Server) (ssh.Signer, error) {
	// Prefer stored private key
	if srv.PrivateKey != "" {
		return ssh.ParsePrivateKey([]byte(srv.PrivateKey))
	}
	// Fall back to key file path
	if srv.KeyPath != "" {
		data, err := os.ReadFile(srv.KeyPath)
		if err != nil {
			return nil, fmt.Errorf("read key %s: %v", srv.KeyPath, err)
		}
		return ssh.ParsePrivateKey(data)
	}
	// Try default keys
	home, _ := os.UserHomeDir()
	for _, k := range []string{"id_ed25519", "id_rsa"} {
		p := filepath.Join(home, ".ssh", k)
		if data, err := os.ReadFile(p); err == nil {
			if signer, err := ssh.ParsePrivateKey(data); err == nil {
				return signer, nil
			}
		}
	}
	return nil, fmt.Errorf("no SSH key found for %s", srv.Name)
}

// Seed from ~/.ssh/config

func (s *Service) SeedFromSSHConfig() {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return
	}

	type entry struct{ alias, host, user, port, keyFile string }
	var entries []entry
	var cur *entry

	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, " ", 2)
		if len(parts) < 2 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(parts[0]))
		val := strings.TrimSpace(parts[1])
		if key == "host" && !strings.Contains(val, "*") {
			e := entry{alias: val, host: val, port: "22", user: os.Getenv("USER")}
			entries = append(entries, e)
			cur = &entries[len(entries)-1]
		} else if cur != nil {
			switch key {
			case "hostname":
				cur.host = val
			case "user":
				cur.user = val
			case "port":
				cur.port = val
			case "identityfile":
				if strings.HasPrefix(val, "~/") {
					val = filepath.Join(home, val[2:])
				}
				cur.keyFile = val
			}
		}
	}

	for _, e := range entries {
		// Skip if already in DB by name
		if s.GetByName(e.alias) != nil {
			continue
		}
		port := 22
		fmt.Sscanf(e.port, "%d", &port)
		s.Upsert(&Server{
			ID:            "ssh_" + e.alias,
			Name:          e.alias,
			Host:          e.host,
			Port:          port,
			User:          e.user,
			BootstrapPort: port,
			KeyPath:       e.keyFile,
			Status:        "offline",
		})
	}
}

// BuildSystemContext returns server info for the LLM system prompt
func (s *Service) BuildSystemContext() string {
	servers, _ := s.List()
	if len(servers) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n## Remote Servers\nAvailable servers (use ssh_start/ssh_exec/ssh_exit):\n")
	for _, srv := range servers {
		status := srv.Status
		if s.IsConnected(srv.ID) {
			status = "CONNECTED"
		}
		b.WriteString(fmt.Sprintf("- %s (%s@%s:%d) [%s]", srv.Name, srv.User, srv.Host, srv.Port, status))
		if srv.OsInfo != "" {
			b.WriteString(" " + srv.OsInfo)
		}
		b.WriteString("\n")
	}
	return b.String()
}
