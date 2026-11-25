package hotspot

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/manuutechy/dualhot/internal/sys"
)

// RunDir holds the state file, configs and DHCP leases while running.
const RunDir = "/run/dualhot"

// State is saved so `stop` and `status` (and crash cleanup) know what exists.
type State struct {
	PID        int    `json:"pid"`
	Mode       string `json:"mode"`
	SSID       string `json:"ssid"`
	AP         string `json:"ap"`
	Base       string `json:"base"`
	Created    bool   `json:"created"`
	NMTouched  bool   `json:"nm_touched"`
	Subnet     string `json:"subnet"`
	FW         string `json:"fw"`
	OldForward string `json:"old_forward"`
	Channel    int    `json:"channel"`
	HostapdPID int    `json:"hostapd_pid"`
	DnsmasqPID int    `json:"dnsmasq_pid"`
	Started    int64  `json:"started"`
}

func statePath() string { return filepath.Join(RunDir, "state.json") }

// Save writes the state file.
func (s *State) Save() {
	_ = os.MkdirAll(RunDir, 0755)
	b, _ := json.MarshalIndent(s, "", "  ")
	_ = os.WriteFile(statePath(), b, 0600)
}

// LoadState reads the state file (nil if not running).
func LoadState() *State {
	b, err := os.ReadFile(statePath())
	if err != nil {
		return nil
	}
	var s State
	if json.Unmarshal(b, &s) != nil {
		return nil
	}
	return &s
}

// Alive reports whether a process exists.
func Alive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

func kill(pid int) {
	if pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
}

// Teardown undoes everything in the state. Safe to call twice.
func Teardown(r sys.Runner, s *State) {
	kill(s.HostapdPID)
	kill(s.DnsmasqPID)
	time.Sleep(300 * time.Millisecond)
	FirewallDown(r, s.FW, s.AP, s.Subnet)
	ForwardRestore(s.OldForward)
	if s.Created {
		_, _ = r.Run("iw", "dev", s.AP, "del")
	} else {
		_, _ = r.Run("ip", "addr", "flush", "dev", s.AP)
		if s.NMTouched && sys.Has("nmcli") {
			_, _ = r.Run("nmcli", "device", "set", s.AP, "managed", "yes")
		}
	}
	if !r.DryRun {
		_ = os.RemoveAll(RunDir)
	}
}

// Lease is one connected device from the DHCP lease file.
type Lease struct{ MAC, IP, Name string }

// Leases reads the devices that got an address from us.
func Leases() []Lease {
	b, err := os.ReadFile(filepath.Join(RunDir, "leases"))
	if err != nil {
		return nil
	}
	var res []Lease
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) >= 4 {
			name := f[3]
			if name == "*" {
				name = "(unknown)"
			}
			res = append(res, Lease{f[1], f[2], name})
		}
	}
	return res
}

// Uptime formats how long it has been running.
func (s *State) Uptime() string {
	d := time.Since(time.Unix(s.Started, 0)).Round(time.Second)
	return strconv.FormatFloat(d.Minutes(), 'f', 0, 64) + " min"
}
