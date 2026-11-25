// dualhot: share your internet as a WiFi hotspot while staying connected to WiFi.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/manuutechy/dualhot/internal/doctor"
	"github.com/manuutechy/dualhot/internal/gui"
	"github.com/manuutechy/dualhot/internal/hotspot"
	"github.com/manuutechy/dualhot/internal/sys"
	"github.com/manuutechy/dualhot/internal/wifi"
)

var version = "0.1.0"

const usage = `dualhot %s: a WiFi hotspot that works while you stay connected to WiFi

Usage:
  dualhot gui               open the app window (recommended)
  dualhot doctor            check if this machine can do it (no root needed)
  sudo dualhot start        start the hotspot (Ctrl+C stops it)
  sudo dualhot stop         stop it and clean up
  dualhot status            show if it is running and who is connected
  dualhot version

Start options:
  --ssid NAME      hotspot name       (default: dualhot-<hostname>)
  --pass PASSWORD  8-63 characters    (default: random, printed once)
  --pass-stdin     read the password from stdin (used by the app)
  --channel N      only for a dedicated adapter; in same-chip mode the hotspot
                   follows your WiFi's channel automatically
  --country CC     two-letter code, e.g. KE (default: system setting)
  --uplink IFACE   interface that has the internet (default: auto)
  --subnet CIDR    hotspot network (default: 10.42.77.0/24)
  --hidden         do not broadcast the name
  --dry-run        show every command without running anything
`

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "dualhot: "+format+"\n", a...)
	os.Exit(1)
}

func logf(f string, a ...any) {
	fmt.Printf("[%s] %s\n", time.Now().Format("15:04:05"), fmt.Sprintf(f, a...))
}

func main() {
	if len(os.Args) < 2 {
		fmt.Printf(usage, version)
		os.Exit(2)
	}
	switch os.Args[1] {
	case "doctor":
		rep := doctor.Run(sys.Runner{})
		doctor.Print(rep)
		if !rep.Ready {
			os.Exit(1)
		}
	case "gui":
		self, _ := os.Executable()
		if err := gui.Run(self); err != nil {
			fatal("%v", err)
		}
	case "start":
		cmdStart(os.Args[2:])
	case "stop":
		cmdStop()
	case "status":
		cmdStatus()
	case "version", "--version", "-v":
		fmt.Println("dualhot", version)
	case "help", "--help", "-h":
		fmt.Printf(usage, version)
	default:
		fmt.Printf(usage, version)
		os.Exit(2)
	}
}

func cmdStart(args []string) {
	fs := flag.NewFlagSet("start", flag.ExitOnError)
	var cfg hotspot.Config
	fs.StringVar(&cfg.SSID, "ssid", "", "")
	fs.StringVar(&cfg.Pass, "pass", "", "")
	fs.IntVar(&cfg.Channel, "channel", 0, "")
	fs.StringVar(&cfg.Country, "country", "", "")
	fs.StringVar(&cfg.Uplink, "uplink", "", "")
	fs.StringVar(&cfg.Subnet, "subnet", "", "")
	fs.BoolVar(&cfg.Hidden, "hidden", false, "")
	fs.BoolVar(&cfg.DryRun, "dry-run", false, "")
	passStdin := fs.Bool("pass-stdin", false, "")
	_ = fs.Parse(args)
	if *passStdin {
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		cfg.Pass = strings.TrimRight(line, "\r\n")
	}

	if os.Geteuid() != 0 && !cfg.DryRun {
		fatal("starting a hotspot needs root. Run: sudo dualhot start")
	}
	if cfg.SSID == "" {
		cfg.SSID = hotspot.DefaultSSID()
	}
	generated := false
	if cfg.Pass == "" {
		cfg.Pass, generated = hotspot.RandomPass(10), true
	}
	if err := cfg.Validate(); err != nil {
		fatal("%v", err)
	}
	for _, b := range []string{"iw", "hostapd", "dnsmasq", "ip"} {
		if !sys.Has(b) {
			fatal("%s is not installed. Run `dualhot doctor` for the install command", b)
		}
	}
	r := sys.Runner{DryRun: cfg.DryRun}
	s, err := wifi.Inspect(r)
	if err != nil {
		fatal("%v", err)
	}
	if cfg.Country == "" {
		cfg.Country = s.Country
	}
	plan, err := hotspot.BuildPlan(cfg, s.Ifaces, s.Caps, sys.DefaultRouteIface())
	if err != nil {
		fatal("%v", err)
	}
	if plan.Mode == "concurrent" {
		if fi, ok := s.Freqs[plan.Phy][plan.Freq]; ok && fi.Radar {
			fatal("your WiFi is on channel %d (DFS), where a hotspot cannot start. Use 2.4 GHz or channel 36-48 / 149-165", plan.Channel)
		}
	}
	fmt.Printf("Mode: %s | adapter: %s | internet via: %s | channel: %d\n", plan.Mode, plan.Base, plan.Uplink, plan.Channel)
	fmt.Printf("Name: %s\n", cfg.SSID)
	if generated {
		fmt.Printf("Password: %s   (random: pass --pass to choose your own)\n", cfg.Pass)
	} else {
		fmt.Println("Password: (the one you gave)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := hotspot.Start(ctx, r, cfg, plan, logf); err != nil {
		fatal("%v", err)
	}
	if !cfg.DryRun {
		logf("hotspot stopped and cleaned up")
	}
}

func cmdStop() {
	st := hotspot.LoadState()
	if st == nil {
		fmt.Println("dualhot is not running.")
		return
	}
	if os.Geteuid() != 0 {
		fatal("stopping needs root. Run: sudo dualhot stop")
	}
	if hotspot.Alive(st.PID) {
		_ = syscall.Kill(st.PID, syscall.SIGTERM)
		for i := 0; i < 50 && hotspot.Alive(st.PID); i++ {
			time.Sleep(200 * time.Millisecond)
		}
		if !hotspot.Alive(st.PID) {
			fmt.Println("Hotspot stopped.")
			return
		}
	}
	hotspot.Teardown(sys.Runner{}, st)
	fmt.Println("Hotspot stopped and cleaned up.")
}

func cmdStatus() {
	st := hotspot.LoadState()
	if st == nil || !hotspot.Alive(st.PID) {
		fmt.Println("dualhot is not running.")
		return
	}
	fmt.Printf("ON: %q | %s mode | channel %d | up %s\n", st.SSID, st.Mode, st.Channel, st.Uptime())
	ls := hotspot.Leases()
	fmt.Printf("Connected devices: %d\n", len(ls))
	for _, l := range ls {
		fmt.Printf("  %-16s %-18s %s\n", l.IP, l.MAC, l.Name)
	}
}
