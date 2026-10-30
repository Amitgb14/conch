package main

import (
	"context"
	"sort"
	"strconv"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/runtime"
)

// Waiting is an agent the page says is waiting on the person.
type Waiting struct {
	Ref      string `json:"ref"` // machine:pane
	Title    string `json:"title"`
	Agent    string `json:"agent"`
	Question string `json:"question"`
}

// Attention is what the page tells the app each time who is waiting
// changes: the list, whether the window has the person's attention, and
// how to say so — the notification and its sound are the person's to turn
// off in Settings.
type Attention struct {
	Waiting []Waiting `json:"waiting"`
	Focused bool      `json:"focused"`
	Notify  bool      `json:"notify"`
	Sound   string    `json:"sound"` // a system sound's name; "" for none
}

// App is what the page reaches through the webview's bindings: the few
// things only the app itself can do.
type App struct {
	ctx context.Context

	mu   sync.Mutex
	seen map[string]bool // waiting refs already announced

	// The outward effects, behind variables so a test never shows a
	// notification, plays a sound or touches the Dock.
	notify func(ctx context.Context, w Waiting)
	badge  func(label string)
	sound  func(name string)
}

// context is the app's, once it has started; nil before.
func (a *App) context() context.Context {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.ctx
}

func newApp() *App {
	return &App{seen: map[string]bool{}, notify: sendNotification, badge: setBadge, sound: playSound}
}

// Attention is called by the page. Each agent that has just started
// waiting is announced once — a notification and a sound, only while the
// window is not in front, since the card on screen already says it — and
// the Dock carries how many wait. One that stops waiting and waits again
// is announced again.
func (a *App) Attention(at Attention) {
	a.mu.Lock()
	now := map[string]bool{}
	var fresh []Waiting
	for _, w := range at.Waiting {
		now[w.Ref] = true
		if !a.seen[w.Ref] {
			fresh = append(fresh, w)
		}
	}
	a.seen = now
	ctx := a.ctx
	a.mu.Unlock()

	a.badge(badgeLabel(len(at.Waiting)))
	if at.Focused || !at.Notify || len(fresh) == 0 {
		return
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].Ref < fresh[j].Ref })
	for _, w := range fresh {
		a.notify(ctx, w)
	}
	if at.Sound != "" {
		a.sound(at.Sound)
	}
}

// badgeLabel is what the Dock shows: nothing at none, the count otherwise.
func badgeLabel(n int) string {
	switch {
	case n <= 0:
		return ""
	case n > 99:
		return "99+"
	}
	return strconv.Itoa(n)
}

// notificationText is a waiting agent as a notification says it.
func notificationText(w Waiting) (title, body string) {
	title = w.Agent + " needs you"
	if w.Title != "" && w.Title != w.Agent {
		title += " — " + w.Title
	}
	body = w.Question
	if body == "" {
		body = "It is waiting for your answer."
	}
	if r := []rune(body); len(r) > 200 {
		body = string(r[:199]) + "…"
	}
	return title, body
}

func sendNotification(ctx context.Context, w Waiting) {
	if ctx == nil {
		return
	}
	title, body := notificationText(w)
	_ = runtime.SendNotification(ctx, runtime.NotificationOptions{
		ID: "conch-" + w.Ref, Title: title, Body: body, Data: map[string]interface{}{"ref": w.Ref},
	})
}

// startup keeps the app's context and asks once to send notifications;
// a click on one brings the window forward on that agent.
func (a *App) startup(ctx context.Context) {
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	if err := runtime.InitializeNotifications(ctx); err != nil {
		return
	}
	if ok, _ := runtime.CheckNotificationAuthorization(ctx); !ok {
		_, _ = runtime.RequestNotificationAuthorization(ctx)
	}
	runtime.OnNotificationResponse(ctx, func(r runtime.NotificationResult) {
		if r.Error != nil {
			return
		}
		ref, _ := r.Response.UserInfo["ref"].(string)
		runtime.WindowShow(ctx)
		runtime.WindowUnminimise(ctx)
		if ref != "" {
			runtime.EventsEmit(ctx, "open", ref)
		}
	})
}
