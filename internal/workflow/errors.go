package workflow

import "fmt"

// ErrDeterminismBan is raised (as a JS exception, via panic of its message) when
// foreign workflow JS reaches for a banned non-deterministic API. The message is
// agent-actionable: it names the API and the deterministic substitute the author
// must use instead.
type ErrDeterminismBan struct {
	API        string
	Substitute string
}

func (e *ErrDeterminismBan) Error() string {
	return fmt.Sprintf("%s is banned in workflows (non-deterministic). %s", e.API, e.Substitute)
}

// ErrAgentCap is raised when a workflow exceeds the 1000-agent lifetime cap. It
// surfaces as a rejected top-level promise so the run terminates with a clear
// status rather than spawning unbounded paid work.
type ErrAgentCap struct {
	Cap int
}

func (e *ErrAgentCap) Error() string {
	return fmt.Sprintf("workflow exceeded the %d-agent lifetime cap", e.Cap)
}

// ErrTooManyItems is raised when a single parallel()/pipeline() call is handed
// more than MaxItemsPerCall items/thunks.
type ErrTooManyItems struct {
	Construct string
	Count     int
	Max       int
}

func (e *ErrTooManyItems) Error() string {
	return fmt.Sprintf("%s received %d items, exceeding the per-call cap of %d", e.Construct, e.Count, e.Max)
}

// ErrInterrupted is the value handed to vm.Interrupt when the watchdog (or ctx
// cancellation) kills a CPU-bound/infinite script segment. goja wraps it in an
// *InterruptedError; the engine surfaces it as RunStatus interrupted.
type ErrInterrupted struct {
	Reason string
}

func (e *ErrInterrupted) Error() string {
	return e.Reason
}

// ErrWorkflowNotImpl is raised by the workflow() host stub: nested workflows are
// out of scope for E1.
type ErrWorkflowNotImpl struct{}

func (e *ErrWorkflowNotImpl) Error() string {
	return "workflow() nesting is not implemented in E1"
}

// ErrMeta describes a rejected meta declaration (non-literal, computed, or
// not the first statement).
type ErrMeta struct {
	Reason string
}

func (e *ErrMeta) Error() string {
	return fmt.Sprintf("invalid workflow meta: %s", e.Reason)
}
