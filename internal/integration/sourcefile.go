package integration

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Source is the text of the integration name: the installed file, else
// the one conch ships, and whether it is that one.
func Source(name string) (data []byte, shipped bool, err error) {
	if err := CheckName(name); err != nil {
		return nil, false, err
	}
	data, err = os.ReadFile(filepath.Join(Dir(), name+".toml"))
	if err == nil {
		return data, false, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, false, err
	}
	if data, err := shippedFS.ReadFile("files/" + name + ".toml"); err == nil {
		return data, true, nil
	}
	return nil, false, fmt.Errorf("no integration %s here: conch integration ls lists them", name)
}
