package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/relay"
)

const relayUsage = `usage: conch relay [-listen ADDR] [-cert FILE -key FILE]`

// runRelay is `conch relay`: a relay phones and conch webs meet at, for
// a server of your own. It forwards Noise sessions it cannot read, keeps
// nothing, and serves the phone's app so a phone can open its address.
func runRelay(args []string) error {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	listen := fs.String("listen", ":8443", "address to listen on")
	cert := fs.String("cert", "", "TLS certificate, to serve HTTPS here rather than behind a proxy that does")
	key := fs.String("key", "", "the certificate's key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New(relayUsage)
	}
	if (*cert == "") != (*key == "") {
		return errors.New("-cert and -key go together")
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	srv := &relay.Server{UI: phone.AppHandler(), Logf: log.New(os.Stderr, "", log.LstdFlags).Printf}
	hs := &http.Server{Handler: srv, ReadHeaderTimeout: 10 * time.Second}
	scheme := "http"
	if *cert != "" {
		scheme = "https"
	}
	fmt.Printf("conch relay: listening on %s://%s\n", scheme, ln.Addr())
	if *cert == "" {
		// A phone's browser does its encryption only on an https page.
		fmt.Println("phones need HTTPS: put a proxy that has a certificate in front (Caddy: reverse_proxy to this),")
		fmt.Println("or pass -cert and -key. Then set [web] relay = \"wss://<that name>\" on each computer.")
	} else {
		fmt.Printf("on each computer: [web] relay = \"wss://<this server's name>%s\" in config.toml\n", portSuffix(ln.Addr()))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() {
		if *cert != "" {
			errc <- hs.ServeTLS(ln, *cert, *key)
		} else {
			errc <- hs.Serve(ln)
		}
	}()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	srv.Close() // gateways reconnect rather than wait on a relay that's gone
	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	return nil
}

func portSuffix(a net.Addr) string {
	_, p, err := net.SplitHostPort(a.String())
	if err != nil || p == "443" {
		return ""
	}
	return ":" + p
}
