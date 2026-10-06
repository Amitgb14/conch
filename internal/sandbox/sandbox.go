// Package sandbox creates and manages sandboxes with a hosted provider. A
// sandbox is a machine to the rest of conch: once it runs, conch installs
// itself there and reaches it over ssh like any other. This package only
// talks to the provider; it knows nothing of conch's servers.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/conch/internal/config"
)

// State is a sandbox's state as its provider reports it.
type State string

// The states conch acts on; a provider may report others (archiving,
// resizing…), which pass through as they are.
const (
	StateCreating    State = "creating"
	StateStarting    State = "starting"
	StateStarted     State = "started"
	StateStopping    State = "stopping"
	StateStopped     State = "stopped"
	StateArchived    State = "archived"
	StateError       State = "error"
	StateBuildFailed State = "build_failed"
	StateDestroying  State = "destroying"
	StateDestroyed   State = "destroyed"
)

// failed reports whether the sandbox can't reach started without help.
func (s State) failed() bool {
	return s == StateError || s == StateBuildFailed || s == StateDestroying || s == StateDestroyed
}

// comingUp reports whether the sandbox is already on its way to started,
// so starting it again would be refused.
func (s State) comingUp() bool {
	switch s {
	case StateCreating, StateStarting, "pulling_snapshot", "building_snapshot", "pending_build", "restoring", "resuming":
		return true
	}
	return false
}

// Going reports whether the sandbox is on its way out, or already gone:
// nothing brings it back, so conch treats it as no longer there rather
// than waiting for a state that never settles.
func (s State) Going() bool { return s == StateDestroying || s == StateDestroyed }

// Moving reports whether the sandbox is on its way to another state, so
// asking again shortly will find it settled.
func (s State) Moving() bool {
	switch s {
	case StateStopping, "archiving", "resizing", "snapshotting", "forking", "pausing":
		return true
	}
	return s.comingUp()
}

// Sandbox is a provider's sandbox.
type Sandbox struct {
	ID      string
	Name    string
	State   State
	Reason  string // why it is in an error state, when the provider says
	Target  string // region
	CPU     int    // vCPUs
	Memory  int    // GiB
	Disk    int    // GiB
	Labels  map[string]string
	Created time.Time
}

// Spec is what to create. Zero values leave the provider's defaults.
type Spec struct {
	Name     string
	Snapshot string
	CPU      int // vCPUs
	Memory   int // GiB
	Disk     int // GiB
	Env      map[string]string
	Labels   map[string]string
	// AutoStop is the minutes without activity before the provider stops
	// the sandbox; 0 never does.
	AutoStop int
}

// Access is how to reach a sandbox over ssh, until Expires.
type Access struct {
	// User is who to log in as. With a provider whose gateway takes the
	// API token as the username it is a secret and is kept out of
	// messages; PlainUser says it is an ordinary account name instead,
	// which must not be hidden — "user" would be struck out of every
	// sentence that happened to contain it.
	User      string
	PlainUser bool
	Host      string
	Port      int // 0 is ssh's default
	Expires   time.Time
}

// Target is the ssh destination for Access.
func (a Access) Target() string {
	if a.Port != 0 && a.Port != 22 {
		return "ssh://" + a.User + "@" + a.Host + ":" + strconv.Itoa(a.Port)
	}
	return a.User + "@" + a.Host
}

// Provider creates and manages sandboxes. Create and Start return once the
// sandbox is started, or the context ends.
type Provider interface {
	// Name is the provider, e.g. "daytona".
	Name() string
	// Check says whether the provider can be used: ErrNotConfigured when
	// it has no credentials.
	Check() error
	Create(ctx context.Context, spec Spec) (Sandbox, error)
	Get(ctx context.Context, id string) (Sandbox, error)
	// List returns the sandboxes conch created.
	List(ctx context.Context) ([]Sandbox, error)
	Start(ctx context.Context, id string) (Sandbox, error)
	Stop(ctx context.Context, id string) error
	Delete(ctx context.Context, id string) error
	// SSHAccess returns fresh credentials to reach a started sandbox.
	SSHAccess(ctx context.Context, id string) (Access, error)
}

// Previewer is a provider that can give a link to a port inside a
// sandbox, so a dev server an agent started can be looked at. Not every
// provider can, so it is asked for separately.
type Previewer interface {
	// PreviewURL is a link to port inside the sandbox that a browser can
	// open on its own, good for about the time asked for. Whatever
	// credential it needs is in the link.
	PreviewURL(ctx context.Context, id string, port int, expires time.Duration) (string, error)
}

// Snapshotter is a provider that can keep a sandbox as it stands and make
// new ones from it: a checkout, its dependencies and an agent's login,
// set up once and started again in a minute.
type Snapshotter interface {
	// Snapshot keeps the sandbox under name. What the provider needs of
	// the sandbox first — stopped, usually — is its own business to
	// refuse.
	Snapshot(ctx context.Context, id, name string) error
	// Snapshots lists what has been kept, newest first.
	Snapshots(ctx context.Context) ([]Snap, error)
	// ForgetSnapshot removes one.
	ForgetSnapshot(ctx context.Context, name string) error
}

// Snap is a kept sandbox, as far as conch shows it.
type Snap struct {
	Name    string
	State   string // the provider's own word: active, inactive, building…
	Size    string // vCPU, memory and disk, when the provider says
	Created time.Time
}

// Metered is a provider that can say what a sandbox has actually cost,
// rather than leaving conch to work it out from prices somebody typed.
type Metered interface {
	// Usage is what the sandbox has cost between from and to. Known is
	// false when the provider has nothing to say yet — its billing may
	// lag hours behind — which is not an error.
	Usage(ctx context.Context, id string, from, to time.Time) (Usage, error)
}

// Usage is what a provider says a sandbox has cost, and the periods it
// was billed in: a provider charges by what the sandbox was doing, so the
// periods say where the money went — running, or only keeping its disk.
type Usage struct {
	Cost     float64
	Known    bool
	From, To time.Time
	Periods  []UsagePeriod // newest last, as the provider gives them
}

// UsagePeriod is one stretch a sandbox was billed for.
type UsagePeriod struct {
	From, To time.Time
	CPU      int // vCPUs held; 0 while it is stopped
	MemGiB   int
	DiskGiB  int
	Cost     float64
}

// Running reports whether the sandbox was up for this period, rather than
// stopped and keeping its disk.
func (p UsagePeriod) Running() bool { return p.CPU > 0 || p.MemGiB > 0 }

// KeyAuthorizer is a provider whose sandboxes are reached with a key of
// your own rather than a secret it hands out: conch authorizes its public
// key through the API and the private half never leaves this computer.
type KeyAuthorizer interface {
	AuthorizeKey(ctx context.Context, id, publicKey string) error
}

// NamedSizes is a provider whose machines come in named sizes rather than
// a number of vCPUs, GiB of memory and GiB of disk. The name goes where a
// snapshot does, and asking for numbers is refused.
type NamedSizes interface {
	// SizeNames lists the sizes, smallest first.
	SizeNames() []string
}

// Lifetime is a provider whose sandboxes are given a length of life when
// they are made rather than being stopped when nobody uses them, so it is
// worth saying how long before one is created.
type Lifetime interface {
	// Life is what a new sandbox gets, with this provider's settings.
	Life() time.Duration
}

// CommandAccess is a provider whose sandboxes are reached through a
// program of its own that wraps ssh — Devin's `devin ssh` — rather than at
// an address conch can ssh to itself. ssh's options and the command to run
// go after what it returns.
type CommandAccess interface {
	SSHCommand(id string) ([]string, error)
}

// Placed is a provider whose machines are neither sized nor made from a
// snapshot but placed: on a platform, or on a pool of machines of your
// own. That place's name goes where the snapshot does, and numbers are
// refused.
type Placed interface {
	// PlaceHint says what the snapshot field takes, for a placeholder.
	PlaceHint() string
}

// Noted is a provider with something to say before a sandbox is made with
// it: a cost or a limit a person would not expect of a machine.
type Noted interface {
	Note() string
}

// Providers lists the providers conch knows.
var Providers = []string{"daytona", "boat", "devin"}

// Known reports whether name is a provider conch knows.
func Known(name string) bool { return slices.Contains(Providers, name) }

// labels are provider names as people write them, where capitalising the
// first letter is not how it is done.
var labels = map[string]string{"e2b": "E2B", "boat": "boat.dev", "devin": "Devin Cloud"}

// ProviderLabel is a provider's name for people to read.
func ProviderLabel(name string) string {
	if l, ok := labels[name]; ok {
		return l
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Open returns the named provider, set up from cfg.
func Open(name string, cfg config.SandboxCfg) (Provider, error) {
	switch name {
	case "daytona":
		return NewDaytona(cfg.Of(name)), nil
	case "boat":
		return NewBoat(cfg.Of(name)), nil
	case "devin":
		return NewDevin(cfg.Of(name)), nil
	}
	return nil, fmt.Errorf("unknown sandbox provider %q", name)
}

// EnvFrom reads the named variables from this environment, to pass into a
// new sandbox. A name that isn't set is refused, rather than passed in
// empty for an agent to trip on.
func EnvFrom(names []string) (map[string]string, error) {
	vars := map[string]string{}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "= \t") {
			return nil, fmt.Errorf("env %q: give a variable name", name)
		}
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			return nil, fmt.Errorf("env %s: $%s isn't set here", name, name)
		}
		vars[name] = v
	}
	return vars, nil
}

// Label marks the sandboxes conch created, so List leaves others alone.
const Label = "conch"

var (
	// ErrNotConfigured is returned when a provider has no credentials.
	ErrNotConfigured = errors.New("sandbox provider not configured")
	// ErrNotFound is returned for a sandbox the provider doesn't have.
	ErrNotFound = errors.New("no such sandbox")
)

// FailedError is a sandbox that ended up in an error state while conch
// waited for it to start.
type FailedError struct {
	ID     string
	State  State
	Reason string
}

func (e *FailedError) Error() string {
	msg := fmt.Sprintf("sandbox %s is %s", e.ID, e.State)
	if e.Reason != "" {
		msg += ": " + e.Reason
	}
	return msg
}
