package sys

import "testing"

func TestDefaultRoute(t *testing.T) {
	in := "Iface\tDestination\tGateway\tFlags\tRefCnt\tUse\tMetric\tMask\tMTU\tWindow\tIRTT\n" +
		"wlan0\t00000000\t0100A8C0\t0003\t0\t0\t600\t00000000\t0\t0\t0\n" +
		"eth0\t00000000\t0100A8C0\t0003\t0\t0\t100\t00000000\t0\t0\t0\n" +
		"wlan0\t0000A8C0\t00000000\t0001\t0\t0\t600\t00FFFFFF\t0\t0\t0\n"
	if got := parseDefaultRoute(in); got != "eth0" {
		t.Fatalf("got %q want eth0 (lowest metric)", got)
	}
	if parseDefaultRoute("Iface\n") != "" {
		t.Fatal("no routes should give empty")
	}
}
