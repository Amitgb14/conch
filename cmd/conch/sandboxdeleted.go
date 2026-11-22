package main

import (
	"time"

	"github.com/Amitgb14/conch/internal/sandbox"
)

// The record of what was just deleted is sandbox's (sandbox.NoteDeleted),
// shared with conch-sandbox; these are its names here.

const deletingFor = sandbox.DeletingFor

func deletedSandboxesFile() string { return sandbox.DeletedFile() }

func noteDeletedSandbox(provider, id string, at time.Time) { sandbox.NoteDeleted(provider, id, at) }

func deletedSandboxes(provider string, now time.Time) map[string]bool {
	return sandbox.Deleted(provider, now)
}

func forgetDeleted(provider string, listed map[string]bool, now time.Time) {
	sandbox.ForgetDeleted(provider, listed, now)
}
