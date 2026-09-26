// Package sandbox creates and manages sandboxes with a hosted provider. A
// sandbox is a machine to the rest of conch: once it runs, conch installs
// itself there and reaches it over ssh like any other. This package only
// talks to the provider; it knows nothing of conch's servers.
package sandbox

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
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
	User    string // a secret: it is the token that lets ssh in
	Host    string
	Port    int // 0 is ssh's default
	Expires time.Time
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

// Providers lists the providers conch knows.
var Providers = []string{"daytona"}

// Known reports whether name is a provider conch knows.
func Known(name string) bool { return slices.Contains(Providers, name) }

// Open returns the named provider, set up from cfg.
func Open(name string, cfg config.SandboxCfg) (Provider, error) {
	switch name {
	case "daytona":
		return NewDaytona(cfg.Daytona), nil
	}
	return nil, fmt.Errorf("unknown sandbox provider %q", name)
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
