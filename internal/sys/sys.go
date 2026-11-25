// Package sys wraps running system commands and reading basic system facts.
package sys

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
)

// Runner runs commands. In DryRun mode, Run only prints what it would do;
// Query (read-only commands) always executes.
type Runner struct{ DryRun bool }

// Query runs a read-only command and returns its stdout.
func (r Runner) Query(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s %s: %v: %s", name, strings.Join(args, " "), err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

// Run runs a command that changes the system (skipped in dry-run).
func (r Runner) Run(name string, args ...string) (string, error) {
	if r.DryRun {
		fmt.Printf("  + %s %s\n", name, strings.Join(args, " "))
		return "", nil
	}
	return r.Query(name, args...)
}

// RunStdin is Run with text piped to the command's stdin.
func (r Runner) RunStdin(stdin, name string, args ...string) error {
	if r.DryRun {
		fmt.Printf("  + %s %s <<EOF\n%s\nEOF\n", name, strings.Join(args, " "), strings.TrimSpace(stdin))
		return nil
	}
	cmd := exec.Command(name, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var errb bytes.Buffer
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %v: %s", name, err, strings.TrimSpace(errb.String()))
	}
	return nil
}

// Has reports whether a program is on PATH (also checks sbin dirs).
func Has(bin string) bool {
	if _, err := exec.LookPath(bin); err == nil {
		return true
	}
	for _, d := range []string{"/usr/sbin", "/sbin", "/usr/local/sbin"} {
		if st, err := os.Stat(d + "/" + bin); err == nil && !st.IsDir() {
			return true
		}
	}
	return false
}

// DefaultRouteIface returns the interface that carries the default route
// (lowest metric), or "" if there is no internet route.
func DefaultRouteIface() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	return parseDefaultRoute(string(b))
}

func parseDefaultRoute(s string) string {
	type rt struct {
		iface  string
		metric int
	}
	var routes []rt
	for i, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if i == 0 || len(f) < 8 {
			continue
		}
		if f[1] == "00000000" && f[7] == "00000000" {
			m, _ := strconv.Atoi(f[6])
			routes = append(routes, rt{f[0], m})
		}
	}
	if len(routes) == 0 {
		return ""
	}
	sort.Slice(routes, func(i, j int) bool { return routes[i].metric < routes[j].metric })
	return routes[0].iface
}

// ServiceActive reports whether a systemd unit is active (false if no systemd).
func ServiceActive(unit string) bool {
	if !Has("systemctl") {
		return false
	}
	out, _ := exec.Command("systemctl", "is-active", unit).Output()
	return strings.TrimSpace(string(out)) == "active"
}
