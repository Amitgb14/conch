package main

import (
	"context"
	"strings"
	"testing"
)

// fakeApp records what would have reached the desktop.
func fakeApp() (*App, *[]string, *[]string, *[]string) {
	var notes, badges, sounds []string
	a := newApp()
	a.notify = func(_ context.Context, w Waiting) { notes = append(notes, w.Ref) }
	a.badge = func(l string) { badges = append(badges, l) }
	a.sound = func(n string) { sounds = append(sounds, n) }
	return a, &notes, &badges, &sounds
}

func TestAttentionAnnouncesEachWaitOnce(t *testing.T) {
	a, notes, badges, sounds := fakeApp()
	w1 := Waiting{Ref: "local:p1", Agent: "claude"}
	w2 := Waiting{Ref: "box:p4", Agent: "codex"}

	a.Attention(Attention{Waiting: []Waiting{w1}, Notify: true, Sound: "Glass"})
	a.Attention(Attention{Waiting: []Waiting{w1}, Notify: true, Sound: "Glass"}) // still waiting: not again
	a.Attention(Attention{Waiting: []Waiting{w1, w2}, Notify: true, Sound: "Glass"})
	if got := strings.Join(*notes, ","); got != "local:p1,box:p4" {
		t.Fatalf("notified %s", got)
	}
	if got := strings.Join(*sounds, ","); got != "Glass,Glass" {
		t.Fatalf("sounds %s: once per new wait, not per call", got)
	}
	// It stops waiting, then waits again: a new question, announced again.
	a.Attention(Attention{Waiting: []Waiting{w2}, Notify: true})
	a.Attention(Attention{Waiting: []Waiting{w1, w2}, Notify: true})
	if len(*notes) != 3 || (*notes)[2] != "local:p1" {
		t.Fatalf("notified %v", *notes)
	}
	if got := strings.Join(*badges, ","); got != "1,1,2,1,2" {
		t.Fatalf("badges %s", got)
	}
	a.Attention(Attention{})
	if last := (*badges)[len(*badges)-1]; last != "" {
		t.Fatalf("nobody waiting: badge %q", last)
	}
}

func TestAttentionQuietWhileFocusedOrOff(t *testing.T) {
	a, notes, badges, sounds := fakeApp()
	w := Waiting{Ref: "local:p1", Agent: "claude"}
	a.Attention(Attention{Waiting: []Waiting{w}, Focused: true, Notify: true, Sound: "Glass"})
	if len(*notes) != 0 || len(*sounds) != 0 {
		t.Fatalf("announced to a person already looking: %v %v", *notes, *sounds)
	}
	// Seen while focused counts as seen: blurring the window does not
	// bring an old question back as news.
	a.Attention(Attention{Waiting: []Waiting{w}, Notify: true, Sound: "Glass"})
	if len(*notes) != 0 {
		t.Fatalf("an old wait announced on blur: %v", *notes)
	}
	b, notes2, _, sounds2 := fakeApp()
	b.Attention(Attention{Waiting: []Waiting{w}, Notify: false, Sound: "Glass"})
	if len(*notes2) != 0 || len(*sounds2) != 0 {
		t.Fatal("announced with notifications off")
	}
	if (*badges)[0] != "1" {
		t.Fatal("the badge counts whatever the window's focus")
	}
}

func TestBadgeLabel(t *testing.T) {
	for n, want := range map[int]string{-1: "", 0: "", 1: "1", 42: "42", 99: "99", 100: "99+"} {
		if got := badgeLabel(n); got != want {
			t.Errorf("%d: %q", n, got)
		}
	}
}

func TestNotificationText(t *testing.T) {
	title, body := notificationText(Waiting{Agent: "claude", Title: "fix-cursor-test", Question: "Do you want to make this edit?"})
	if title != "claude needs you — fix-cursor-test" || body != "Do you want to make this edit?" {
		t.Fatalf("%q %q", title, body)
	}
	title, body = notificationText(Waiting{Agent: "codex", Title: "codex"})
	if title != "codex needs you" || body != "It is waiting for your answer." {
		t.Fatalf("%q %q", title, body)
	}
	_, body = notificationText(Waiting{Agent: "x", Question: strings.Repeat("é", 500)})
	if r := []rune(body); len(r) != 200 || !strings.HasSuffix(body, "…") {
		t.Fatalf("long question: %d runes", len(r))
	}
}

// A notification before the app has started has nowhere to go, and is
// dropped rather than crashing.
func TestNotifyBeforeStart(t *testing.T) {
	sendNotification(nil, Waiting{Ref: "local:p1"})
	if newApp().context() != nil {
		t.Fatal("a context before start")
	}
}
