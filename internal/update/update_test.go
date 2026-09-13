package update

import (
	"testing"

	"github.com/Amitgb14/conch/internal/buildinfo"
	"github.com/Amitgb14/conch/internal/proto"
)

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"0.2.0", "0.1.9", 1}, {"v0.10.0", "0.9.0", 1}, {"1.0.0", "1.0.0", 0}, {"1.0", "1.0.0", 0},
		{"1.0.0-rc1", "1.0.0", -1}, {"1.0.0", "1.0.0-rc1", 1}, {"1.0.0-rc1", "1.0.0-rc2", -1}, {"0.1.0", "0.2.0", -1},
	} {
		if got := Compare(c.a, c.b); got != c.want {
			t.Errorf("Compare(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestSameBuild(t *testing.T) {
	old := proto.Version
	defer func() { proto.Version = old }()

	proto.Version = "0.1.0-dev"
	if same, ok := SameBuild(proto.HelloResult{BuildID: buildinfo.ID()}); !same || !ok {
		t.Fatal("same build ID")
	}
	if same, ok := SameBuild(proto.HelloResult{BuildID: "other"}); same || !ok {
		t.Fatal("different build ID")
	}
	if _, ok := SameBuild(proto.HelloResult{}); ok {
		t.Fatal("old servers can't tell")
	}

	proto.Version = "0.3.0"
	if same, _ := SameBuild(proto.HelloResult{Version: "0.3.0", BuildID: "linux-hash"}); !same {
		t.Fatal("releases compare versions")
	}
	if same, _ := SameBuild(proto.HelloResult{Version: "0.2.0"}); same {
		t.Fatal("older release")
	}
}
