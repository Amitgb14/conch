package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/client"
	"github.com/Amitgb14/conch/internal/proto"
)

const (
	uploadUsage   = "usage: conch [-m MACHINE] upload [-ssh PANE] [-to DIR] [-f] PATH..."
	downloadUsage = "usage: conch [-m MACHINE] download [-ssh PANE] [-to DIR] [-f] PATH..."
)

// tooOldForSSH is said when the machine's conch can't reach the host a
// terminal there is logged in to.
const tooOldForSSH = "the conch server there is older and can't copy to or from an ssh terminal's host; update it (`conch machine upgrade`, or reload the server here)"

// sshPane turns -ssh's pane, an ID or a name, into the ID, once the server
// is known to reach that pane's host (fs.ssh.v1). Empty is the machine
// itself.
func sshPane(c *client.Client, ref string) (string, error) {
	if ref == "" {
		return "", nil
	}
	if len(c.MissingCapabilities([]string{proto.CapFSSSH})) > 0 {
		return "", errors.New(tooOldForSSH)
	}
	return resolvePane(c, ref)
}

// tooOldToCopy is said when the machine's conch takes dropped files but not
// files sent to a folder, folders, or copies back.
const tooOldToCopy = "the conch server there is older and can't copy files to a folder, copy folders, or copy anything back; update it with `conch machine upgrade`"

// runUpload copies local files and folders to a machine: into a folder
// there with -to, else its uploads folder as dropping them on one of its
// panes does. It prints where each one landed.
func runUpload(args []string) error {
	fs := flag.NewFlagSet("upload", flag.ContinueOnError)
	to := fs.String("to", "", "the folder to copy into there (~ is its home); default: conch's uploads folder, or with -ssh the home folder")
	force := fs.Bool("f", false, "write over a file or into a folder of the same name, rather than numbering the copy")
	ssh := fs.String("ssh", "", "the pane (ID or name) of a terminal logged in with ssh: copy to that host instead")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return errors.New(uploadUsage)
	}
	// An ssh host has no uploads folder of conch's: its home it is.
	if *ssh != "" && *to == "" {
		*to = "~"
	}
	// Every path is looked at before anything is sent, so a typo in the
	// last one doesn't leave the first ones copied.
	folders := false
	for _, path := range paths {
		st, err := os.Stat(path)
		switch {
		case err != nil:
			return err
		case st.IsDir():
			folders = true
		case !st.Mode().IsRegular():
			return fmt.Errorf("%s is not a file or folder", path)
		}
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{"fs.upload.v1"})) > 0 {
		return errors.New("the conch server there is older and can't take uploads; update it with `conch machine upgrade`")
	}
	if (*to != "" || folders) && len(c.MissingCapabilities([]string{proto.CapFSTransfer})) > 0 {
		return errors.New(tooOldToCopy)
	}
	pane, err := sshPane(c, *ssh)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for _, path := range paths {
		up, err := client.OpenUpload(c, path)
		if err != nil {
			return err
		}
		if *to != "" {
			up.Into(*to, *force)
		}
		up.On(pane)
		err = runSteps(ctx, up.Step, up.Timeout)
		up.Close()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Println(up.Path())
	}
	return nil
}

// runDownload copies files and folders from a machine into a folder here,
// and prints where each one landed.
func runDownload(args []string) error {
	fs := flag.NewFlagSet("download", flag.ContinueOnError)
	to := fs.String("to", ".", "the folder here to copy into")
	force := fs.Bool("f", false, "write over a file or into a folder of the same name, rather than numbering the copy")
	ssh := fs.String("ssh", "", "the pane (ID or name) of a terminal logged in with ssh: copy from that host instead")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return errors.New(downloadUsage)
	}
	dest, err := filepath.Abs(*to)
	if err != nil {
		return err
	}
	if st, err := os.Stat(dest); err != nil {
		return err
	} else if !st.IsDir() {
		return fmt.Errorf("%s is not a folder", *to)
	}
	c, err := connect(false)
	if err != nil {
		return err
	}
	defer c.Close()
	if len(c.MissingCapabilities([]string{proto.CapFSTransfer})) > 0 {
		return errors.New(tooOldToCopy)
	}
	pane, err := sshPane(c, *ssh)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	for _, path := range paths {
		d := client.NewDownload(c, path, dest, *force).On(pane)
		err := runSteps(ctx, d.Step, d.Timeout)
		d.Close() // what an interrupted one wrote
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Println(d.Path())
	}
	return nil
}

// runSteps runs a transfer to the end, each step with its own time limit:
// a whole copy has none, since it is as long as the file is big.
func runSteps(ctx context.Context, step func(context.Context) (bool, error), timeout func() time.Duration) error {
	for {
		sctx, cancel := context.WithTimeout(ctx, timeout())
		done, err := step(sctx)
		cancel()
		if err != nil {
			if ctx.Err() != nil {
				return errors.New("interrupted")
			}
			return err
		}
		if done {
			return nil
		}
	}
}
