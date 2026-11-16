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
	Name string
	// Snapshot is the image or snapshot to start from.
	Snapshot string
	// Region and Size are a provider file's settings (format 4): where it
	// runs, and a named size for a provider whose machines come in them.
	Region string
	Size   string
	// Template is what the provider makes the machine from, where its file
	// offers templates beside images or snapshots (InstaBox's).
	Template string
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

// AccountKeyed is a provider whose gateway lets in the ssh keys registered
// with the person's account there, which conch cannot register with an API
// key: the transport offers the key it would have authorized, and the
// provider's Note says to register it.
type AccountKeyed interface {
	AccountKey() bool
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

// Noted is a provider with something to say before a sandbox is made with
// it: a cost or a limit a person would not expect of a machine.
type Noted interface {
	Note() string
}

// Names lists the providers that can be used here: the provider files in
// ProvidersDir that load, installed from the registry or of the person's
// own. conch has none of its own. It reads the folder each time, so a
// provider saved a moment ago is there. Tests replace it.
var Names = func() []string {
	good, _ := CustomProviders()
	return good
}

// Offered lists the providers a sandbox is made with from a menu: every
// one installed.
func Offered() []string { return Names() }

// Known reports whether name is a provider a machine may name: one
// installed, or one conch had built in, whose file is installed the first
// time such a machine is used.
func Known(name string) bool {
	if name == "" {
		return false
	}
	_, was := former[name]
	return was || slices.Contains(Names(), name)
}

// ProviderLabel is a provider's name for people to read: its file's
// label, else the one conch's own provider of that name had.
func ProviderLabel(name string) string {
	if providerName.MatchString(name) {
		if f, problems, err := ReadProviderFile(ProviderFilePath(name)); err == nil && len(problems) == 0 && f.Label != "" {
			return f.Label
		}
	}
	if f, was := former[name]; was {
		return f.label
	}
	if name == "" {
		return ""
	}
	return strings.ToUpper(name[:1]) + name[1:]
}

// Open returns the named provider, set up from cfg. A provider conch had
// built in has its file installed from the registry the first time.
func Open(name string, cfg config.SandboxCfg) (Provider, error) {
	if why, ok := dropped[name]; ok {
		return nil, fmt.Errorf("%w: %s", ErrDropped, why)
	}
	if _, was := former[name]; was && fileMissing(name) {
		if err := adopt(name); err != nil {
			return nil, err
		}
	}
	return openCustom(name, cfg)
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

// pause waits pollEvery between a wait's polls, or until ctx ends. A context
// already ended is said at once: in a select that also holds a timer, a
// timer that fired as the select was being set up is as ready as the
// context, Go picks between them at random, and the poll that followed a
// cancelled wait failed with the request's bare "context canceled" instead
// of what the wait was waiting for (TestBoatGoesWrong, on a loaded runner).
func pause(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tick(pollEvery):
		return nil
	}
}

// tick is the pause's timer; tests hand one that has already fired.
var tick = time.After
