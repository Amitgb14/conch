package cloudagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Amitgb14/conch/internal/phone"
	"github.com/Amitgb14/conch/internal/proto"
)

// Push (PROTOCOL §8): when an agent starts waiting, the machine tells the
// account's devices itself, the notification encrypted on the machine to
// each device's own key (RFC 8291) and handed to the control plane, which
// delivers what it cannot read. The targets are fetched before a burst and
// kept at most five minutes.

// targetsFor is how long fetched targets are used (the contract's most).
var targetsFor = 5 * time.Minute

// PushMessage is what a notification says, inside the encryption: which
// machine and pane, and enough words to say who is waiting. The contract
// leaves the plaintext to conch; this is the phone gateway's message
// (phone.PushMessage) with the machine named, since a device is told about
// every machine of the account.
type PushMessage struct {
	Type    string `json:"type"` // waiting
	Machine string `json:"machine"`
	Pane    string `json:"pane"`
	Name    string `json:"name,omitempty"`
	Project string `json:"project,omitempty"`
}

type pusher struct {
	mu      sync.Mutex
	targets []PushTarget
	fetched time.Time
}

// pushWaiting tells the account's devices that p's agent waits. It runs
// aside: an event is never held up by the control plane.
func (a *Agent) pushWaiting(ctx context.Context, p proto.PaneInfo) {
	a.mu.Lock()
	msg := PushMessage{Type: "waiting", Machine: a.boot.MachineID, Pane: p.ID, Name: p.DisplayName(), Project: a.projects[p.ProjectID]}
	a.mu.Unlock()
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		n, err := a.push(ctx, msg)
		switch {
		case err != nil:
			a.logf("cloud: push for %s: %v", p.ID, err)
		case n > 0:
			a.logf("cloud: %s is waiting: pushed to %d device(s)", p.ID, n)
		}
	}()
}

// push encrypts msg to every target it can and posts them, saying to how
// many.
func (a *Agent) push(ctx context.Context, msg PushMessage) (int, error) {
	targets, err := a.pushTargets(ctx)
	if err != nil {
		return 0, err
	}
	var payloads []PushPayload
	for _, t := range targets {
		if t.Kind != "webpush" && t.Kind != "apns" {
			continue // a kind of a later contract
		}
		ct, err := encryptFor(msg, t)
		if err != nil {
			a.logf("cloud: push to %s: %v", t.DeviceID, err)
			continue
		}
		payloads = append(payloads, PushPayload{DeviceID: t.DeviceID, Ciphertext: base64.RawURLEncoding.EncodeToString(ct)})
	}
	if len(payloads) == 0 {
		return 0, nil
	}
	return len(payloads), a.api.Push(ctx, VMPush{Urgency: "high", Payloads: payloads})
}

// pushTargets are the account's devices, from the control plane at most
// targetsFor ago.
func (a *Agent) pushTargets(ctx context.Context) ([]PushTarget, error) {
	a.pushes.mu.Lock()
	defer a.pushes.mu.Unlock()
	if !a.pushes.fetched.IsZero() && time.Since(a.pushes.fetched) < targetsFor {
		return a.pushes.targets, nil
	}
	t, err := a.api.PushTargets(ctx)
	if err != nil {
		return nil, err
	}
	a.pushes.targets, a.pushes.fetched = t, time.Now()
	return t, nil
}

// encryptFor is msg encrypted for t, within its max_ciphertext_bytes: a
// message too long has its words cut — the name, then the project —
// never the machine or the pane, which say where to go.
func encryptFor(msg PushMessage, t PushTarget) ([]byte, error) {
	room := t.MaxCiphertextBytes - phone.PushOverhead
	if t.MaxCiphertextBytes <= 0 {
		room = 4096 - phone.PushOverhead
	}
	plain, err := fit(msg, room)
	if err != nil {
		return nil, err
	}
	return phone.EncryptPush(plain, phone.PushKeys{P256dh: t.P256dh, Auth: t.Auth})
}

// fit is msg as JSON in at most room bytes, its words shortened as far as
// they have to be.
func fit(msg PushMessage, room int) ([]byte, error) {
	for {
		b, _ := json.Marshal(msg)
		if len(b) <= room {
			return b, nil
		}
		over := len(b) - room
		switch {
		case msg.Name != "":
			msg.Name = cut(msg.Name, over)
		case msg.Project != "":
			msg.Project = cut(msg.Project, over)
		default:
			return nil, errors.New("too small a notification for even the pane's name")
		}
	}
}

// cut is s less at least over bytes, on a character's boundary, ending
// in an ellipsis while anything of it is left.
func cut(s string, over int) string {
	keep := len(s) - over - len("…")
	if keep <= 0 {
		return ""
	}
	for keep > 0 && !utf8.RuneStart(s[keep]) {
		keep--
	}
	return s[:keep] + "…"
}
