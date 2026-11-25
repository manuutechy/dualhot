package hotspot

import (
	"strings"
	"testing"

	"github.com/manuutechy/dualhot/internal/wifi"
)

var both = wifi.Capability{AP: true, Concurrent: true, SameChannel: true}
var apOnly = wifi.Capability{AP: true}

func TestConcurrentFollowsUplinkChannel(t *testing.T) {
	ifs := []wifi.Iface{{Name: "wlan0", Phy: "phy0", Type: "managed", Channel: 11, Freq: 2462}}
	p, err := BuildPlan(Config{}, ifs, map[string]wifi.Capability{"phy0": both}, "wlan0")
	if err != nil || p.Mode != "concurrent" || p.Channel != 11 || !p.Create || p.AP != "ap0" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestEthernetUplinkUsesWifiAsHotspot(t *testing.T) {
	ifs := []wifi.Iface{{Name: "wlan0", Phy: "phy0", Type: "managed"}}
	p, err := BuildPlan(Config{}, ifs, map[string]wifi.Capability{"phy0": apOnly}, "eth0")
	if err != nil || p.Mode != "dedicated" || p.Base != "wlan0" || p.Create || p.Channel != 6 {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestChipCannotDoBothUsesSecondAdapter(t *testing.T) {
	ifs := []wifi.Iface{
		{Name: "wlan0", Phy: "phy0", Type: "managed", Channel: 6, Freq: 2437},
		{Name: "wlan1", Phy: "phy1", Type: "managed"},
	}
	caps := map[string]wifi.Capability{"phy0": apOnly, "phy1": apOnly}
	p, err := BuildPlan(Config{}, ifs, caps, "wlan0")
	if err != nil || p.Mode != "dedicated" || p.Base != "wlan1" {
		t.Fatalf("%+v %v", p, err)
	}
}

func TestChipCannotDoBothNoSecondAdapter(t *testing.T) {
	ifs := []wifi.Iface{{Name: "wlan0", Phy: "phy0", Type: "managed", Channel: 6, Freq: 2437}}
	_, err := BuildPlan(Config{}, ifs, map[string]wifi.Capability{"phy0": apOnly}, "wlan0")
	if err == nil || !strings.Contains(err.Error(), "USB") {
		t.Fatalf("want helpful error, got %v", err)
	}
}

func TestNoInternet(t *testing.T) {
	if _, err := BuildPlan(Config{}, nil, nil, ""); err == nil {
		t.Fatal("want error")
	}
}

func TestValidateAndNet(t *testing.T) {
	if (Config{SSID: "x", Pass: "short"}).Validate() == nil {
		t.Fatal("short password must fail")
	}
	if (Config{SSID: "x", Pass: "longenough"}).Validate() != nil {
		t.Fatal("good config must pass")
	}
	gw, _, from, to, err := Config{Subnet: "192.168.50.0/24"}.Net()
	if err != nil || gw != "192.168.50.1" || from != "192.168.50.10" || to != "192.168.50.250" {
		t.Fatal(gw, from, to, err)
	}
	if _, _, _, _, err := (Config{Subnet: "10.0.0.0/16"}).Net(); err == nil {
		t.Fatal("/16 must be rejected")
	}
}

func TestHostapdConf(t *testing.T) {
	c := HostapdConf(Config{SSID: "Net", Pass: "password1", Country: "KE"}, "ap0", 36, 5180)
	for _, want := range []string{"interface=ap0", "ssid=Net", "hw_mode=a", "channel=36", "country_code=KE", "wpa_passphrase=password1"} {
		if !strings.Contains(c, want) {
			t.Errorf("missing %q in\n%s", want, c)
		}
	}
	if !strings.Contains(HostapdConf(Config{SSID: "N", Pass: "password1"}, "ap0", 6, 2437), "hw_mode=g") {
		t.Error("2.4GHz must be hw_mode=g")
	}
}

func TestRandomPass(t *testing.T) {
	a, b := RandomPass(10), RandomPass(10)
	if len(a) != 10 || a == b {
		t.Fatal(a, b)
	}
}
