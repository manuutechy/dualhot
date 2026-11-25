package hotspot

import (
	"bufio"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// The running hotspot listens on a private socket so the app (running as the
// normal user) can switch it off without asking for the password a second time.

// SocketPath is the control socket.
func SocketPath() string { return filepath.Join(RunDir, "ctl.sock") }

func listenControl(stop func()) net.Listener {
	_ = os.Remove(SocketPath())
	ln, err := net.Listen("unix", SocketPath())
	if err != nil {
		return nil
	}
	_ = os.Chmod(SocketPath(), 0600)
	// hand the socket to the person who started us (pkexec or sudo), nobody else
	for _, env := range []string{"PKEXEC_UID", "SUDO_UID"} {
		if uid, err := strconv.Atoi(os.Getenv(env)); err == nil {
			_ = os.Chown(SocketPath(), uid, -1)
			break
		}
	}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(c).ReadString('\n')
			if strings.TrimSpace(line) == "stop" {
				stop()
			}
			c.Close()
		}
	}()
	return ln
}

// SendStop asks a running hotspot (started by this user) to shut down.
func SendStop() error {
	c, err := net.Dial("unix", SocketPath())
	if err != nil {
		return err
	}
	defer c.Close()
	_, err = c.Write([]byte("stop\n"))
	return err
}
