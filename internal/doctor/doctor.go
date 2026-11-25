// Package doctor checks whether this machine can run a hotspot.
package doctor

import (
	"fmt"
	"strings"

	"github.com/manuutechy/dualhot/internal/hotspot"
	"github.com/manuutechy/dualhot/internal/sys"
	"github.com/manuutechy/dualhot/internal/wifi"
)

// Level of one check line.
type Level string

const (
	OK   Level = "ok"
	Warn Level = "warn"
	Fail Level = "FAIL"
	Info Level = "info"
)

// Check is one line of the report.
type Check struct {
	Level  Level
	Text   string
	Detail string
}

// Report is the full result.
type Report struct {
	Checks  []Check
	Ready   bool
	Verdict string
}

func (r *Report) add(l Level, text, detail string) {
	r.Checks = append(r.Checks, Check{l, text, detail})
}

var pkgOf = map[string]string{"iw": "iw", "hostapd": "hostapd", "dnsmasq": "dnsmasq", "nft": "nftables", "ip": "iproute2"}

// InstallHint returns the install command for the missing programs.
func InstallHint(missing []string) string {
	var pk []string
	for _, m := range missing {
		pk = append(pk, pkgOf[m])
	}
	list := strings.Join(pk, " ")
	cmds := []struct{ bin, cmd string }{
		{"pacman", "sudo pacman -S --needed "}, {"apt", "sudo apt install "}, {"dnf", "sudo dnf install "},
		{"zypper", "sudo zypper install "}, {"apk", "sudo apk add "}, {"xbps-install", "sudo xbps-install -S "},
	}
	for _, c := range cmds {
		if sys.Has(c.bin) {
			return c.cmd + list
		}
	}
	return "install: " + list
}

// Run inspects the machine.
func Run(r sys.Runner) Report {
	var rep Report
	var missing []string
	for _, b := range []string{"iw", "hostapd", "dnsmasq", "ip"} {
		if sys.Has(b) {
			rep.add(OK, b+" installed", "")
		} else {
			rep.add(Fail, b+" is missing", "")
			missing = append(missing, b)
		}
	}
	switch {
	case sys.Has("nft"):
		rep.add(OK, "nftables installed (used to share the internet)", "")
	case sys.Has("iptables"):
		rep.add(OK, "iptables installed (used to share the internet)", "")
	default:
		rep.add(Fail, "nftables / iptables is missing", "")
		missing = append(missing, "nft")
	}
	if len(missing) > 0 {
		rep.add(Info, "install what is missing:", InstallHint(missing))
	}
	if !sys.Has("iw") {
		rep.Verdict = "Cannot check the WiFi card until `iw` is installed."
		return rep
	}

	s, err := wifi.Inspect(r)
	if err != nil {
		rep.Verdict = "Could not read WiFi state: " + err.Error()
		return rep
	}
	if len(s.Ifaces) == 0 {
		rep.add(Fail, "no WiFi adapter found", "")
		rep.Verdict = "No WiFi adapter on this machine."
		return rep
	}
	seen := map[string]bool{}
	for _, i := range s.Ifaces {
		c := s.Caps[i.Phy]
		line := fmt.Sprintf("WiFi adapter %s (%s, driver %s)", i.Name, i.Phy, orDash(i.Driver))
		if i.Type == "managed" && i.SSID != "" {
			line += fmt.Sprintf(", connected to %q on channel %d", i.SSID, i.Channel)
		}
		rep.add(Info, line, "")
		if seen[i.Phy] {
			continue
		}
		seen[i.Phy] = true
		switch {
		case !c.AP:
			rep.add(Fail, i.Phy+": cannot host a hotspot (no AP mode)", "")
		case c.Concurrent && c.SameChannel:
			rep.add(OK, i.Phy+": can be a client AND a hotspot at once", "both must use the same WiFi channel (dualhot handles this)")
		case c.Concurrent:
			rep.add(OK, i.Phy+": can be a client AND a hotspot at once, on different channels", "")
		default:
			rep.add(Warn, i.Phy+": hotspot works, but not while also connected to WiFi", "needs a second adapter or a cable for internet")
		}
	}

	if s.Country == "" {
		rep.add(Warn, "WiFi country is not set", "5 GHz hotspots may fail. Set it: sudo iw reg set KE   (use your country code)")
	} else {
		rep.add(OK, "WiFi country: "+s.Country, "")
	}
	if sys.ServiceActive("NetworkManager") {
		rep.add(Info, "NetworkManager is running", "fine: dualhot tells it to leave the hotspot interface alone")
	}
	if sys.ServiceActive("dnsmasq") {
		rep.add(Warn, "a system dnsmasq service is running", "it can block the hotspot's DHCP. Stop it: sudo systemctl stop dnsmasq")
	}
	if sys.ServiceActive("hostapd") {
		rep.add(Warn, "a system hostapd service is running", "stop it: sudo systemctl stop hostapd")
	}

	def := sys.DefaultRouteIface()
	plan, perr := hotspot.BuildPlan(hotspot.Config{}, s.Ifaces, s.Caps, def)
	if def != "" {
		rep.add(Info, "internet comes in through "+def, "")
	}
	if perr != nil {
		rep.Verdict = "NOT READY: " + perr.Error()
		return rep
	}
	if fi, ok := s.Freqs[plan.Phy][plan.Freq]; ok && plan.Mode == "concurrent" {
		if fi.Radar {
			rep.add(Warn, fmt.Sprintf("your WiFi is on a DFS channel (%d)", plan.Channel),
				"a hotspot cannot start there. Use 2.4 GHz, or set your router to channel 36-48 or 149-165")
		} else if fi.NoIR {
			rep.add(Info, fmt.Sprintf("channel %d is marked 'no IR' by the driver", plan.Channel), "if the hotspot fails to start, connect your WiFi on 2.4 GHz")
		}
	}
	if len(missing) > 0 {
		rep.Verdict = "ALMOST: this machine can do it (" + plan.Mode + " mode) once the missing programs are installed."
		return rep
	}
	rep.Ready = true
	rep.Verdict = fmt.Sprintf("READY: %s mode on %s. Start it with: sudo dualhot start", plan.Mode, plan.Base)
	return rep
}

func orDash(s string) string {
	if s == "" {
		return "?"
	}
	return s
}

// Print writes the report to the terminal.
func Print(rep Report) {
	mark := map[Level]string{OK: "  ✓", Warn: "  !", Fail: "  ✗", Info: "  ·"}
	for _, c := range rep.Checks {
		fmt.Printf("%s %s\n", mark[c.Level], c.Text)
		if c.Detail != "" {
			fmt.Printf("      %s\n", c.Detail)
		}
	}
	fmt.Printf("\n%s\n", rep.Verdict)
}
