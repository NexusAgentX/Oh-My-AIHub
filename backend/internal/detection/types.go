// Package detection executes bounded, metadata-only upstream capability jobs.
// Detection never uses the gateway billing/health/usage stores.
package detection

import (
	"context"
	"errors"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

var (
	ErrInvalid  = errors.New("invalid detection request")
	ErrNotFound = errors.New("detection task not found")
	ErrBusy     = errors.New("detection queue saturated")
	ErrConflict = errors.New("detection inputs changed")
	ErrClosed   = errors.New("detection service stopped")
)

type Actor struct {
	ID    string
	Admin bool
}
type Target struct {
	GroupID     string                 `json:"group_id"`
	Binding     market.EvidenceBinding `json:"binding"`
	ModelType   string                 `json:"model_type"`
	Model       string                 `json:"model"`
	ServiceTier string                 `json:"service_tier"`
}
type Parameters struct {
	Context   gatewaycontract.SearchSettings `json:"context"`
	MaxOutput gatewaycontract.SearchSettings `json:"max_output"`
}

// Credentials and request bodies are intentionally absent from all persisted
// types. Resolved configuration exists only in a worker's private memory.
type Resolved struct {
	Target    Target
	Addresses []string
	UserAgent string
	Headers   market.HeaderRules
}
type Resolver interface {
	Resolve(context.Context, Actor, Target) (Resolved, error)
}

type Usage struct {
	HTTP         int64 `json:"http"`
	Generate     int64 `json:"generate"`
	Count        int64 `json:"count"`
	Query        int64 `json:"query"`
	Cleanup      int64 `json:"cleanup"`
	InputTokens  int64 `json:"input_tokens"`
	OutputTokens int64 `json:"output_tokens"`
	Images       int64 `json:"images"`
	Tools        int64 `json:"tools"`
}

func (u *Usage) Add(v Usage) {
	u.HTTP += v.HTTP
	u.Generate += v.Generate
	u.Count += v.Count
	u.Query += v.Query
	u.Cleanup += v.Cleanup
	u.InputTokens += v.InputTokens
	u.OutputTokens += v.OutputTokens
	u.Images += v.Images
	u.Tools += v.Tools
}

type Attempt struct {
	ID               string    `json:"id"`
	EndpointID       string    `json:"endpoint_id"`
	Method           string    `json:"method"`
	Path             string    `json:"path"` // the registered template, never arbitrary URLs/IDs
	Status           int       `json:"status"`
	Reason           string    `json:"reason"` // fixed enum; never upstream error text
	Category         string    `json:"category"`
	DurationMS       int64     `json:"duration_ms"`
	Sent             bool      `json:"sent"`
	ResponseReceived bool      `json:"response_received"`
	At               time.Time `json:"at"`
}
type Assertion struct {
	ID         string   `json:"id"`
	State      string   `json:"state"`
	Reason     string   `json:"reason"`
	AttemptIDs []string `json:"attempt_ids"`
}
type Result struct {
	ID         string      `json:"id"`
	ScenarioID string      `json:"scenario_id"`
	State      string      `json:"state"`
	Reason     string      `json:"reason"`
	Layer      string      `json:"layer"`
	Source     string      `json:"source"` // real_upstream or synthetic; fixtures never authorize creation
	Assertions []Assertion `json:"assertions"`
	Attempts   []Attempt   `json:"attempts"`
	Usage      Usage       `json:"usage"`
	StartedAt  time.Time   `json:"started_at"`
	FinishedAt time.Time   `json:"finished_at"`
}
type Planned struct {
	ID            string   `json:"id"`
	SuiteID       string   `json:"suite_id"`
	Requirements  []string `json:"requirements"`
	Assertions    []string `json:"assertions"`
	Conditional   []string `json:"conditional"`
	Dependencies  []string `json:"dependencies"`
	Aggregate     bool     `json:"aggregate"`
	Implemented   bool     `json:"implemented"`
	CodeVersion   string   `json:"code_version"`
	Layer         string   `json:"layer"`
	EstimatedHTTP *int64   `json:"estimated_http"`
}
type Plan struct {
	EngineVersion   string    `json:"engine_version"`
	ManifestVersion string    `json:"manifest_version"`
	RegistryVersion string    `json:"registry_version"`
	Items           []Planned `json:"items"`
}
type Job struct {
	ID         string     `json:"id"`
	OwnerID    string     `json:"owner_id"`
	Target     Target     `json:"target"`
	Parameters Parameters `json:"parameters"`
	Plan       Plan       `json:"plan"`
	State      string     `json:"state"`
	Complete   bool       `json:"complete"`
	Results    []Result   `json:"results"`
	Usage      Usage      `json:"usage"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}
type Artifact struct {
	ID                string `json:"id"`
	Kind              string `json:"kind"`
	NativeID          string `json:"native_id"`
	DeleteEndpoint    string `json:"delete_endpoint"`
	DeletePath        string `json:"delete_path"`
	ProducerAttemptID string `json:"producer_attempt_id"`
	Removed           bool   `json:"removed"`
	Reason            string `json:"reason"`
}
type Store interface {
	Create(context.Context, Job) error
	Get(context.Context, string) (Job, error)
	List(context.Context, string, bool, int) ([]Job, error)
	Save(context.Context, Job) error
	Interrupt(context.Context) error
	RecordAttempt(context.Context, string, Attempt) error
	Attempts(context.Context, string, []string) ([]Attempt, error)
	AddArtifact(context.Context, string, Artifact) error
	Artifacts(context.Context, string) ([]Artifact, error)
	SaveArtifact(context.Context, string, Artifact) error
	RecordResult(context.Context, string, Result) error
}

type Runner func(context.Context, *Session) Result
type Definition struct {
	ID            string
	SuiteID       string
	Requirements  []string
	Dependencies  []string
	Conditional   []string
	CodeVersion   string
	Layer         string
	EstimatedHTTP *int64
	Run           Runner
}
