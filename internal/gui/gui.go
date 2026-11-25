// Package gui serves the dualhot app window: a small local web page.
package gui

import (
	"bytes"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/manuutechy/dualhot/internal/doctor"
	"github.com/manuutechy/dualhot/internal/hotspot"
	"github.com/manuutechy/dualhot/internal/sys"
)

//go:embed index.html
var page string

type saved struct {
	SSID string `json:"ssid"`
	Pass string `json:"pass"`
}

type server struct {
	self, token, addr string
	mu                sync.Mutex
	starting          bool
	lastErr           string
	cfg               saved
}

func cfgPath() string {
	d, _ := os.UserConfigDir()
	return filepath.Join(d, "dualhot", "gui.json")
}

func (s *server) loadCfg() {
	if b, err := os.ReadFile(cfgPath()); err == nil {
		_ = json.Unmarshal(b, &s.cfg)
	}
	if s.cfg.SSID == "" {
		s.cfg.SSID = hotspot.DefaultSSID()
	}
	if len(s.cfg.Pass) < 8 {
		s.cfg.Pass = hotspot.RandomPass(10)
	}
	s.saveCfg()
}

func (s *server) saveCfg() {
	_ = os.MkdirAll(filepath.Dir(cfgPath()), 0700)
	b, _ := json.Marshal(s.cfg)
	_ = os.WriteFile(cfgPath(), b, 0600)
}

// Run starts the app window and blocks.
func Run(self string) error {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	t := make([]byte, 16)
	_, _ = rand.Read(t)
	s := &server{self: self, token: hex.EncodeToString(t), addr: ln.Addr().String()}
	s.loadCfg()

	mux := http.NewServeMux()
	mux.HandleFunc("/", s.index)
	mux.HandleFunc("/api/state", s.guard(s.state))
	mux.HandleFunc("/api/doctor", s.guard(s.doctor))
	mux.HandleFunc("/api/start", s.guard(s.start))
	mux.HandleFunc("/api/stop", s.guard(s.stop))

	url := fmt.Sprintf("http://%s/?t=%s", s.addr, s.token)
	fmt.Println("dualhot app is open at:", url)
	fmt.Println("(keep this window open; press Ctrl+C to quit the app. A running hotspot keeps running.)")
	go func() {
		time.Sleep(300 * time.Millisecond)
		if sys.Has("xdg-open") && os.Getenv("DUALHOT_NO_OPEN") == "" {
			_ = exec.Command("xdg-open", url).Start()
		}
	}()
	return http.Serve(ln, mux)
}

// guard blocks other websites and other local users: right Host + secret token.
func (s *server) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Host != s.addr || r.Header.Get("X-Token") != s.token {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		h(w, r)
	}
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.Host != s.addr || r.URL.Path != "/" || r.URL.Query().Get("t") != s.token {
		http.Error(w, "Open the link printed by `dualhot gui`.", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(strings.ReplaceAll(page, "__TOKEN__", s.token)))
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func (s *server) state(w http.ResponseWriter, r *http.Request) {
	st := hotspot.LoadState()
	running := st != nil && hotspot.Alive(st.PID)
	s.mu.Lock()
	out := map[string]any{"running": running, "starting": s.starting, "error": s.lastErr,
		"saved": map[string]string{"ssid": s.cfg.SSID, "pass": s.cfg.Pass}}
	s.mu.Unlock()
	if running {
		out["ssid"], out["mode"], out["channel"] = st.SSID, st.Mode, st.Channel
		out["seconds"] = time.Now().Unix() - st.Started
		out["devices"] = hotspot.Leases()
	}
	writeJSON(w, out)
}

func (s *server) doctor(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, doctor.Run(sys.Runner{}))
}

func (s *server) start(w http.ResponseWriter, r *http.Request) {
	var in saved
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		http.Error(w, "bad request", 400)
		return
	}
	cfg := hotspot.Config{SSID: strings.TrimSpace(in.SSID), Pass: in.Pass}
	if err := cfg.Validate(); err != nil {
		writeJSON(w, map[string]string{"error": err.Error()})
		return
	}
	s.mu.Lock()
	if s.starting {
		s.mu.Unlock()
		writeJSON(w, map[string]string{"error": "already starting"})
		return
	}
	s.starting, s.lastErr = true, ""
	s.cfg = saved{cfg.SSID, cfg.Pass}
	s.saveCfg()
	s.mu.Unlock()

	if !sys.Has("pkexec") {
		s.finish("The password prompt needs polkit (pkexec). Install polkit, or run `sudo dualhot start` in a terminal.")
		writeJSON(w, map[string]string{"error": s.lastErr})
		return
	}
	cmd := exec.Command("pkexec", s.self, "start", "--ssid", cfg.SSID, "--pass-stdin")
	cmd.Stdin = strings.NewReader(cfg.Pass + "\n")
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Start(); err != nil {
		s.finish("Could not open the password prompt: " + err.Error())
		writeJSON(w, map[string]string{"error": s.lastErr})
		return
	}
	go func() {
		err := cmd.Wait()
		msg := ""
		if err != nil {
			code := cmd.ProcessState.ExitCode()
			switch code {
			case 126:
				msg = "The password prompt was closed, so nothing started."
			case 127:
				msg = "Your account is not allowed to do this. Ask an administrator."
			default:
				msg = lastLines(out.String(), 6)
			}
		}
		s.finish(msg)
	}()
	// "starting" ends when the hotspot is really on or the process dies
	go func() {
		for i := 0; i < 60; i++ {
			time.Sleep(500 * time.Millisecond)
			if st := hotspot.LoadState(); st != nil && hotspot.Alive(st.PID) && st.HostapdPID > 0 {
				s.mu.Lock()
				s.starting = false
				s.mu.Unlock()
				return
			}
		}
	}()
	writeJSON(w, map[string]string{"ok": "starting"})
}

func (s *server) finish(msg string) {
	s.mu.Lock()
	s.starting = false
	if msg != "" {
		s.lastErr = msg
	}
	s.mu.Unlock()
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

func (s *server) stop(w http.ResponseWriter, r *http.Request) {
	if err := hotspot.SendStop(); err != nil {
		// not started by this user: fall back to the password prompt
		if sys.Has("pkexec") {
			go func() { _ = exec.Command("pkexec", s.self, "stop").Run() }()
		}
	}
	writeJSON(w, map[string]string{"ok": "stopping"})
}
