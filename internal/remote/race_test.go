package remote

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// catalogTargets lists the saved machines' targets, sorted.
func catalogTargets(t *testing.T) []string {
	t.Helper()
	ms, err := Machines()
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, m := range ms {
		got = append(got, m.Target)
	}
	sort.Strings(got)
	return got
}

// noTempFiles fails if a writer left a temp file behind in dir.
func noTempFiles(t *testing.T, dir string) {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

// Issue #6: goroutines saving different machines at once each got their
// machine written, with no lost update and no failed rename of a shared
// machines.json.tmp.
func TestCatalogConcurrentSaves(t *testing.T) {
	dir := a4Env(t)
	const n = 40
	var want []string
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		target := fmt.Sprintf("dev@box%02d", i)
		want = append(want, target)
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, err := SaveMachine(Machine{Target: target})
			if err == nil && m.Target != target {
				err = fmt.Errorf("saved %+v for %s", m, target)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	sort.Strings(want)
	if got := catalogTargets(t); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("saved %d machines, want %d:\n%v", len(got), len(want), got)
	}
	// IDs were chosen under the lock, so none repeats.
	ms, _ := Machines()
	ids := map[string]bool{}
	for _, m := range ms {
		if ids[m.ID] {
			t.Fatalf("duplicate id %q", m.ID)
		}
		ids[m.ID] = true
	}
	noTempFiles(t, dir)
}

// Renames and removals racing with saves don't undo each other either.
func TestCatalogConcurrentRenameRemoveSave(t *testing.T) {
	dir := a4Env(t)
	const n = 20
	for i := 0; i < n; i++ {
		if _, err := SaveMachine(Machine{Target: fmt.Sprintf("old%02d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 3*n)
	for i := 0; i < n; i++ {
		wg.Add(3)
		go func() {
			defer wg.Done()
			_, err := SaveMachine(Machine{Target: fmt.Sprintf("new%02d", i)})
			errs <- err
		}()
		go func() {
			defer wg.Done()
			ref := fmt.Sprintf("old%02d", i)
			if i%2 == 0 {
				errs <- RemoveMachine(ref)
				return
			}
			_, err := RenameMachine(ref, "renamed"+ref)
			errs <- err
		}()
		go func() {
			defer wg.Done()
			// Readers never see a missing or partial catalog.
			_, err := Machines()
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Error(err)
		}
	}
	ms, err := Machines()
	if err != nil {
		t.Fatal(err)
	}
	labels := map[string]string{}
	for _, m := range ms {
		labels[m.Target] = m.Label
	}
	for i := 0; i < n; i++ {
		if _, ok := labels[fmt.Sprintf("new%02d", i)]; !ok {
			t.Errorf("new%02d lost", i)
		}
		old := fmt.Sprintf("old%02d", i)
		label, ok := labels[old]
		switch {
		case i%2 == 0 && ok:
			t.Errorf("%s not removed", old)
		case i%2 == 1 && label != "renamed"+old:
			t.Errorf("%s label %q, want renamed%s", old, label, old)
		}
	}
	if len(ms) != n+n/2 {
		t.Errorf("%d machines, want %d", len(ms), n+n/2)
	}
	noTempFiles(t, dir)
}

// The lock is between processes, not only goroutines: separate conch
// processes adding machines at once, as in `conch machine add box$i &`.
func TestCatalogConcurrentProcesses(t *testing.T) {
	dir := a4Env(t)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	const procs, each = 8, 5
	var want []string
	cmds := make([]*exec.Cmd, procs)
	outs := make([]*bytes.Buffer, procs)
	for p := 0; p < procs; p++ {
		var targets []string
		for i := 0; i < each; i++ {
			targets = append(targets, fmt.Sprintf("p%d-box%d", p, i))
		}
		want = append(want, targets...)
		cmd := exec.Command(exe)
		cmd.Env = append(os.Environ(), "A4_HELPER_MODE=save", "A4_SAVE_TARGETS="+strings.Join(targets, ","))
		outs[p] = &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = outs[p], outs[p]
		cmds[p] = cmd
	}
	for _, cmd := range cmds {
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
	}
	for p, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Errorf("process %d: %v: %s", p, err, outs[p])
		}
	}
	sort.Strings(want)
	if got := catalogTargets(t); strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("saved %d machines, want %d:\n%v", len(got), len(want), got)
	}
	noTempFiles(t, dir)
}

// A lock that can't be taken is an error, not a silent unlocked write.
func TestCatalogLockUnavailable(t *testing.T) {
	dir := a4Env(t)
	if err := os.Mkdir(filepath.Join(dir, "machines.json.lock"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := SaveMachine(Machine{Target: "box"}); err == nil {
		t.Fatal("SaveMachine without the lock")
	}
	if _, err := RenameMachine("box", "b"); err == nil || strings.Contains(err.Error(), "no machine") {
		t.Fatalf("RenameMachine without the lock: %v", err)
	}
	if err := RemoveMachine("box"); err == nil || strings.Contains(err.Error(), "no machine") {
		t.Fatalf("RemoveMachine without the lock: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "machines.json")); !os.IsNotExist(err) {
		t.Fatalf("catalog written without the lock: %v", err)
	}

	// Under a file, the lock's directory can't be made.
	file := filepath.Join(dir, "file")
	os.WriteFile(file, nil, 0o600)
	t.Setenv("CONCH_HOME", filepath.Join(file, "conch"))
	if _, err := RenameMachine("box", "b"); err == nil {
		t.Fatal("RenameMachine under a file")
	}
	if err := RemoveMachine("box"); err == nil {
		t.Fatal("RemoveMachine under a file")
	}
}

// A second holder waits for the first to release the lock.
func TestCatalogLockExcludes(t *testing.T) {
	a4Env(t)
	unlock, err := lockCatalog()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := SaveMachine(Machine{Target: "box"})
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("SaveMachine ran while the catalog was locked: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	// Machines only reads, so it doesn't wait.
	if ms, err := Machines(); err != nil || len(ms) != 0 {
		t.Fatalf("read while locked: %v %v", ms, err)
	}
	unlock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := catalogTargets(t); len(got) != 1 || got[0] != "box" {
		t.Fatalf("after unlock: %v", got)
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f")
	// Replaces an existing file, including one with looser permissions.
	if err := os.WriteFile(path, []byte("old contents that are longer"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAtomic(path, []byte("new")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(path); string(b) != "new" {
		t.Fatalf("contents %q", b)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st, err)
	}
	// Empty data is a valid (empty) file.
	if err := writeFileAtomic(path, nil); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(path); err != nil || len(b) != 0 {
		t.Fatalf("empty write %q %v", b, err)
	}
	noTempFiles(t, dir)

	if err := writeFileAtomic(filepath.Join(dir, "missing", "f"), []byte("x")); err == nil {
		t.Fatal("write into a missing directory")
	}
	// The rename fails onto a non-empty directory; the temp file goes.
	sub := filepath.Join(dir, "sub")
	os.MkdirAll(filepath.Join(sub, "child"), 0o700)
	if err := writeFileAtomic(sub, []byte("x")); err == nil {
		t.Fatal("renamed over a directory")
	}
	noTempFiles(t, dir)
}

// Issue #6: other processes run ssh -F on the config while one rewrites it
// on start. A reader never finds it empty or half written.
func TestSSHConfigRewriteNeverPartial(t *testing.T) {
	a4Env(t)
	path, err := sshConfig()
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var readers sync.WaitGroup
	var mu sync.Mutex
	var bad []string
	reads := 0
	for r := 0; r < 4; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				b, err := os.ReadFile(path)
				mu.Lock()
				reads++
				if err != nil || !bytes.Equal(b, want) {
					bad = append(bad, fmt.Sprintf("%d bytes, %v", len(b), err))
				}
				mu.Unlock()
			}
		}()
	}
	var writers sync.WaitGroup
	for w := 0; w < 4; w++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 300; i++ {
				// Each pass is a fresh process's first ssh call.
				resetSSHConfig()
				if _, err := sshConfig(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	writers.Wait()
	close(stop)
	readers.Wait()
	if len(bad) > 0 {
		t.Fatalf("%d of %d reads saw a partial config, e.g. %s", len(bad), reads, bad[0])
	}
	noTempFiles(t, filepath.Dir(path))
}
