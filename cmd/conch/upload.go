package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/conch/internal/client"
)

// runUpload copies local files to a machine's uploads folder, as dropping
// them on one of its panes does, and prints where each one landed.
func runUpload(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: conch [-m MACHINE] upload FILE...")
	}
	files := make([][]byte, len(args))
	for i, path := range args {
		st, err := os.Stat(path)
		switch {
		case err != nil:
			return err
		case !st.Mode().IsRegular():
			return fmt.Errorf("%s is not a file", path)
		}
		if files[i], err = os.ReadFile(path); err != nil {
			return err
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
	for i, path := range args {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		stored, err := client.NewUpload(c, filepath.Base(path), files[i]).Run(ctx)
		cancel()
		if err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		fmt.Println(stored)
	}
	return nil
}
