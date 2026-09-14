package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestThresholds(t *testing.T) {
	for _, c := range []struct {
		in   []int
		want []int
	}{
		{nil, []int{80, 95}},
		{[]int{}, []int{80, 95}},
		{[]int{0, -5, 101}, []int{80, 95}}, // nothing valid: defaults
		{[]int{95, 50, 95, 80}, []int{50, 80, 95}},
		{[]int{100}, []int{100}},
		{[]int{1, 200}, []int{1}},
	} {
		if got := (NotifyCfg{LimitAt: c.in}).Thresholds(); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%v: %v, want %v", c.in, got, c.want)
		}
	}
	// The defaults returned can't be changed through the result.
	got := NotifyCfg{}.Thresholds()
	got[0] = 1
	if DefaultLimitAt[0] != 80 || Default().Notify.LimitAt[0] != 80 {
		t.Fatal("defaults were modified")
	}
}

// A config.toml written before plan limit alerts existed turns them on.
func TestLimitsDefaultForOldConfig(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("CONCH_HOME", dir)
	old := "[notify]\nenabled = true\ndesktop = false\nwaiting = true\ndone = false\n"
	if err := os.WriteFile(filepath.Join(dir, "config.toml"), []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Notify.Limits || !reflect.DeepEqual(cfg.Notify.LimitAt, []int{80, 95}) || cfg.Notify.Desktop {
		t.Fatalf("notify: %+v", cfg.Notify)
	}
	custom := "[notify]\nlimits = false\nlimit_at = [60, 90]\n"
	os.WriteFile(filepath.Join(dir, "config.toml"), []byte(custom), 0o600)
	if cfg, _ = Load(); cfg.Notify.Limits || !reflect.DeepEqual(cfg.Notify.Thresholds(), []int{60, 90}) {
		t.Fatalf("custom: %+v", cfg.Notify)
	}
}
