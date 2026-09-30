package phone

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
)

// DefaultPort is where the gateway listens unless told otherwise.
const DefaultPort = 8722

// Tailscale hands every machine an address from these ranges.
var (
	tailscale4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscale6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// ErrNoTailscale is a machine with no Tailscale address to listen on.
var ErrNoTailscale = errors.New("this machine has no Tailscale address. conch web listens only there, " +
	"so the phone reaches it over your tailnet and nobody else can: start Tailscale, " +
	"or name an address yourself with -listen ADDR")

// TailscaleAddr picks the machine's Tailscale address out of its
// interface addresses, IPv4 before IPv6.
func TailscaleAddr(addrs []net.Addr) (netip.Addr, bool) {
	var six netip.Addr
	for _, a := range addrs {
		ipn, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		ip, ok := netip.AddrFromSlice(ipn.IP)
		if !ok {
			continue
		}
		ip = ip.Unmap()
		if tailscale4.Contains(ip) {
			return ip, true
		}
		if tailscale6.Contains(ip) && !six.IsValid() {
			six = ip
		}
	}
	return six, six.IsValid()
}

// ListenAddr decides where the gateway listens. With nothing asked for it
// is the Tailscale address and only that; listen names another, and comes
// back with a warning to show, since that is remote access to the machine
// offered to whoever can reach the address.
func ListenAddr(listen string, port int, addrs []net.Addr) (addr, warning string, err error) {
	if listen == "" {
		ip, ok := TailscaleAddr(addrs)
		if !ok {
			return "", "", ErrNoTailscale
		}
		return netip.AddrPortFrom(ip, uint16(port)).String(), "", nil
	}
	host, p, err := net.SplitHostPort(listen)
	if err != nil {
		host, p = listen, strconv.Itoa(port) // an address without a port
		if _, _, err := net.SplitHostPort(net.JoinHostPort(host, p)); err != nil {
			return "", "", fmt.Errorf("-listen %q: %v", listen, err)
		}
	}
	addr = net.JoinHostPort(host, p)
	if ip, perr := netip.ParseAddr(host); perr == nil {
		if ip = ip.Unmap(); tailscale4.Contains(ip) || tailscale6.Contains(ip) {
			return addr, "", nil
		}
	}
	return addr, "listening on " + addr + ", which is not this machine's Tailscale address: " +
		"anyone who can reach it can try pairing codes, and a paired device can type into your agents", nil
}
