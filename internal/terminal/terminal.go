package terminal

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

const (
	MsgData      = 0x01
	MsgResize    = 0x04
	MsgHeartbeat = 0x07
	ringSize     = 64 * 1024
)

type Session struct {
	mu   sync.Mutex
	ptmx *os.File
	cmd  *exec.Cmd
	ring [ringSize]byte
	rpos int
	rlen int
	conn *websocket.Conn
}

var (
	sessions   = make(map[string]*Session)
	sessionsMu sync.Mutex
	sessionSeq int
)

func getSession(id string) *Session {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	return sessions[id]
}

func NewSession() (string, *Session) {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	sessionSeq++
	id := fmt.Sprintf("%d", sessionSeq)
	s := &Session{}
	sessions[id] = s
	return id, s
}

func DeleteSession(id string) {
	sessionsMu.Lock()
	s := sessions[id]
	delete(sessions, id)
	sessionsMu.Unlock()
	if s != nil {
		s.mu.Lock()
		if s.ptmx != nil {
			s.ptmx.Close()
		}
		if s.cmd != nil && s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
		s.mu.Unlock()
	}
}

func ListSessions() []string {
	sessionsMu.Lock()
	defer sessionsMu.Unlock()
	ids := make([]string, 0, len(sessions))
	for id, s := range sessions {
		s.mu.Lock()
		alive := s.ptmx != nil
		s.mu.Unlock()
		if alive {
			ids = append(ids, id)
		}
	}
	return ids
}

func (s *Session) Init() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.ptmx != nil {
		return nil
	}
	cmd := exec.Command("/bin/bash", "-l")
	cmd.Dir = os.Getenv("HOME")
	cmd.Env = append(os.Environ(), "TERM=xterm-256color", "LANG=en_US.UTF-8")
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 120, Rows: 30})
	if err != nil {
		return err
	}
	s.ptmx = ptmx
	s.cmd = cmd

	go func() {
		buf := make([]byte, 32769)
		buf[0] = MsgData
		for {
			n, err := ptmx.Read(buf[1:])
			if err != nil {
				s.mu.Lock()
				s.ptmx = nil
				s.cmd = nil
				s.rlen = 0
				s.rpos = 0
				s.mu.Unlock()
				return
			}
			s.mu.Lock()
			for i := 0; i < n; i++ {
				s.ring[s.rpos] = buf[1+i]
				s.rpos = (s.rpos + 1) % ringSize
			}
			s.rlen += n
			if s.rlen > ringSize {
				s.rlen = ringSize
			}
			c := s.conn
			s.mu.Unlock()
			if c != nil {
				msg := make([]byte, n+1)
				msg[0] = MsgData
				copy(msg[1:], buf[1:n+1])
				c.Write(context.Background(), websocket.MessageBinary, msg)
			}
		}
	}()
	return nil
}

func (s *Session) replay(conn *websocket.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.rlen == 0 {
		return
	}
	size := s.rlen
	if size > ringSize {
		size = ringSize
	}
	start := (s.rpos - size + ringSize) % ringSize
	buf := make([]byte, 1+size)
	buf[0] = MsgData
	for i := 0; i < size; i++ {
		buf[1+i] = s.ring[(start+i)%ringSize]
	}
	conn.Write(context.Background(), websocket.MessageBinary, buf)
}

func HandleTerm(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Upgrade") == "" {
		handleREST(w, r)
		return
	}
	id := r.URL.Query().Get("id")
	sess := getSession(id)
	if sess == nil {
		http.Error(w, "no such session", 404)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
	if err != nil {
		return
	}
	defer conn.CloseNow()

	// Read initial resize
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	_, msg, err2 := conn.Read(ctx)
	cancel()
	if err2 == nil && len(msg) >= 5 && msg[0] == MsgResize {
		cols := uint16(msg[1])<<8 | uint16(msg[2])
		rows := uint16(msg[3])<<8 | uint16(msg[4])
		if cols > 0 && cols < 500 && rows > 0 && rows < 200 {
			sess.mu.Lock()
			if sess.ptmx != nil {
				pty.Setsize(sess.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
			}
			sess.mu.Unlock()
		}
	}

	sess.replay(conn)
	sess.mu.Lock()
	sess.conn = conn
	sess.mu.Unlock()
	defer func() {
		sess.mu.Lock()
		if sess.conn == conn {
			sess.conn = nil
		}
		sess.mu.Unlock()
	}()

	// Server-side ping — detects dead connections
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(25 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				ctx, c := context.WithTimeout(context.Background(), 10*time.Second)
				err := conn.Ping(ctx)
				c()
				if err != nil {
					conn.CloseNow()
					return
				}
			}
		}
	}()

	for {
		_, msg, err := conn.Read(context.Background())
		if err != nil {
			break
		}
		if len(msg) == 0 {
			continue
		}
		switch msg[0] {
		case MsgData:
			sess.mu.Lock()
			if sess.ptmx != nil && len(msg) > 1 {
				sess.ptmx.Write(msg[1:])
			}
			sess.mu.Unlock()
		case MsgResize:
			if len(msg) >= 5 {
				cols := uint16(msg[1])<<8 | uint16(msg[2])
				rows := uint16(msg[3])<<8 | uint16(msg[4])
				sess.mu.Lock()
				if sess.ptmx != nil && cols > 0 && cols < 500 && rows > 0 && rows < 200 {
					pty.Setsize(sess.ptmx, &pty.Winsize{Cols: cols, Rows: rows})
				}
				sess.mu.Unlock()
			}
		case MsgHeartbeat:
			conn.Write(context.Background(), websocket.MessageBinary, []byte{MsgHeartbeat})
		}
	}
	close(done)
}

func handleREST(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Query().Get("action") {
	case "new":
		id, s := NewSession()
		if err := s.Init(); err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		fmt.Fprintf(w, `{"id":"%s"}`, id)
	case "close":
		DeleteSession(r.URL.Query().Get("id"))
		fmt.Fprint(w, `{"ok":true}`)
	case "list":
		b, _ := json.Marshal(ListSessions())
		w.Write(b)
	default:
		http.Error(w, "bad request", 400)
	}
}
