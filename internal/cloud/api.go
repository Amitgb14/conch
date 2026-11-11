// Package cloud is conch's client of Conch Cloud's control plane: signing
// a device in (the device flow), keeping its tokens, and the calls a
// person's conch makes — the account, its devices, its machines and how
// to reach one (contract conch-cloud/contract, openapi.yaml's userToken
// side and PROTOCOL.md §3.2).
//
// Nothing here runs unless somebody signed in: Open answers
// ErrNotSignedIn from what is on disk, without a request, so a conch with
// no account behaves exactly as one without this package.
package cloud

import (
	"os"

	"github.com/Amitgb14/conch/internal/config"
	"strings"
	"time"
)

// Contract is the conch-cloud contract version this client speaks, sent
// as Conch-Contract on every request (PROTOCOL §1).
const Contract = "1.4.0"

// DefaultURL is the contract's production server.
const DefaultURL = "https://api.cloud.conch.dev"

// URLEnv overrides the API's address: a development server, or a test's.
const URLEnv = "CONCH_CLOUD_URL"

// APIURL is the control plane this conch signs in to: CONCH_CLOUD_URL,
// else config.toml's [cloud] api, else production. A signed-in device
// keeps using the server it signed in to (cloud.json), whatever this
// says later.
func APIURL() string {
	if u := strings.TrimRight(os.Getenv(URLEnv), "/"); u != "" {
		return u
	}
	if cfg, err := config.Load(); err == nil {
		if u := strings.TrimRight(strings.TrimSpace(cfg.Cloud.API), "/"); u != "" {
			return u
		}
	}
	return DefaultURL
}

// DeviceAuth is POST /v1/auth/device's answer.
type DeviceAuth struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

// tokenResponse is POST /v1/auth/token's answer.
type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	DeviceID     string `json:"device_id"`
}

// Me is the signed-in account (GET /v1/me).
type Me struct {
	ID        string    `json:"id"`
	Handle    string    `json:"handle"`
	Email     string    `json:"email,omitempty"`
	Plan      string    `json:"plan"`
	Allowance Allowance `json:"allowance"`
}

// Allowance is this month's share of the plan.
type Allowance struct {
	RunningSeconds     int       `json:"running_seconds"`
	RunningSecondsUsed int       `json:"running_seconds_used"`
	StorageGB          int       `json:"storage_gb"`
	StorageGBUsed      float64   `json:"storage_gb_used"`
	MaxMachines        int       `json:"max_machines"`
	PeriodEnd          time.Time `json:"period_end,omitzero"`
}

// Device is a device signed in to the account.
type Device struct {
	ID         string    `json:"id"`
	Name       string    `json:"name"`
	Client     string    `json:"client"`
	CreatedAt  time.Time `json:"created_at"`
	LastSeenAt time.Time `json:"last_seen_at"`
	Current    bool      `json:"current"`
}

// Provider is where machines can run (1.2.0).
type Provider struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	Owner       string   `json:"owner"`
	Status      string   `json:"status"`
	Regions     []string `json:"regions"`
	Sizes       []string `json:"sizes"`
	MemorySleep bool     `json:"memory_sleep"`
	Default     bool     `json:"default"`
	Machines    int      `json:"machines,omitempty"`
}

// Size is a machine size the account may create.
type Size struct {
	ID       string   `json:"id"`
	Name     string   `json:"name"`
	VCPU     int      `json:"vcpu"`
	MemoryGB int      `json:"memory_gb"`
	DiskGB   int      `json:"disk_gb"`
	Plans    []string `json:"plans"`
}

// Region is where a machine can be made.
type Region struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Sizes is GET /v1/sizes.
type Sizes struct {
	Sizes   []Size   `json:"sizes"`
	Regions []Region `json:"regions"`
}

// Machine states (openapi.yaml MachineState). A state this list lacks is
// passed through as it is.
const (
	StateCreating   = "creating"
	StateRunning    = "running"
	StateSleeping   = "sleeping"
	StateAsleep     = "asleep"
	StateWaking     = "waking"
	StateArchived   = "archived"
	StateError      = "error"
	StateDestroying = "destroying"
)

// SleepPolicy is a machine's sleep settings.
type SleepPolicy struct {
	IdleSeconds          int `json:"idle_seconds,omitempty"`
	UnattendedMaxSeconds int `json:"unattended_max_seconds,omitempty"`
	ArchiveAfterSeconds  int `json:"archive_after_seconds,omitempty"`
}

// Machine is a cloud machine.
type Machine struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Size         string           `json:"size"`
	Region       string           `json:"region"`
	State        string           `json:"state"`
	StateDetail  string           `json:"state_detail,omitempty"`
	StateSince   time.Time        `json:"state_since,omitzero"`
	Sleep        SleepPolicy      `json:"sleep"`
	MemorySleep  bool             `json:"memory_sleep,omitempty"`
	ConchVersion string           `json:"conch_version,omitempty"`
	ServerKey    string           `json:"server_key,omitempty"`
	CreatedAt    time.Time        `json:"created_at"`
	Activity     *Activity        `json:"activity,omitempty"`
	Provider     *MachineProvider `json:"provider,omitempty"`
}

// Activity is the VM's last activity report (1.1.0).
type Activity struct {
	Agents          []AgentActivity `json:"agents"`
	ClientsAttached int             `json:"clients_attached"`
	ProcessesBusy   bool            `json:"processes_busy,omitempty"`
	ReportedAt      time.Time       `json:"reported_at"`
}

// AgentActivity is one agent in a machine's activity.
type AgentActivity struct {
	Agent   string `json:"agent"`
	State   string `json:"state"`
	Project string `json:"project,omitempty"`
}

// MachineProvider is where a machine runs (1.2.0).
type MachineProvider struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	Owner string `json:"owner"`
}

// MachineCreate is POST /v1/machines's body.
type MachineCreate struct {
	Name     string       `json:"name"`
	Size     string       `json:"size"`
	Region   string       `json:"region,omitempty"`
	Snapshot string       `json:"snapshot,omitempty"`
	Sleep    *SleepPolicy `json:"sleep,omitempty"`
	Provider string       `json:"provider,omitempty"`
}

// MachineUpdate is PATCH /v1/machines/{id}'s body.
type MachineUpdate struct {
	Name  string       `json:"name,omitempty"`
	Size  string       `json:"size,omitempty"`
	Sleep *SleepPolicy `json:"sleep,omitempty"`
}

// ConnectInfo is what a client needs to open a tunnel to a machine.
type ConnectInfo struct {
	RelayURL  string    `json:"relay_url"`
	Room      string    `json:"room"`
	Ticket    string    `json:"ticket"`
	ServerKey string    `json:"server_key"`
	ExpiresAt time.Time `json:"expires_at"`
	State     string    `json:"state"`
}

// Event is one of GET /v1/events's server-sent events.
type Event struct {
	ID        string         `json:"-"`
	Type      string         `json:"type"`
	At        time.Time      `json:"at"`
	MachineID string         `json:"machine_id,omitempty"`
	Data      map[string]any `json:"data,omitempty"`
}

// Event types a client acts on; others are ignored.
const (
	EventMachineState   = "machine.state"
	EventMachineUpdated = "machine.updated"
	EventMachineDeleted = "machine.deleted"
	EventDeviceRevoked  = "device.revoked"
)
