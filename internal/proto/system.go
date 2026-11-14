package proto

import "time"

// CapSystemStats is system.stats: the whole machine the server runs on —
// its CPU, each core, memory and where it goes, load, disk, and the
// heaviest processes, each one conch started named by its pane. The
// resource monitor shows what conch uses; this is for the window that
// shows everything else beside it, on any machine conch reaches.
const CapSystemStats = "system.stats.v1"

// MethodSystemStats answers a SystemStats (system.stats.v1).
const MethodSystemStats = "system.stats"

// SystemStatsParams asks for the machine. Since is the newest history
// point the client already holds: only newer ones come back, so a window
// asking every two seconds is not sent the last hour each time. Summary
// leaves out the processes and the history — a machine's line in a list.
type SystemStatsParams struct {
	Since   time.Time `json:"since,omitzero"`
	Summary bool      `json:"summary,omitempty"`
}

// SystemStats is the machine at one moment. CPU is nil until there is a
// second sample to measure it by; anything the machine would not say is
// left out rather than given as zero.
type SystemStats struct {
	At        time.Time       `json:"at"`
	Host      SystemHost      `json:"host"`
	CPU       *SystemCPU      `json:"cpu,omitempty"`
	Memory    *SystemMemory   `json:"memory,omitempty"`
	Load      []float64       `json:"load,omitempty"` // 1, 5 and 15 minutes
	Disk      *SystemDisk     `json:"disk,omitempty"`
	Procs     int             `json:"procs,omitempty"` // every process, not only those listed
	Processes []SystemProcess `json:"processes,omitempty"`
	History   []SystemPoint   `json:"history,omitempty"`
}

// SystemHost is what the machine is.
type SystemHost struct {
	Name   string `json:"name,omitempty"`
	OS     string `json:"os,omitempty"` // "macOS 26.1", "Linux 6.8.0"
	Arch   string `json:"arch,omitempty"`
	Model  string `json:"model,omitempty"` // the processor, as the machine names it
	Cores  int    `json:"cores"`
	PCores int    `json:"p_cores,omitempty"` // Apple silicon's performance cores…
	ECores int    `json:"e_cores,omitempty"` // …and efficiency cores
	// Uptime is how long since the machine started, in seconds.
	Uptime int64 `json:"uptime,omitempty"`
}

// SystemCPU is the share of the whole machine in use since the last
// sample, 0–100. User and System are set only where the machine says how
// it splits (Split); Cores only where it says per core.
type SystemCPU struct {
	Total  float64   `json:"total"`
	User   float64   `json:"user,omitempty"`
	System float64   `json:"system,omitempty"`
	Split  bool      `json:"split,omitempty"`
	Cores  []float64 `json:"cores,omitempty"`
}

// SystemMemory is in bytes. Used is what programs hold (Apps, Wired and
// Compressed where the machine tells them apart); Cached is file data the
// system can let go of; Pressure is "normal", "warn" or "critical" when
// the machine says.
type SystemMemory struct {
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Apps       uint64 `json:"apps,omitempty"`
	Wired      uint64 `json:"wired,omitempty"`
	Compressed uint64 `json:"compressed,omitempty"`
	Cached     uint64 `json:"cached,omitempty"`
	Free       uint64 `json:"free"`
	SwapTotal  uint64 `json:"swap_total,omitempty"`
	SwapUsed   uint64 `json:"swap_used,omitempty"`
	Pressure   string `json:"pressure,omitempty"`
}

// Memory pressure, as SystemMemory.Pressure says it.
const (
	PressureNormal   = "normal"
	PressureWarn     = "warn"
	PressureCritical = "critical"
)

// SystemDisk is the filesystem the home directory is on.
type SystemDisk struct {
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Free  uint64 `json:"free"`
}

// SystemProcess is one process. CPU is the share of one core since the
// last sample (200 is two cores), absent on the first. Pane and the
// fields after it are set for a process conch started — its pane's id and
// name, the pane's project and branch, the agent in it — and Conch is
// "server" for the server itself and what it runs outside any pane.
type SystemProcess struct {
	PID     int       `json:"pid"`
	PPID    int       `json:"ppid"`
	Name    string    `json:"name"`
	Command string    `json:"command,omitempty"`
	User    string    `json:"user,omitempty"`
	CPU     *float64  `json:"cpu,omitempty"`
	RSS     uint64    `json:"rss"`
	Threads int       `json:"threads,omitempty"`
	Started time.Time `json:"started,omitzero"`

	Pane     string `json:"pane,omitempty"`
	PaneName string `json:"pane_name,omitempty"`
	Project  string `json:"project,omitempty"`
	Branch   string `json:"branch,omitempty"`
	Agent    string `json:"agent,omitempty"`
	Conch    string `json:"conch,omitempty"`
}

// SystemPoint is one moment of the history: the machine's CPU (0–100)
// and memory in use, in bytes. The server keeps an hour of them while
// anybody is asking, and a little longer.
type SystemPoint struct {
	At         time.Time `json:"at"`
	CPU        float64   `json:"cpu"`
	User       float64   `json:"user,omitempty"`
	System     float64   `json:"system,omitempty"`
	MemUsed    uint64    `json:"mem_used"`
	Compressed uint64    `json:"compressed,omitempty"`
}
