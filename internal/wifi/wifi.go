// Package wifi reads WiFi adapter facts from `iw` and works out whether an
// adapter can be a client and a hotspot at the same time.
package wifi

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/manuutechy/dualhot/internal/sys"
)

// Iface is one WiFi network interface.
type Iface struct {
	Name, Phy, Type, SSID, MAC, Driver string
	Freq, Channel                      int
}

var (
	rePhy  = regexp.MustCompile(`^phy#(\d+)`)
	reChan = regexp.MustCompile(`^channel (\d+) \((\d+)(?:\.\d+)? MHz\)`)
)

// ParseIwDev parses `iw dev` output.
func ParseIwDev(out string) []Iface {
	var res []Iface
	phy, i := "", -1
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if m := rePhy.FindStringSubmatch(t); m != nil {
			phy = "phy" + m[1]
			i = -1
			continue
		}
		if strings.HasPrefix(t, "Interface ") {
			res = append(res, Iface{Name: strings.TrimPrefix(t, "Interface "), Phy: phy})
			i = len(res) - 1
			continue
		}
		if i < 0 {
			continue
		}
		switch {
		case strings.HasPrefix(t, "type "):
			res[i].Type = strings.TrimPrefix(t, "type ")
		case strings.HasPrefix(t, "addr "):
			res[i].MAC = strings.TrimPrefix(t, "addr ")
		case strings.HasPrefix(t, "ssid "):
			res[i].SSID = strings.TrimPrefix(t, "ssid ")
		default:
			if m := reChan.FindStringSubmatch(t); m != nil {
				res[i].Channel, _ = strconv.Atoi(m[1])
				res[i].Freq, _ = strconv.Atoi(m[2])
			}
		}
	}
	return res
}

// FreqToChannel converts a frequency in MHz to a WiFi channel number.
func FreqToChannel(f int) int {
	switch {
	case f == 2484:
		return 14
	case f >= 2412 && f <= 2472:
		return (f - 2407) / 5
	case f >= 5160 && f <= 5885:
		return (f - 5000) / 5
	case f >= 5955 && f <= 7115:
		return (f - 5950) / 5
	}
	return 0
}

// BandOf returns "2.4", "5" or "6" for a frequency in MHz.
func BandOf(f int) string {
	switch {
	case f == 0:
		return ""
	case f < 3000:
		return "2.4"
	case f < 5900:
		return "5"
	}
	return "6"
}

// Group is one "#{ managed, AP } <= 2" clause of an interface combination.
type Group struct {
	Modes []string
	Max   int
}

// Combo is one "valid interface combination" a driver allows.
type Combo struct {
	Groups      []Group
	MaxChannels int
}

var (
	reGroup = regexp.MustCompile(`#\{\s*([^}]*?)\s*\}\s*<=\s*(\d+)`)
	reChans = regexp.MustCompile(`#channels\s*<=\s*(\d+)`)
)

func leadingTabs(s string) int {
	n := 0
	for _, c := range s {
		if c != '\t' {
			break
		}
		n++
	}
	return n
}

// ParseCombos reads the "valid interface combinations" part of `iw phy X info`.
func ParseCombos(info string) []Combo {
	var blocks []string
	in := false
	for _, l := range strings.Split(info, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "valid interface combinations:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if t == "" || leadingTabs(l) < 2 {
			in = false
			continue
		}
		if strings.HasPrefix(t, "*") {
			blocks = append(blocks, strings.TrimSpace(strings.TrimPrefix(t, "*")))
		} else if len(blocks) > 0 {
			blocks[len(blocks)-1] += " " + t
		}
	}
	var combos []Combo
	for _, b := range blocks {
		c := Combo{MaxChannels: 1}
		for _, m := range reGroup.FindAllStringSubmatch(b, -1) {
			max, _ := strconv.Atoi(m[2])
			var modes []string
			for _, x := range strings.Split(m[1], ",") {
				modes = append(modes, strings.TrimSpace(x))
			}
			c.Groups = append(c.Groups, Group{modes, max})
		}
		if m := reChans.FindStringSubmatch(b); m != nil {
			c.MaxChannels, _ = strconv.Atoi(m[1])
		}
		combos = append(combos, c)
	}
	return combos
}

// Allows reports whether this combination lets all the given modes exist at once.
func (c Combo) Allows(modes ...string) bool {
	rem := make([]int, len(c.Groups))
	for i, g := range c.Groups {
		rem[i] = g.Max
	}
	var place func(k int) bool
	place = func(k int) bool {
		if k == len(modes) {
			return true
		}
		for i, g := range c.Groups {
			if rem[i] <= 0 {
				continue
			}
			for _, m := range g.Modes {
				if m == modes[k] {
					rem[i]--
					if place(k + 1) {
						return true
					}
					rem[i]++
					break
				}
			}
		}
		return false
	}
	return place(0)
}

// ParseModes reads "Supported interface modes" from `iw phy X info`.
func ParseModes(info string) []string {
	var modes []string
	in := false
	for _, l := range strings.Split(info, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(t, "Supported interface modes:") {
			in = true
			continue
		}
		if !in {
			continue
		}
		if !strings.HasPrefix(t, "*") || leadingTabs(l) < 2 {
			break
		}
		modes = append(modes, strings.TrimSpace(strings.TrimPrefix(t, "*")))
	}
	return modes
}

// Capability says what one WiFi chip (phy) can do.
type Capability struct {
	AP          bool // can host a hotspot at all
	Concurrent  bool // can be a client and a hotspot at the same time
	SameChannel bool // ...but only when both use the same channel
}

// Analyze works out a Capability from `iw phy X info`.
func Analyze(info string) Capability {
	var c Capability
	for _, m := range ParseModes(info) {
		if m == "AP" {
			c.AP = true
		}
	}
	c.SameChannel = true
	for _, combo := range ParseCombos(info) {
		if combo.Allows("managed", "AP") {
			c.Concurrent = true
			if combo.MaxChannels > 1 {
				c.SameChannel = false
			}
		}
	}
	if !c.Concurrent {
		c.SameChannel = false
	}
	return c
}

// FreqInfo flags on one frequency.
type FreqInfo struct{ Disabled, NoIR, Radar bool }

var reFreq = regexp.MustCompile(`\*\s*(\d+)(?:\.\d+)? MHz \[(\d+)\](.*)`)

// ParseFreqs reads the per-frequency flags from `iw phy X info`.
func ParseFreqs(info string) map[int]FreqInfo {
	res := map[int]FreqInfo{}
	for _, l := range strings.Split(info, "\n") {
		if m := reFreq.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
			f, _ := strconv.Atoi(m[1])
			rest := m[3]
			res[f] = FreqInfo{
				Disabled: strings.Contains(rest, "disabled"),
				NoIR:     strings.Contains(rest, "no IR") || strings.Contains(rest, "passive scan"),
				Radar:    strings.Contains(rest, "radar detection"),
			}
		}
	}
	return res
}

// System is everything we learn about the machine's WiFi.
type System struct {
	Ifaces  []Iface
	Caps    map[string]Capability // by phy
	Freqs   map[string]map[int]FreqInfo
	Country string
}

// Inspect reads the WiFi state using `iw`.
func Inspect(r sys.Runner) (System, error) {
	s := System{Caps: map[string]Capability{}, Freqs: map[string]map[int]FreqInfo{}}
	out, err := r.Query("iw", "dev")
	if err != nil {
		return s, err
	}
	s.Ifaces = ParseIwDev(out)
	for i := range s.Ifaces {
		s.Ifaces[i].Driver = driverOf(s.Ifaces[i].Name)
		if _, ok := s.Caps[s.Ifaces[i].Phy]; !ok {
			info, err := r.Query("iw", "phy", s.Ifaces[i].Phy, "info")
			if err != nil {
				return s, fmt.Errorf("reading %s: %w", s.Ifaces[i].Phy, err)
			}
			s.Caps[s.Ifaces[i].Phy] = Analyze(info)
			s.Freqs[s.Ifaces[i].Phy] = ParseFreqs(info)
		}
	}
	if reg, err := r.Query("iw", "reg", "get"); err == nil {
		for _, l := range strings.Split(reg, "\n") {
			if strings.HasPrefix(l, "country ") {
				c := strings.TrimSuffix(strings.Fields(l)[1], ":")
				if c != "00" && c != "99" {
					s.Country = c
				}
				break
			}
		}
	}
	return s, nil
}

func driverOf(iface string) string {
	l, err := os.Readlink(filepath.Join("/sys/class/net", iface, "device/driver"))
	if err != nil {
		return ""
	}
	return filepath.Base(l)
}

// Refresh re-reads `iw dev` only (cheap), to follow channel changes.
func Refresh(r sys.Runner) []Iface {
	out, err := r.Query("iw", "dev")
	if err != nil {
		return nil
	}
	return ParseIwDev(out)
}
