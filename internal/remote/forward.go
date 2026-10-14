package remote

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// ForwardPort makes a port on a machine reachable from this computer
// through the connection conch already holds to it, and says which local
// port that is: the same number when it is free here, so a link written on
// the machine works unchanged, or another when something here holds it.
//
// It asks the running master (ssh -O forward) rather than starting a
// connection of its own: the bridge keeps that master up for as long as
// the machine is online, so the forward lasts as long as the machine does,
// and asking twice for the same port is answered by the forward already
// there.
func ForwardPort(ctx context.Context, target string, port int) (int, error) {
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d is out of range", port)
	}
	cfg, err := sshConfig()
	if err != nil {
		return 0, err
	}
	local := port
	if !localFree(port) {
		if local, err = anyLocalPort(); err != nil {
			return 0, err
		}
	}
	spec := fmt.Sprintf("%d:127.0.0.1:%d", local, port)
	out, err := exec.CommandContext(ctx, sshBinary(), "-F", cfg, "-O", "forward", "-L", spec, "--", target).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if strings.Contains(msg, "No such file") || strings.Contains(msg, "Control socket connect") {
			return 0, fmt.Errorf("conch is not connected to %s right now; reconnect it and try again", target)
		}
		if msg == "" {
			msg = err.Error()
		}
		return 0, fmt.Errorf("forwarding port %d from %s: %s", port, target, msg)
	}
	return local, nil
}

// localFree reports whether nothing on this computer listens on port.
// A variable so tests can say which ports are taken.
var localFree = func(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return false
	}
	ln.Close()
	return true
}

// anyLocalPort is a port free on this computer, chosen by the kernel.
func anyLocalPort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}
