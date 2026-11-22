package sandbox

import (
	"os"
	"testing"
	"time"
)

func TestDeletedRecord(t *testing.T) {
	t.Setenv("CONCH_HOME", t.TempDir())
	now := time.Now()
	// Nothing recorded: nothing deleted, and forgetting writes nothing.
	if got := Deleted("daytona", now); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	ForgetDeleted("daytona", nil, now)
	if _, err := os.Stat(DeletedFile()); err == nil {
		t.Fatal("forgetting nothing wrote a file")
	}
	NoteDeleted("daytona", "old", now.Add(-2*DeletingFor))
	NoteDeleted("daytona", "new", now)
	NoteDeleted("daytona", "edge", now.Add(-DeletingFor)) // exactly the limit is over
	NoteDeleted("boat", "other", now)
	got := Deleted("daytona", now)
	if len(got) != 1 || !got["new"] {
		t.Fatalf("daytona: %v", got)
	}
	if fi, err := os.Stat(DeletedFile()); err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("record: %v %v", fi, err)
	}
	// Forgetting daytona's keeps only what it still lists, and boat's.
	ForgetDeleted("daytona", map[string]bool{"new": true}, now)
	if m := readDeleted(); len(m) != 2 {
		t.Fatalf("after forgetting: %v", m)
	}
	ForgetDeleted("daytona", nil, now)
	ForgetDeleted("boat", nil, now)
	if _, err := os.Stat(DeletedFile()); err == nil {
		t.Fatal("an empty record is left behind")
	}
	// A damaged file reads as empty and is written over.
	os.WriteFile(DeletedFile(), []byte("{not json"), 0o600)
	if got := Deleted("daytona", now); len(got) != 0 {
		t.Fatalf("damaged: %v", got)
	}
	NoteDeleted("daytona", "d", now)
	if !Deleted("daytona", now)["d"] {
		t.Fatal("not written over a damaged file")
	}
}

func TestPollFaster(t *testing.T) {
	old := pollEvery
	t.Run("inner", func(t *testing.T) {
		PollFaster(t, time.Millisecond)
		if pollEvery != time.Millisecond {
			t.Fatalf("%v", pollEvery)
		}
	})
	if pollEvery != old {
		t.Fatalf("not put back: %v", pollEvery)
	}
}
