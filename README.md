# dualhot

**Share your internet as a WiFi hotspot while staying connected to WiFi.** One command, any Linux.

Most Linux hotspot tools turn your WiFi off when the hotspot starts. `dualhot` uses a trick modern WiFi chips support: the one chip acts as a client *and* a hotspot at the same time. If your chip can't, it tells you, and falls back to a second adapter or a cable.

```
$ dualhot doctor          # can this machine do it? (no root needed)
$ sudo dualhot start --ssid MyHotspot --pass supersecret
$ dualhot status          # who is connected
$ sudo dualhot stop       # or press Ctrl+C
```

## Install

Needs: `iw`, `hostapd`, `dnsmasq`, and `nftables` (or `iptables`). `dualhot doctor` prints the exact command for your distro.

```
go install github.com/manuutechy/dualhot/cmd/dualhot@latest
```

Or build from source: `make build` (needs Go 1.22+).

## How it works

| Situation | Mode | What happens |
|---|---|---|
| WiFi chip supports client + hotspot together | `concurrent` | creates a virtual interface `ap0` on the same chip, on **your WiFi's channel** |
| Chip can't do both, but a 2nd adapter exists (USB dongle) | `dedicated` | the 2nd adapter is the hotspot |
| Internet comes from Ethernet / USB tethering | `dedicated` | the WiFi chip is the hotspot |
| None of the above | | `doctor` explains what to buy or plug in |

Steps `start` performs: create `ap0` → `hostapd` broadcasts the network → `dnsmasq` hands out addresses (10.42.77.0/24) → NAT shares the internet through private nftables tables (or tagged iptables rules). `stop` and Ctrl+C remove everything, including after a crash.

**Same-channel rule.** A chip doing both jobs can use only one channel. If your router moves to another channel, `dualhot` moves the hotspot with it (devices reconnect in a few seconds).

## Options

```
--ssid NAME      hotspot name (default dualhot-<hostname>)
--pass PASSWORD  8-63 characters (default: random, shown once)
--country CC     e.g. KE (default: system setting)
--uplink IFACE   interface with the internet (default: auto)
--channel N      dedicated adapter only
--subnet CIDR    default 10.42.77.0/24
--hidden         do not broadcast the name
--dry-run        print every command, change nothing
```

## Known limits

- 5 GHz DFS channels (52-144): a hotspot cannot start there. Use 2.4 GHz or channel 36-48 / 149-165 on your router.
- Some chips (many Broadcom and some Realtek) cannot do client + hotspot together. `doctor` tells you.
- IPv4 only for now. Docker's firewall can block forwarding in rare setups.
- WPA2-PSK only (WPA3 is planned).

## Safety

`dualhot` never edits your firewall or NetworkManager profiles. It adds its own nftables tables (`dualhot_nat`, `dualhot_fw`) and marks only the hotspot interface as unmanaged, and removes both on stop. Check any change first with `--dry-run`.

## Status

Early (v0.1). The detection logic and planning are unit-tested; real-hardware reports are welcome: run `dualhot doctor` and open an issue with the output.

MIT licence.
