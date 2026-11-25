package hotspot

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/manuutechy/dualhot/internal/sys"
	"github.com/manuutechy/dualhot/internal/wifi"
)

// tail keeps the last few KB of a process's output for error messages.
type tail struct {
	mu sync.Mutex
	b  []byte
}

func (t *tail) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.b = append(t.b, p...)
	if len(t.b) > 8192 {
		t.b = t.b[len(t.b)-8192:]
	}
	return len(p), nil
}
func (t *tail) String() string { t.mu.Lock(); defer t.mu.Unlock(); return string(t.b) }

type proc struct {
	cmd  *exec.Cmd
	out  *tail
	done chan struct{}
}

func startProc(name string, args ...string) (*proc, error) {
	p := &proc{cmd: exec.Command(name, args...), out: &tail{}, done: make(chan struct{})}
	p.cmd.Stdout, p.cmd.Stderr = p.out, p.out
	if err := p.cmd.Start(); err != nil {
		return nil, err
	}
	go func() { _ = p.cmd.Wait(); close(p.done) }()
	return p, nil
}

func (p *proc) pid() int { return p.cmd.Process.Pid }

func (p *proc) waitFor(substr string, d time.Duration) error {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if strings.Contains(p.out.String(), substr) {
			return nil
		}
		select {
		case <-p.done:
			return fmt.Errorf("exited early:\n%s", strings.TrimSpace(p.out.String()))
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("did not become ready in %s:\n%s", d, strings.TrimSpace(p.out.String()))
}

func (p *proc) stop() {
	_ = p.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
		_ = p.cmd.Process.Kill()
		<-p.done
	}
}

func startHostapd(cfg Config, plan Plan, channel, freq int) (*proc, error) {
	conf := filepath.Join(RunDir, "hostapd.conf")
	if err := os.WriteFile(conf, []byte(HostapdConf(cfg, plan.AP, channel, freq)), 0600); err != nil {
		return nil, err
	}
	p, err := startProc("hostapd", conf)
	if err != nil {
		return nil, err
	}
	if err := p.waitFor("AP-ENABLED", 8*time.Second); err != nil {
		p.stop()
		return nil, fmt.Errorf("hostapd %w", err)
	}
	return p, nil
}

func macFor(base string) string {
	b, err := os.ReadFile("/sys/class/net/" + base + "/address")
	if err != nil {
		return ""
	}
	hw, err := net.ParseMAC(strings.TrimSpace(string(b)))
	if err != nil || len(hw) != 6 {
		return ""
	}
	hw[0] |= 0x02
	hw[4] ^= 0x5a
	return hw.String()
}

// Start builds the hotspot and keeps it running until ctx is cancelled.
func Start(ctx context.Context, r sys.Runner, cfg Config, plan Plan, logf func(string, ...any)) error {
	gw, mask, from, to, err := cfg.Net()
	if err != nil {
		return err
	}
	subnet := cfg.Subnet
	if subnet == "" {
		subnet = "10.42.77.0/24"
	}
	if !r.DryRun {
		if old := LoadState(); old != nil {
			if Alive(old.PID) {
				return fmt.Errorf("already running (pid %d). Stop it first with: sudo dualhot stop", old.PID)
			}
			logf("cleaning up after an earlier run that did not shut down")
			Teardown(r, old)
		}
		if err := os.MkdirAll(RunDir, 0755); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if !r.DryRun {
		if ln := listenControl(cancel); ln != nil {
			defer ln.Close()
		}
	}
	st := &State{PID: os.Getpid(), Mode: plan.Mode, SSID: cfg.SSID, AP: plan.AP, Base: plan.Base,
		Subnet: subnet, Channel: plan.Channel, Started: time.Now().Unix()}
	ok := false
	defer func() {
		if !ok || !r.DryRun {
			Teardown(r, st)
		}
	}()

	// 1. the hotspot interface
	if plan.Create {
		_, _ = r.Run("iw", "dev", plan.AP, "del") // leftover from a crash
		args := []string{"phy", plan.Phy, "interface", "add", plan.AP, "type", "__ap"}
		if mac := macFor(plan.Base); mac != "" {
			args = append(args, "addr", mac)
		}
		if _, err := r.Run("iw", args...); err != nil {
			if _, err2 := r.Run("iw", "phy", plan.Phy, "interface", "add", plan.AP, "type", "__ap"); err2 != nil {
				return fmt.Errorf("could not create the hotspot interface: %w", err2)
			}
		}
		st.Created = true
	}
	if sys.Has("nmcli") && sys.ServiceActive("NetworkManager") {
		_, _ = r.Run("nmcli", "device", "set", plan.AP, "managed", "no")
		st.NMTouched = !plan.Create
	}
	_, _ = r.Run("ip", "addr", "flush", "dev", plan.AP)
	if _, err := r.Run("ip", "addr", "add", gw+"/24", "dev", plan.AP); err != nil {
		return err
	}
	if _, err := r.Run("ip", "link", "set", plan.AP, "up"); err != nil {
		return err
	}
	st.Save()

	// 2. broadcast the network
	freq := plan.Freq
	var ha *proc
	if r.DryRun {
		fmt.Printf("  + hostapd %s/hostapd.conf   (channel %d)\n", RunDir, plan.Channel)
		fmt.Println(strings.ReplaceAll(HostapdConf(cfg, plan.AP, plan.Channel, freq), cfg.Pass, "********"))
	} else {
		ha, err = startHostapd(cfg, plan, plan.Channel, freq)
		if err != nil {
			return err
		}
		st.HostapdPID = ha.pid()
		st.Save()
	}

	// 3. hand out addresses
	var dns *proc
	dargs := []string{"-k", "--conf-file=/dev/null", "--interface=" + plan.AP, "--bind-interfaces",
		"--except-interface=lo", "--listen-address=" + gw,
		fmt.Sprintf("--dhcp-range=%s,%s,%s,12h", from, to, mask),
		"--dhcp-option=option:router," + gw, "--dhcp-option=option:dns-server," + gw,
		"--dhcp-leasefile=" + filepath.Join(RunDir, "leases"), "--pid-file=" + filepath.Join(RunDir, "dnsmasq.pid")}
	if r.DryRun {
		fmt.Println("  + dnsmasq " + strings.Join(dargs, " "))
	} else {
		dns, err = startProc("dnsmasq", dargs...)
		if err != nil {
			return err
		}
		select {
		case <-dns.done:
			return fmt.Errorf("dnsmasq exited:\n%s\n(is another dnsmasq already using port 53? Stop it: sudo systemctl stop dnsmasq)", strings.TrimSpace(dns.out.String()))
		case <-time.After(700 * time.Millisecond):
		}
		st.DnsmasqPID = dns.pid()
		st.Save()
	}

	// 4. share the internet
	st.OldForward = ForwardOn(r)
	method, err := FirewallUp(r, plan.AP, subnet)
	if err != nil {
		return err
	}
	st.FW = method
	st.Save()

	ok = true
	if r.DryRun {
		return nil
	}
	logf("hotspot %q is ON (%s mode, channel %d, internet via %s)", cfg.SSID, plan.Mode, plan.Channel, plan.Uplink)

	// 5. keep it alive; follow the uplink if its channel changes
	tick := time.NewTicker(3 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			logf("stopping...")
			if ha != nil {
				ha.stop()
			}
			if dns != nil {
				dns.stop()
			}
			return nil
		case <-ha.done:
			return fmt.Errorf("hostapd stopped unexpectedly:\n%s", strings.TrimSpace(ha.out.String()))
		case <-dns.done:
			return fmt.Errorf("dnsmasq stopped unexpectedly:\n%s", strings.TrimSpace(dns.out.String()))
		case <-tick.C:
			if plan.Mode != "concurrent" {
				continue
			}
			for _, i := range wifi.Refresh(r) {
				if i.Name == plan.Base && i.Channel != 0 && i.Channel != st.Channel {
					logf("your WiFi moved to channel %d: moving the hotspot too (devices reconnect in a few seconds)", i.Channel)
					ha.stop()
					st.Channel = i.Channel
					nh, err := startHostapd(cfg, plan, i.Channel, i.Freq)
					if err != nil {
						st.HostapdPID = 0
						return fmt.Errorf("could not follow the channel change: %w", err)
					}
					ha = nh
					st.HostapdPID = ha.pid()
					st.Save()
				}
			}
		}
	}
}
