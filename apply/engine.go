package apply

import "context"

const (
	applyName     = "olg-renderer-vyos/apply"
	applyVersion  = "dev"
	applyTarget   = "vyos"
	applyStrategy = "cloud-authoritative-reset-with-protected-roots"
)

// Engine validates rendered commands, prepares reset plans, and applies them through an Executor.
type Engine struct {
	executor        Executor
	saveAfterCommit bool
	resetPolicy     ResetPolicy
}

// New constructs an apply engine.
func New(opts ...Option) (*Engine, error) {
	policy, err := validateResetPolicy(DefaultResetPolicy())
	if err != nil {
		return nil, err
	}

	e := &Engine{
		executor:    newDefaultExecutor(),
		resetPolicy: policy,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(e); err != nil {
			return nil, wrapOptionError(err)
		}
	}
	e.resetPolicy.ResetRoots = cloneStrings(e.resetPolicy.ResetRoots)
	return e, nil
}

// GetInfo returns apply package metadata.
func GetInfo() Info {
	return Info{
		Name:              applyName,
		Version:           applyVersion,
		Target:            applyTarget,
		ApplyStrategy:     applyStrategy,
		DefaultResetRoots: defaultResetRootList(),
		SaveDefault:       false,
	}
}

// Info returns apply package metadata from an engine instance.
func (e *Engine) Info() Info {
	return GetInfo()
}

// Prepare validates input and returns a deterministic non-executing Plan.
func (e *Engine) Prepare(ctx context.Context, input Input) (Plan, error) {
	plan, err := e.prepare(ctx, input)
	if err != nil {
		return Plan{}, err
	}
	return clonePlan(plan), nil
}

// Apply validates, plans, executes through the configured Executor, and commits.
func (e *Engine) Apply(ctx context.Context, input Input) (Result, error) {
	plan, err := e.prepare(ctx, input)
	if err != nil {
		return Result{}, err
	}

	if e.executor == nil {
		return resultFromPlan(plan), newError(CodeExecutorFailed, "executor is not configured", nil)
	}

	execResult, err := e.executor.Execute(ctx, clonePlan(plan))
	result := resultFromExecution(plan, execResult)
	if err != nil {
		code := CodeOf(err)
		if code != CodeSaveFailed {
			result.Applied = false
		}
		if code != "" {
			return result, err
		}
		return result, newError(CodeExecutorFailed, "executor failed", err)
	}
	return result, nil
}

func resultFromPlan(plan Plan) Result {
	return Result{
		Target:         plan.Target,
		ConfigUUID:     plan.ConfigUUID,
		DeleteCommands: cloneStrings(plan.DeleteCommands),
		SetCommands:    cloneStrings(plan.SetCommands),
	}
}

func resultFromExecution(plan Plan, execResult ExecutionResult) Result {
	result := resultFromPlan(plan)
	result.Applied = execResult.Applied
	result.Saved = plan.Save && execResult.Saved
	result.CommitOutput = execResult.CommitOutput
	result.SaveOutput = execResult.SaveOutput
	result.DiscardOutput = execResult.DiscardOutput
	return result
}
