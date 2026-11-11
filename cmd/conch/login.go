package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Amitgb14/conch/internal/cloud"
	"github.com/Amitgb14/conch/internal/proto"
	"github.com/Amitgb14/conch/internal/remote"
)

// syncCatalog puts the account's machines in conch's catalog, where the
// TUI, Conch.app and -m find machines. Failing to is said, not fatal.
func syncCatalog(ctx context.Context, c *cloud.Client) {
	ms, err := c.Machines(ctx)
	if err == nil {
		err = remote.SyncCloudMachines(ms)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "conch: the cloud machines were not added to conch's machines: %v\n", err)
	}
}

// conch login, logout and account: this device's Conch Cloud sign-in.
// Nothing here asks the network unless somebody has signed in, or asks to.

// cloudOut is where these commands write; a variable for tests.
var cloudOut io.Writer = os.Stdout

// loginEvent is one line of `conch login -json`, for a window (Conch.app)
// to draw the code and know when it is done: "code" first, then
// "signed_in" or "error".
type loginEvent struct {
	Event                   string `json:"event"`
	UserCode                string `json:"user_code,omitempty"`
	VerificationURI         string `json:"verification_uri,omitempty"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in,omitempty"`
	API                     string `json:"api,omitempty"`
	Handle                  string `json:"handle,omitempty"`
	Error                   string `json:"error,omitempty"`
}

func runLogin(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	noBrowser := fs.Bool("no-browser", false, "print the link and code; don't open the browser")
	asJSON := fs.Bool("json", false, "say each step as a line of JSON (for Conch.app)")
	client := fs.String("client", cloud.ClientCLI, "who is signing in: cli, tui or mac_app")
	name := fs.String("name", "", "this device's name on the account (default: conch on HOSTNAME)")
	api := fs.String("api", "", "the Conch Cloud server (default: CONCH_CLOUD_URL, [cloud] api, or Conch Cloud)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() > 0 {
		return errors.New("usage: conch login [-no-browser] [-json] [-api URL]")
	}
	server := strings.TrimRight(*api, "/")
	if server == "" {
		server = cloud.APIURL()
	}
	deviceName := *name
	if deviceName == "" {
		host, _ := os.Hostname()
		host = strings.TrimSuffix(host, ".local")
		deviceName = "conch on " + host
		if *client == cloud.ClientMacApp {
			deviceName = "Conch.app on " + host
		}
	}
	emit := func(e loginEvent) {
		if *asJSON {
			b, _ := json.Marshal(e)
			fmt.Fprintln(cloudOut, string(b))
		}
	}
	fail := func(err error) error {
		emit(loginEvent{Event: "error", Error: err.Error()})
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	da, err := cloud.StartLogin(ctx, server, *client, deviceName, proto.Version)
	if err != nil {
		return fail(err)
	}
	link := da.VerificationURIComplete
	if link == "" {
		link = da.VerificationURI
	}
	emit(loginEvent{Event: "code", UserCode: da.UserCode, VerificationURI: da.VerificationURI, VerificationURIComplete: da.VerificationURIComplete, ExpiresIn: da.ExpiresIn, API: server})
	if !*asJSON {
		fmt.Fprintf(cloudOut, "Signing in to Conch Cloud (%s).\n\n  Your code: %s\n\n  Approve it at %s\n\n", server, da.UserCode, link)
	}
	if !*noBrowser {
		if err := openInBrowser(link); err != nil && !*asJSON {
			fmt.Fprintf(cloudOut, "(could not open the browser: %v; open the link yourself)\n", err)
		}
	}
	if !*asJSON {
		fmt.Fprintln(cloudOut, "Waiting for you to approve it…")
	}
	c, err := cloud.FinishLogin(ctx, server, da)
	if err != nil {
		return fail(err)
	}
	handle := ""
	if me, err := c.Me(ctx); err == nil {
		handle = me.Handle
	}
	syncCatalog(ctx, c)
	emit(loginEvent{Event: "signed_in", API: server, Handle: handle})
	if !*asJSON {
		who := handle
		if who == "" {
			who = "your account"
		}
		fmt.Fprintf(cloudOut, "Signed in to Conch Cloud as %s.\n", who)
	}
	return nil
}

func runLogout(args []string) error {
	if len(args) > 0 {
		return errors.New("usage: conch logout")
	}
	c, err := cloud.Open()
	if errors.Is(err, cloud.ErrNotSignedIn) {
		fmt.Fprintln(cloudOut, "Not signed in to Conch Cloud.")
		return nil
	}
	if err != nil {
		// A sign-in that cannot be read is still forgotten.
		if ferr := cloud.Forget(); ferr != nil {
			return ferr
		}
		_ = remote.ForgetCloudMachines()
		fmt.Fprintf(cloudOut, "Signed out here (%v).\n", err)
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	who := c.Account().Handle
	err = c.Logout(ctx)
	if ferr := remote.ForgetCloudMachines(); ferr != nil && err == nil {
		err = ferr
	}
	if err != nil {
		return err
	}
	if who != "" {
		fmt.Fprintf(cloudOut, "Signed out of Conch Cloud (%s).\n", who)
	} else {
		fmt.Fprintln(cloudOut, "Signed out of Conch Cloud.")
	}
	return nil
}

// accountJSON is `conch account -json`: signed_in false, with nothing
// asked of the network, when there is no sign-in.
type accountJSON struct {
	SignedIn bool           `json:"signed_in"`
	API      string         `json:"api"`
	DeviceID string         `json:"device_id,omitempty"`
	Me       *cloud.Me      `json:"me,omitempty"`
	Devices  []cloud.Device `json:"devices,omitempty"`
	Error    string         `json:"error,omitempty"`
}

func runAccount(args []string) error {
	fs := flag.NewFlagSet("account", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print it as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := cloud.Open()
	if errors.Is(err, cloud.ErrNotSignedIn) {
		if *asJSON {
			return writeJSONOut(accountJSON{API: cloud.APIURL()})
		}
		fmt.Fprintf(cloudOut, "Not signed in to Conch Cloud (%s). conch login signs this device in.\n", cloud.APIURL())
		return nil
	}
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	acct := c.Account()
	me, err := c.Me(ctx)
	if err != nil {
		if *asJSON && !errors.Is(err, cloud.ErrSignedOut) {
			writeJSONOut(accountJSON{SignedIn: true, API: acct.API, DeviceID: acct.DeviceID, Error: err.Error()})
		}
		return err
	}
	devices, err := c.Devices(ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return writeJSONOut(accountJSON{SignedIn: true, API: acct.API, DeviceID: acct.DeviceID, Me: &me, Devices: devices})
	}
	a := me.Allowance
	fmt.Fprintf(cloudOut, "Signed in to Conch Cloud (%s) as %s", acct.API, me.Handle)
	if me.Email != "" {
		fmt.Fprintf(cloudOut, " <%s>", me.Email)
	}
	fmt.Fprintf(cloudOut, "\nPlan: %s\n", me.Plan)
	fmt.Fprintf(cloudOut, "Running time this month: %s of %s", hours(a.RunningSecondsUsed), hours(a.RunningSeconds))
	if !a.PeriodEnd.IsZero() {
		fmt.Fprintf(cloudOut, " (until %s)", a.PeriodEnd.Local().Format("2 Jan"))
	}
	plural := "s"
	if a.MaxMachines == 1 {
		plural = ""
	}
	fmt.Fprintf(cloudOut, "\nStorage: %.1f of %d GB · up to %d machine%s\n\nDevices:\n", a.StorageGBUsed, a.StorageGB, a.MaxMachines, plural)
	tw := tabwriter.NewWriter(cloudOut, 0, 4, 2, ' ', 0)
	for _, d := range devices {
		mark := ""
		if d.Current {
			mark = "  (this one)"
		}
		fmt.Fprintf(tw, "  %s\t%s\tlast seen %s%s\n", d.Name, d.Client, d.LastSeenAt.Local().Format("2 Jan 15:04"), mark)
	}
	return tw.Flush()
}

// hours says seconds as hours, to a tenth.
func hours(s int) string {
	h := float64(s) / 3600
	if h == float64(int(h)) {
		return fmt.Sprintf("%d h", int(h))
	}
	return fmt.Sprintf("%.1f h", h)
}

func writeJSONOut(v any) error {
	enc := json.NewEncoder(cloudOut)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
