// Package hotspot plans, starts and stops the hotspot.
package hotspot

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"os"

	"github.com/manuutechy/dualhot/internal/wifi"
)

// Config is what the user asked for.
type Config struct {
	SSID, Pass, Country, Subnet, Uplink, APName string
	Channel                                     int
	Hidden, DryRun                              bool
}

// Plan is how we will do it on this machine.
type Plan struct {
	Mode    string // "concurrent" (same chip as the uplink) or "dedicated" (its own adapter)
	Uplink  string
	Phy     string
	Base    string // the physical interface the hotspot is built on
	AP      string // the hotspot interface name
	Channel int
	Freq    int
	Create  bool // create a virtual interface (concurrent) vs use Base directly
}

// BuildPlan decides how to run the hotspot. It is pure so it can be tested.
func BuildPlan(cfg Config, ifaces []wifi.Iface, caps map[string]wifi.Capability, defRoute string) (Plan, error) {
	uplink := cfg.Uplink
	if uplink == "" {
		uplink = defRoute
	}
	if uplink == "" {
		return Plan{}, errors.New("no internet connection found. Connect to WiFi or plug in a cable first, or pass --uplink")
	}
	ap := cfg.APName
	if ap == "" {
		ap = "ap0"
	}
	var up *wifi.Iface
	for i := range ifaces {
		if ifaces[i].Name == uplink && ifaces[i].Type == "managed" {
			up = &ifaces[i]
		}
	}
	dedicated := func(i wifi.Iface) Plan {
		ch := cfg.Channel
		if ch == 0 {
			ch = 6
		}
		return Plan{Mode: "dedicated", Uplink: uplink, Phy: i.Phy, Base: i.Name, AP: i.Name, Channel: ch}
	}
	if up != nil {
		c := caps[up.Phy]
		if c.AP && c.Concurrent {
			if up.Channel == 0 {
				return Plan{}, fmt.Errorf("%s is not connected to a WiFi network yet", up.Name)
			}
			return Plan{Mode: "concurrent", Uplink: uplink, Phy: up.Phy, Base: up.Name, AP: ap,
				Channel: up.Channel, Freq: up.Freq, Create: true}, nil
		}
		for _, i := range ifaces {
			if i.Phy != up.Phy && i.Type == "managed" && caps[i.Phy].AP {
				return dedicated(i), nil
			}
		}
		return Plan{}, errors.New("this WiFi chip cannot be a client and a hotspot at the same time, and there is no second WiFi adapter.\n" +
			"  Options: (1) plug in a USB WiFi adapter, (2) use an Ethernet cable or USB phone tethering as the internet and let the WiFi chip be the hotspot")
	}
	for _, i := range ifaces {
		if i.Type == "managed" && caps[i.Phy].AP {
			return dedicated(i), nil
		}
	}
	return Plan{}, errors.New("no WiFi adapter on this machine can host a hotspot (no AP mode)")
}

// Net works out the gateway and DHCP range from the subnet.
func (c Config) Net() (gw, mask, from, to string, err error) {
	s := c.Subnet
	if s == "" {
		s = "10.42.77.0/24"
	}
	ip, n, err := net.ParseCIDR(s)
	if err != nil || ip.To4() == nil {
		return "", "", "", "", fmt.Errorf("bad --subnet %q: want something like 10.42.77.0/24", s)
	}
	if ones, _ := n.Mask.Size(); ones != 24 {
		return "", "", "", "", errors.New("--subnet must be a /24")
	}
	b := n.IP.To4()
	at := func(last byte) string { return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], last) }
	return at(1), "255.255.255.0", at(10), at(250), nil
}

// Validate checks SSID and password.
func (c Config) Validate() error {
	if l := len(c.SSID); l < 1 || l > 32 {
		return errors.New("--ssid must be 1 to 32 characters")
	}
	if l := len(c.Pass); l < 8 || l > 63 {
		return errors.New("--pass must be 8 to 63 characters")
	}
	return nil
}

// RandomPass makes a readable random password (no look-alike characters).
func RandomPass(n int) string {
	const al = "abcdefghjkmnpqrstuvwxyz23456789"
	b := make([]byte, n)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = al[int(b[i])%len(al)]
	}
	return string(b)
}

// HostapdConf builds the hostapd configuration text.
func HostapdConf(c Config, iface string, channel, freq int) string {
	band := wifi.BandOf(freq)
	if band == "" {
		if channel <= 14 {
			band = "2.4"
		} else {
			band = "5"
		}
	}
	mode := "g"
	if band != "2.4" {
		mode = "a"
	}
	s := fmt.Sprintf("interface=%s\ndriver=nl80211\nssid=%s\nhw_mode=%s\nchannel=%d\nieee80211n=1\nwmm_enabled=1\n", iface, c.SSID, mode, channel)
	if c.Country != "" {
		s += fmt.Sprintf("country_code=%s\nieee80211d=1\n", c.Country)
	}
	if c.Hidden {
		s += "ignore_broadcast_ssid=1\n"
	}
	s += fmt.Sprintf("auth_algs=1\nwpa=2\nwpa_key_mgmt=WPA-PSK\nrsn_pairwise=CCMP\nwpa_passphrase=%s\n", c.Pass)
	return s
}

// DefaultSSID is "dualhot-<hostname>".
func DefaultSSID() string {
	h, _ := os.Hostname()
	if h == "" {
		h = "linux"
	}
	s := "dualhot-" + h
	if len(s) > 32 {
		s = s[:32]
	}
	return s
}
