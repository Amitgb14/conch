package server

import (
	"fmt"
	"testing"
	"time"

	"github.com/Amitgb14/conch/internal/proto"
)

// TestPreviewCacheBounded: every push is a new key, so the cache drops
// what has expired as it goes and never holds more than previewsMax.
func TestPreviewCacheBounded(t *testing.T) {
	var c previewCache
	// Empty: nothing kept, nothing to drop.
	if _, ok := c.get("x"); ok {
		t.Fatal("an empty cache answered")
	}
	c.put("a", proto.BranchPreviews{})
	if _, ok := c.get("a"); !ok {
		t.Fatal("a fresh answer not kept")
	}

	// Expired answers go at the next put.
	c.mu.Lock()
	for i := range 50 {
		c.kept[fmt.Sprintf("old%d", i)] = previewsKept{at: time.Now().Add(-2 * previewsFor)}
	}
	c.mu.Unlock()
	c.put("b", proto.BranchPreviews{})
	c.mu.Lock()
	n := len(c.kept)
	c.mu.Unlock()
	if n != 2 {
		t.Fatalf("expired answers kept: %d entries, want 2", n)
	}

	// Many fresh answers at once: capped, the newest kept, the oldest gone.
	for i := range 3 * previewsMax {
		c.put(fmt.Sprintf("k%d", i), proto.BranchPreviews{})
	}
	c.mu.Lock()
	n = len(c.kept)
	c.mu.Unlock()
	if n > previewsMax {
		t.Fatalf("%d answers kept, cap %d", n, previewsMax)
	}
	if _, ok := c.get(fmt.Sprintf("k%d", 3*previewsMax-1)); !ok {
		t.Fatal("the newest answer was dropped")
	}

	// Exactly at the cap, the next put still fits.
	c.forgetBranch("")
	for i := range previewsMax {
		c.put(fmt.Sprintf("e%d", i), proto.BranchPreviews{})
	}
	c.put("last", proto.BranchPreviews{})
	c.mu.Lock()
	n = len(c.kept)
	c.mu.Unlock()
	if n != previewsMax {
		t.Fatalf("at the cap: %d, want %d", n, previewsMax)
	}
	if _, ok := c.get("last"); !ok {
		t.Fatal("the put at the cap was lost")
	}
}
