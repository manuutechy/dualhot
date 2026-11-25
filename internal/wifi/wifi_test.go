package wifi

import "testing"

const intelInfo = "Wiphy phy0\n" +
	"\tSupported interface modes:\n" +
	"\t\t * IBSS\n\t\t * managed\n\t\t * AP\n\t\t * P2P-client\n\t\t * P2P-GO\n\t\t * P2P-device\n" +
	"\tBand 1:\n\t\tFrequencies:\n" +
	"\t\t\t* 2412.0 MHz [1] (22.0 dBm)\n" +
	"\t\t\t* 5180.0 MHz [36] (22.0 dBm) (no IR)\n" +
	"\t\t\t* 5260.0 MHz [52] (22.0 dBm) (no IR, radar detection)\n" +
	"\t\t\t* 5955.0 MHz [1] (disabled)\n" +
	"\tvalid interface combinations:\n" +
	"\t\t * #{ managed } <= 1, #{ AP, P2P-client, P2P-GO } <= 1, #{ P2P-device } <= 1,\n" +
	"\t\t   total <= 3, #channels <= 2\n" +
	"\t\t * #{ managed } <= 1, #{ AP } <= 1, #{ P2P-client } <= 1,\n" +
	"\t\t   total <= 4, #channels <= 1\n" +
	"\tDevice supports something\n"

const brokenInfo = "Wiphy phy1\n" +
	"\tSupported interface modes:\n\t\t * managed\n\t\t * AP\n" +
	"\tvalid interface combinations:\n" +
	"\t\t * #{ managed, AP } <= 1,\n\t\t   total <= 1, #channels <= 1\n" +
	"\tDevice supports x\n"

func TestAnalyzeConcurrent(t *testing.T) {
	c := Analyze(intelInfo)
	if !c.AP || !c.Concurrent {
		t.Fatalf("want AP+concurrent, got %+v", c)
	}
	if c.SameChannel {
		t.Fatalf("first combo allows 2 channels, so SameChannel should be false: %+v", c)
	}
}

func TestAnalyzeNotConcurrent(t *testing.T) {
	c := Analyze(brokenInfo)
	if !c.AP || c.Concurrent {
		t.Fatalf("want AP but not concurrent, got %+v", c)
	}
}

func TestSharedGroupNeedsTwo(t *testing.T) {
	c := Combo{Groups: []Group{{[]string{"managed", "AP"}, 2}}}
	if !c.Allows("managed", "AP") {
		t.Fatal("limit 2 in a shared group should allow both")
	}
}

func TestFreqs(t *testing.T) {
	f := ParseFreqs(intelInfo)
	if !f[5260].Radar || !f[5180].NoIR || f[5180].Radar || !f[5955].Disabled || f[2412].NoIR {
		t.Fatalf("bad flags: %+v", f)
	}
}

func TestChannels(t *testing.T) {
	cases := map[int]int{2412: 1, 2437: 6, 2472: 13, 2484: 14, 5180: 36, 5745: 149, 5955: 1}
	for f, want := range cases {
		if got := FreqToChannel(f); got != want {
			t.Errorf("FreqToChannel(%d)=%d want %d", f, got, want)
		}
	}
	if BandOf(2437) != "2.4" || BandOf(5180) != "5" || BandOf(5955) != "6" {
		t.Error("bad bands")
	}
}

func TestParseIwDev(t *testing.T) {
	out := "phy#0\n\tInterface wlan0\n\t\tifindex 3\n\t\taddr aa:bb:cc:dd:ee:ff\n\t\tssid Home\n\t\ttype managed\n\t\tchannel 6 (2437 MHz), width: 20 MHz, center1: 2437 MHz\n" +
		"phy#1\n\tInterface wlan1\n\t\ttype managed\n"
	ifs := ParseIwDev(out)
	if len(ifs) != 2 || ifs[0].Phy != "phy0" || ifs[0].Channel != 6 || ifs[0].Freq != 2437 || ifs[0].SSID != "Home" || ifs[1].Phy != "phy1" || ifs[1].Channel != 0 {
		t.Fatalf("bad parse: %+v", ifs)
	}
}
