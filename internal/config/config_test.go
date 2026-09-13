package config

import (
	"strings"
	"testing"
)

func TestMergeEnv(t *testing.T) {
	got := MergeEnv([]string{"PATH=/bin", "CONCH_SOCKET=/outer.sock", "HOME=/h", "CONCH_SOCKET=/dup.sock"},
		"CONCH_SOCKET=/inner.sock", "TERM=xterm")
	if want := "PATH=/bin,HOME=/h,CONCH_SOCKET=/inner.sock,TERM=xterm"; strings.Join(got, ",") != want {
		t.Fatalf("got %v, want %s", got, want)
	}
}
