package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/preview"
)

// `conch preview` reaches the simulators and emulators on this computer:
// what Conch.app's preview tab draws beside an agent, and how an agent
// looks at its own work (`conch preview shot`, then reads the picture).
// It is this computer's alone: the phones run where Xcode and the Android
// SDK are, and no server is asked.

const previewUsage = "usage: conch preview devices [-json] | boot DEVICE | shot [-device D] [-o FILE] | tap [-device D] X Y | type [-device D] TEXT | button [-device D] home|back | detect [DIR]"

// previewTools is how the command finds the platforms' programs; a test
// gives its own.
var previewTools = func() *preview.Tools { return preview.Find(os.Environ()) }

// previewOut is where it prints; a test reads it.
var previewOut io.Writer = os.Stdout

func runPreview(args []string) error {
	if machineFlag != "" && machineFlag != "local" {
		return errors.New("conch preview reaches this computer's simulators and emulators; run it on " + machineFlag + " itself")
	}
	if len(args) == 0 {
		return errors.New(previewUsage)
	}
	sub, rest := args[0], args[1:]
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch sub {
	case "devices", "ls":
		return previewDevices(ctx, rest)
	case "detect":
		dir := "."
		if len(rest) > 1 {
			return errors.New("usage: conch preview detect [DIR]")
		}
		if len(rest) == 1 {
			dir = rest[0]
		}
		if st, err := os.Stat(dir); err != nil || !st.IsDir() {
			return fmt.Errorf("%s is not a folder", dir)
		}
		return json.NewEncoder(previewOut).Encode(preview.Detect(dir))
	case "boot":
		if len(rest) != 1 {
			return errors.New("usage: conch preview boot DEVICE")
		}
		t, d, err := previewDevice(ctx, rest[0], true)
		if err != nil {
			return err
		}
		if err := t.Boot(ctx, d); err != nil {
			return err
		}
		fmt.Fprintf(previewOut, "%s is starting\n", d.Name)
		return nil
	case "shot", "screenshot":
		fs := flag.NewFlagSet("preview shot", flag.ContinueOnError)
		dev := fs.String("device", "", "the device, by ID or name (default: the one running)")
		out := fs.String("o", "", "where to write the PNG (default: a new file in the temporary folder)")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() > 0 {
			return errors.New("usage: conch preview shot [-device D] [-o FILE]")
		}
		t, d, err := previewDevice(ctx, *dev, false)
		if err != nil {
			return err
		}
		path := *out
		if path == "" {
			path = filepath.Join(os.TempDir(), fmt.Sprintf("conch-preview-%s.png", time.Now().Format("20060102-150405.000")))
		}
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		if err := t.Shot(ctx, d, path); err != nil {
			return err
		}
		// The path alone, so an agent can read the picture from it.
		fmt.Fprintln(previewOut, path)
		return nil
	case "tap":
		fs := flag.NewFlagSet("preview tap", flag.ContinueOnError)
		dev := fs.String("device", "", "the device, by ID or name")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() != 2 {
			return errors.New("usage: conch preview tap [-device D] X Y   (pixels of conch preview shot's picture)")
		}
		x, errX := strconv.Atoi(fs.Arg(0))
		y, errY := strconv.Atoi(fs.Arg(1))
		if errX != nil || errY != nil {
			return fmt.Errorf("%s %s: X and Y are whole pixels", fs.Arg(0), fs.Arg(1))
		}
		t, d, err := previewDevice(ctx, *dev, false)
		if err != nil {
			return err
		}
		return t.Tap(ctx, d, x, y)
	case "type":
		fs := flag.NewFlagSet("preview type", flag.ContinueOnError)
		dev := fs.String("device", "", "the device, by ID or name")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() == 0 {
			return errors.New("usage: conch preview type [-device D] TEXT")
		}
		t, d, err := previewDevice(ctx, *dev, false)
		if err != nil {
			return err
		}
		return t.Type(ctx, d, strings.Join(fs.Args(), " "))
	case "button":
		fs := flag.NewFlagSet("preview button", flag.ContinueOnError)
		dev := fs.String("device", "", "the device, by ID or name")
		if err := fs.Parse(rest); err != nil {
			return err
		}
		if fs.NArg() != 1 {
			return errors.New("usage: conch preview button [-device D] " + strings.Join(preview.Buttons, "|"))
		}
		t, d, err := previewDevice(ctx, *dev, false)
		if err != nil {
			return err
		}
		return t.Button(ctx, d, fs.Arg(0))
	}
	return fmt.Errorf("unknown preview subcommand %q\n%s", sub, previewUsage)
}

// previewDevice finds the tools and the device meant. Booting needs a
// device named; everything else takes the one running when none is.
func previewDevice(ctx context.Context, want string, named bool) (*preview.Tools, preview.Device, error) {
	t := previewTools()
	devs, notes, err := t.Devices(ctx)
	if err != nil {
		return nil, preview.Device{}, err
	}
	if named && want == "" {
		return nil, preview.Device{}, errors.New("which device? conch preview devices lists them")
	}
	d, err := preview.Pick(devs, want)
	if err != nil && len(notes) > 0 {
		// What could not be listed may be why it was not found.
		err = fmt.Errorf("%w (%s)", err, strings.Join(notes, "; "))
	}
	return t, d, err
}

func previewDevices(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("preview devices", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print JSON: {devices, notes}")
	if err := fs.Parse(args); err != nil {
		return err
	}
	devs, notes, err := previewTools().Devices(ctx)
	if *asJSON {
		// The app reads this: a machine with no tools is an empty list
		// and the reason, not a failure it has to tell from others.
		if errors.Is(err, preview.ErrNoTools) {
			notes, err = []string{err.Error()}, nil
		}
		if err != nil {
			return err
		}
		if devs == nil {
			devs = []preview.Device{}
		}
		return json.NewEncoder(previewOut).Encode(struct {
			Devices []preview.Device `json:"devices"`
			Notes   []string         `json:"notes,omitempty"`
		}{devs, notes})
	}
	if err != nil {
		return err
	}
	if len(devs) == 0 {
		fmt.Fprintln(previewOut, "no simulators or emulators")
	}
	for _, d := range devs {
		about := d.OS
		if about == "" {
			about = d.Kind
		}
		fmt.Fprintf(previewOut, "%-9s %-28s %-12s %s\n", d.State, d.Name, about, d.ID)
	}
	for _, n := range notes {
		fmt.Fprintln(os.Stderr, "conch:", n)
	}
	return nil
}
