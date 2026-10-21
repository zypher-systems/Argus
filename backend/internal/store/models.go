package store

import (
	"time"
)

type MonitorConfig struct {
	Method             string            `json:"method,omitempty"`
	ExpectStatus       int               `json:"expect_status,omitempty"`
	Keyword            string            `json:"keyword,omitempty"`
	InsecureSkipVerify bool              `json:"insecure_skip_verify,omitempty"`
	TimeoutSeconds     int               `json:"timeout_seconds,omitempty"`
	Headers            map[string]string `json:"headers,omitempty"`
	Body               string            `json:"body,omitempty"`
	JSONPath           string            `json:"json_path,omitempty"`
	JSONExpect         string            `json:"json_expect,omitempty"`
	Auth               string            `json:"auth,omitempty"`
	AuthUser           string            `json:"auth_user,omitempty"`
	AuthPassword       string            `json:"auth_password,omitempty"`
	AuthToken          string            `json:"auth_token,omitempty"`
	Record             string            `json:"record,omitempty"`
	ExpectIP           string            `json:"expect_ip,omitempty"`
	// ExpectValue matches a non-address DNS answer: an exact match for CNAME,
	// MX and NS, a substring for TXT.
	ExpectValue   string `json:"expect_value,omitempty"`
	KeywordInvert bool   `json:"keyword_invert,omitempty"`
	ProxyURL      string `json:"proxy_url,omitempty"`
	PushToken     string `json:"push_token,omitempty"`
	// Schedule and Timezone make a push monitor expect its ping at cron
	// times (a nightly backup) instead of every interval.
	Schedule string `json:"schedule,omitempty"`
	Timezone string `json:"timezone,omitempty"`
	// WarnDays overrides how early an expiry warning fires (cert, domain).
	WarnDays int `json:"warn_days,omitempty"`
	// TLSMode is how a mail monitor secures the connection: none, starttls or tls.
	TLSMode string `json:"tls_mode,omitempty"`
	// Database and Query drive database monitors; Query's scalar result is
	// compared against ExpectValue when one is set.
	Database     string `json:"database,omitempty"`
	Query        string `json:"query,omitempty"`
	GraceSeconds int    `json:"grace_seconds,omitempty"`
	// Steps are a transaction monitor's requests, run in order.
	Steps []TransactionStep `json:"steps,omitempty"`
	// Secrets are named values steps refer to as {{secret.name}}. They are
	// encrypted at rest and never returned by the API, so bodies and headers
	// that use them can stay readable in the editor.
	Secrets map[string]string `json:"secrets,omitempty"`
}

// TransactionStep is one request in a transaction monitor. URL, header
// values and body may use {{name}} for a value an earlier step extracted and
// {{secret.name}} for a stored secret.
type TransactionStep struct {
	Name         string            `json:"name"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers,omitempty"`
	Body         string            `json:"body,omitempty"`
	ExpectStatus int               `json:"expect_status,omitempty"`
	Keyword      string            `json:"keyword,omitempty"`
	Extract      []StepExtract     `json:"extract,omitempty"`
}

// StepExtract captures a value from a step's response for later steps.
type StepExtract struct {
	Name string `json:"name"`
	// From is "json" (a dotted path into the body) or "header".
	From string `json:"from"`
	Path string `json:"path"`
}

// StepEvidence is how one step of a transaction went.
type StepEvidence struct {
	Name       string  `json:"name"`
	StatusCode int     `json:"status_code,omitempty"`
	LatencyMs  float64 `json:"latency_ms"`
	Error      string  `json:"error,omitempty"`
}

type Monitor struct {
	ID                 int64         `json:"id"`
	Name               string        `json:"name"`
	Type               string        `json:"type"`
	Target             string        `json:"target"`
	IntervalSeconds    int           `json:"interval_seconds"`
	Retries            int           `json:"retries"`
	EscalateMinutes    int           `json:"escalate_minutes"`
	EscalateRecipients string        `json:"escalate_recipients"`
	Enabled            bool          `json:"enabled"`
	Tags               string        `json:"tags"`
	LatencyThresholdMs int           `json:"latency_threshold_ms"`
	ChannelIDs         []int64       `json:"channel_ids"`
	ResendMinutes      int           `json:"resend_minutes"`
	ParentID           *int64        `json:"parent_id"`
	DependsOn          *int64        `json:"depends_on"`
	RealmIDs           []int64       `json:"realm_ids"`
	Config             MonitorConfig `json:"config"`
	CreatedAt          time.Time     `json:"created_at"`
	LastPushAt         *time.Time    `json:"-"`
	// LastPushDown is set when the newest heartbeat reported a failure.
	LastPushDown bool    `json:"-"`
	SLOTarget    float64 `json:"slo_target"`
	ProbeNodeIDs []int64 `json:"probe_node_ids"`
	// ConfirmLocations is how many locations must see the monitor down
	// before it is down. 1 means any location that confirms a failure.
	ConfirmLocations int `json:"confirm_locations"`
	// OrgID is the organization the monitor belongs to.
	OrgID int64 `json:"-"`
}

type CheckEvidence struct {
	Kind        string    `json:"kind"`
	At          time.Time `json:"at"`
	Success     bool      `json:"success"`
	Error       string    `json:"error,omitempty"`
	StatusCode  int       `json:"status_code,omitempty"`
	BodySnippet string    `json:"body_snippet,omitempty"`
	ContentType string    `json:"content_type,omitempty"`
	TLSError    string    `json:"tls_error,omitempty"`
	DNSAnswers  []string  `json:"dns_answers,omitempty"`
	RemoteAddr  string    `json:"remote_addr,omitempty"`
	Curl        string    `json:"curl,omitempty"`
	// Steps records each step a transaction ran, up to the one that failed.
	Steps []StepEvidence `json:"steps,omitempty"`
}

type MonitorState struct {
	Status           string     `json:"status"`
	LastLatencyMs    *float64   `json:"last_latency_ms"`
	LastCheckAt      *time.Time `json:"last_check_at"`
	LastChangeAt     *time.Time `json:"last_change_at"`
	ConsecutiveFails int        `json:"consecutive_failures"`
	DownSince        *time.Time `json:"down_since"`
	EscalatedAt      *time.Time `json:"escalated_at"`
	Uptime24h        *float64   `json:"uptime_24h"`
	TLSExpiresAt     *time.Time `json:"tls_expires_at"`
	// NextPingAt is when a scheduled heartbeat is next due.
	NextPingAt *time.Time `json:"next_ping_at,omitempty"`
	// ExpiresAt is a domain registration expiry, for domain monitors.
	ExpiresAt           *time.Time     `json:"expires_at,omitempty"`
	Slow                bool           `json:"slow"`
	SlowSince           *time.Time     `json:"slow_since"`
	Stale               bool           `json:"stale"`
	MutedUntil          *time.Time     `json:"muted_until,omitempty"`
	AckedAt             *time.Time     `json:"acked_at,omitempty"`
	AckedUntil          *time.Time     `json:"acked_until,omitempty"`
	LastFailureEvidence *CheckEvidence `json:"last_failure_evidence,omitempty"`
	LastFailureAt       *time.Time     `json:"last_failure_at,omitempty"`
}

type MonitorWithState struct {
	Monitor
	State   MonitorState    `json:"state"`
	Regions []ProbeNodeView `json:"regions,omitempty"`
}

type ProbeNode struct {
	ID        int64      `json:"id"`
	Name      string     `json:"name"`
	Region    string     `json:"region"`
	Token     string     `json:"token,omitempty"`
	LastSeen  *time.Time `json:"last_seen"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
}

type ProbeNodeView struct {
	NodeID        int64      `json:"node_id"`
	Name          string     `json:"name"`
	Region        string     `json:"region"`
	Status        string     `json:"status"`
	LastLatencyMs *float64   `json:"last_latency_ms"`
	LastCheckAt   *time.Time `json:"last_check_at"`
	LastError     string     `json:"last_error"`
	// ConsecutiveFailures counts this location's failures in a row.
	ConsecutiveFailures int `json:"consecutive_failures"`
}

type AuditEntry struct {
	ID         int64     `json:"id"`
	TS         time.Time `json:"ts"`
	UserID     *int64    `json:"user_id"`
	Email      string    `json:"email"`
	Action     string    `json:"action"`
	Resource   string    `json:"resource"`
	ResourceID *int64    `json:"resource_id"`
	Detail     any       `json:"detail"`
	IP         string    `json:"ip"`
}

type SLARow struct {
	MonitorID    int64    `json:"monitor_id"`
	Name         string   `json:"name"`
	SLOTarget    float64  `json:"slo_target"`
	UptimePct    *float64 `json:"uptime_pct"`
	TotalChecks  int      `json:"total_checks"`
	FailedChecks int      `json:"failed_checks"`
	WithinSLO    bool     `json:"within_slo"`
}

type InstanceInfo struct {
	InstanceID string `json:"instance_id"`
	Version    string `json:"version"`
}

type PingResult struct {
	MonitorID    int64
	TS           time.Time
	LatencyMs    *float64
	Success      bool
	TLSExpiresAt *time.Time
	// ExpiresAt is a domain registration expiry, kept apart from TLS so
	// neither column has to mean two things.
	ExpiresAt *time.Time
	Evidence  *CheckEvidence
}

type Event struct {
	ID        int64     `json:"id"`
	MonitorID int64     `json:"monitor_id"`
	TS        time.Time `json:"ts"`
	Type      string    `json:"type"`
	Detail    string    `json:"detail"`
}

type Alert struct {
	ID         int64     `json:"id"`
	MonitorID  int64     `json:"monitor_id"`
	Type       string    `json:"type"`
	Status     string    `json:"status"`
	Recipients string    `json:"recipients"`
	Error      string    `json:"error"`
	CreatedAt  time.Time `json:"created_at"`
}

type MaintenanceWindow struct {
	ID        int64     `json:"id"`
	MonitorID *int64    `json:"monitor_id"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Note      string    `json:"note"`
	// Public announces the window on any status page that lists the monitor.
	Public    bool      `json:"public"`
	CreatedAt time.Time `json:"created_at"`
}

// PublicMaintenance is an announced window as a status page shows it.
type PublicMaintenance struct {
	StartsAt time.Time `json:"starts_at"`
	EndsAt   time.Time `json:"ends_at"`
	Note     string    `json:"note"`
	Monitor  string    `json:"monitor,omitempty"`
	Active   bool      `json:"active"`
}

type IncidentUpdate struct {
	ID         int64     `json:"id"`
	IncidentID int64     `json:"incident_id"`
	Status     string    `json:"status"`
	Message    string    `json:"message"`
	CreatedAt  time.Time `json:"created_at"`
}

type Incident struct {
	ID          int64            `json:"id"`
	MonitorID   *int64           `json:"monitor_id"`
	MonitorName string           `json:"monitor_name,omitempty"`
	Status      string           `json:"status"`
	Title       string           `json:"title"`
	Impact      string           `json:"impact"`
	Public      bool             `json:"public"`
	AutoOpened  bool             `json:"auto_opened"`
	OpenedAt    time.Time        `json:"opened_at"`
	ResolvedAt  *time.Time       `json:"resolved_at"`
	Updates     []IncidentUpdate `json:"updates,omitempty"`
}

type StatusSubscriber struct {
	ID           int64     `json:"id"`
	StatusPageID int64     `json:"status_page_id"`
	Kind         string    `json:"kind"`
	Dest         string    `json:"dest"`
	Confirmed    bool      `json:"confirmed"`
	CreatedAt    time.Time `json:"created_at"`
	Token        string    `json:"token,omitempty"`
	// UnsubscribeToken goes in every email to this subscriber, never in the API.
	UnsubscribeToken string `json:"-"`
}

type RawPoint struct {
	TS        time.Time `json:"ts"`
	LatencyMs *float64  `json:"latency_ms"`
	Success   bool      `json:"success"`
}

type RollupPoint struct {
	TS         time.Time `json:"ts"`
	Total      int       `json:"total"`
	Failures   int       `json:"failures"`
	AvgLatency *float64  `json:"avg_latency"`
	MaxLatency *float64  `json:"max_latency"`
}

type LatencyPercentiles struct {
	P50     *float64 `json:"p50"`
	P95     *float64 `json:"p95"`
	P99     *float64 `json:"p99"`
	Samples int      `json:"samples"`
}

type APIToken struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	Prefix     string     `json:"token_prefix"`
	Token      string     `json:"token,omitempty"`
	UserID     int64      `json:"user_id,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	LastUsedAt *time.Time `json:"last_used_at"`
}
