package hotspot

import (
	"fmt"
	"os"
	"strings"

	"github.com/manuutechy/dualhot/internal/sys"
)

// Internet sharing (NAT) uses its own nftables tables, or tagged iptables
// rules, so removing them never touches the user's own firewall.

func nftRules(ap, subnet string) string {
	return fmt.Sprintf(`table ip dualhot_nat {
  chain post { type nat hook postrouting priority 100; ip saddr %s oifname != "%s" masquerade }
}
table inet dualhot_fw {
  chain in { type filter hook input priority -10; iifname "%s" udp dport { 53, 67 } accept; iifname "%s" tcp dport 53 accept }
  chain fwd { type filter hook forward priority -10; iifname "%s" accept; oifname "%s" ct state established,related accept }
}
`, subnet, ap, ap, ap, ap, ap)
}

func iptRules(ap, subnet string) [][]string {
	return [][]string{
		{"-t", "nat", "POSTROUTING", "-s", subnet, "!", "-o", ap, "-j", "MASQUERADE"},
		{"", "FORWARD", "-i", ap, "-j", "ACCEPT"},
		{"", "FORWARD", "-o", ap, "-m", "conntrack", "--ctstate", "RELATED,ESTABLISHED", "-j", "ACCEPT"},
		{"", "INPUT", "-i", ap, "-p", "udp", "-m", "multiport", "--dports", "53,67", "-j", "ACCEPT"},
		{"", "INPUT", "-i", ap, "-p", "tcp", "--dport", "53", "-j", "ACCEPT"},
	}
}

func iptArgs(op string, rule []string) []string {
	var a []string
	if rule[0] == "-t" {
		a = append(a, "-t", rule[1], op)
		a = append(a, rule[2:]...)
	} else {
		a = append(a, op)
		a = append(a, rule[1:]...)
	}
	return append(a, "-m", "comment", "--comment", "dualhot")
}

// FirewallUp turns on internet sharing and returns the method used.
func FirewallUp(r sys.Runner, ap, subnet string) (string, error) {
	if sys.Has("nft") {
		_, _ = r.Run("nft", "delete", "table", "ip", "dualhot_nat")
		_, _ = r.Run("nft", "delete", "table", "inet", "dualhot_fw")
		if err := r.RunStdin(nftRules(ap, subnet), "nft", "-f", "-"); err != nil {
			return "", err
		}
		return "nft", nil
	}
	if sys.Has("iptables") {
		for _, rule := range iptRules(ap, subnet) {
			op := "-A"
			if rule[0] == "" {
				op = "-I"
			}
			if _, err := r.Run("iptables", iptArgs(op, rule)...); err != nil {
				return "", err
			}
		}
		return "iptables", nil
	}
	return "", fmt.Errorf("need nftables (nft) or iptables to share the internet")
}

// FirewallDown removes what FirewallUp added.
func FirewallDown(r sys.Runner, method, ap, subnet string) {
	switch method {
	case "nft":
		_, _ = r.Run("nft", "delete", "table", "ip", "dualhot_nat")
		_, _ = r.Run("nft", "delete", "table", "inet", "dualhot_fw")
	case "iptables":
		for _, rule := range iptRules(ap, subnet) {
			_, _ = r.Run("iptables", iptArgs("-D", rule)...)
		}
	}
}

// ForwardOn enables IPv4 forwarding and returns the old value.
func ForwardOn(r sys.Runner) string {
	const p = "/proc/sys/net/ipv4/ip_forward"
	old := "0"
	if b, err := os.ReadFile(p); err == nil {
		old = strings.TrimSpace(string(b))
	}
	if r.DryRun {
		fmt.Println("  + echo 1 > " + p)
		return old
	}
	_ = os.WriteFile(p, []byte("1"), 0644)
	return old
}

// ForwardRestore puts IPv4 forwarding back how it was.
func ForwardRestore(old string) {
	if old == "0" || old == "1" {
		_ = os.WriteFile("/proc/sys/net/ipv4/ip_forward", []byte(old), 0644)
	}
}
