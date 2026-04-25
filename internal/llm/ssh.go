package llm

import (
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

var sshSessions = struct {
	sync.Mutex
	m map[string]*ssh.Client
}{m: map[string]*ssh.Client{}}

// sshConfig parsed from ~/.ssh/config
type sshHost struct {
	Host, User, Port string
	IdentityFile     string
}

func parseSSHConfig() map[string]*sshHost {
	home, _ := os.UserHomeDir()
	data, err := os.ReadFile(filepath.Join(home, ".ssh", "config"))
	if err != nil {
		return nil
	}
	hosts := map[string]*sshHost{}
	var cur *sshHost
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
		if key == "host" {
			cur = &sshHost{Host: val, Port: "22"}
			hosts[val] = cur
		} else if cur != nil {
			switch key {
			case "hostname":
				cur.Host = val
			case "user":
				cur.User = val
			case "port":
				cur.Port = val
			case "identityfile":
				if strings.HasPrefix(val, "~/") {
					h, _ := os.UserHomeDir()
					val = filepath.Join(h, val[2:])
				}
				cur.IdentityFile = val
			}
		}
	}
	return hosts
}

func resolveSSHHost(name string) (host, port, user, keyFile string) {
	home, _ := os.UserHomeDir()

	// Handle user@host format
	if i := strings.Index(name, "@"); i >= 0 {
		user = name[:i]
		name = name[i+1:]
	}

	// Handle host:port format
	if h, p, err := net.SplitHostPort(name); err == nil {
		name = h
		port = p
	}

	hosts := parseSSHConfig()
	if h, ok := hosts[name]; ok {
		host = h.Host
		if port == "" {
			port = h.Port
		}
		if user == "" {
			user = h.User
		}
		if keyFile == "" {
			keyFile = h.IdentityFile
		}
	} else {
		host = name
	}
	if port == "" {
		port = "22"
	}
	if user == "" {
		user = os.Getenv("USER")
	}
	if keyFile == "" {
		for _, k := range []string{"id_ed25519", "id_rsa"} {
			p := filepath.Join(home, ".ssh", k)
			if _, err := os.Stat(p); err == nil {
				keyFile = p
				break
			}
		}
	}
	return
}

func sshConnect(name string) (*ssh.Client, error) {
	sshSessions.Lock()
	defer sshSessions.Unlock()
	if c, ok := sshSessions.m[name]; ok {
		// Check if still alive
		_, _, err := c.SendRequest("keepalive@openssh.com", true, nil)
		if err == nil {
			return c, nil
		}
		c.Close()
		delete(sshSessions.m, name)
	}

	host, port, user, keyFile := resolveSSHHost(name)
	if keyFile == "" {
		return nil, fmt.Errorf("no SSH key found for %s", name)
	}
	keyData, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read key %s: %v", keyFile, err)
	}
	signer, err := ssh.ParsePrivateKey(keyData)
	if err != nil {
		return nil, fmt.Errorf("parse key: %v", err)
	}

	home, _ := os.UserHomeDir()
	khPath := filepath.Join(home, ".ssh", "known_hosts")
	var hostKeyCallback ssh.HostKeyCallback
	if cb, err := knownhosts.New(khPath); err == nil {
		hostKeyCallback = cb
	} else {
		hostKeyCallback = ssh.InsecureIgnoreHostKey()
	}

	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKeyCallback,
		Timeout:         10 * time.Second,
	}

	addr := net.JoinHostPort(host, port)
	client, err := ssh.Dial("tcp", addr, cfg)
	if err != nil {
		return nil, fmt.Errorf("connect %s: %v", addr, err)
	}
	sshSessions.m[name] = client
	return client, nil
}

func sshExec(name, command string, timeout int) (string, error) {
	sshSessions.Lock()
	client, ok := sshSessions.m[name]
	sshSessions.Unlock()
	if !ok {
		return "", fmt.Errorf("no active session for %s — use ssh_start first", name)
	}

	sess, err := client.NewSession()
	if err != nil {
		return "", err
	}
	defer sess.Close()

	done := make(chan struct{})
	var out []byte
	var execErr error
	go func() {
		out, execErr = sess.CombinedOutput(command)
		close(done)
	}()

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
	return result, nil
}

func sshDisconnect(name string) {
	sshSessions.Lock()
	defer sshSessions.Unlock()
	if c, ok := sshSessions.m[name]; ok {
		c.Close()
		delete(sshSessions.m, name)
	}
}

func sshListSessions() string {
	sshSessions.Lock()
	defer sshSessions.Unlock()
	if len(sshSessions.m) == 0 {
		return "No active SSH sessions."
	}
	var b strings.Builder
	for name, c := range sshSessions.m {
		b.WriteString(fmt.Sprintf("- %s (%s)\n", name, c.RemoteAddr()))
	}
	return b.String()
}
