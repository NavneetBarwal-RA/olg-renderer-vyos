package apply

import "context"

// Input is the canonical apply request payload.
type Input struct {
	Target          string
	ConfigUUID      string
	DesiredCommands string
}

// Plan describes validated commands and reset operations without executing them.
type Plan struct {
	Target         string
	ConfigUUID     string
	DeleteCommands []string
	SetCommands    []string
	Commit         bool
	Save           bool
}

// Result describes the outcome of an Apply execution.
type Result struct {
	Target         string
	ConfigUUID     string
	Applied        bool
	Saved          bool
	DeleteCommands []string
	SetCommands    []string
	DeleteOutput   string
	SetOutput      string
	CommitOutput   string
	SaveOutput     string
	DiscardOutput  string
}

// ResetPolicy lists cloud-controlled VyOS roots that may be reset before apply.
type ResetPolicy struct {
	ResetRoots []string
}

// ExecutionResult is returned by an Executor after applying a Plan.
type ExecutionResult struct {
	Applied       bool
	Saved         bool
	DeleteOutput  string
	SetOutput     string
	CommitOutput  string
	SaveOutput    string
	DiscardOutput string
}

// Executor applies a validated Plan through a controlled target-specific mechanism.
type Executor interface {
	Execute(ctx context.Context, plan Plan) (ExecutionResult, error)
}

// Info describes apply package capabilities and defaults.
type Info struct {
	Name              string
	Version           string
	Target            string
	ApplyStrategy     string
	DefaultResetRoots []string
	SaveDefault       bool
}
