package workflow

import (
	"context"
	"encoding/json"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// RunStatus is the terminal disposition of a Run/Resume.
type RunStatus string

const (
	StatusCompleted   RunStatus = "completed"
	StatusErrored     RunStatus = "errored"
	StatusInterrupted RunStatus = "interrupted"
)

const (
	defaultAgentLifetimeCap = 1000
	defaultMaxItemsPerCall  = 4096
	defaultWatchdogTimeout  = 30 * time.Second
)

// Config configures an Engine. All fields are optional; zero values use defaults.
type Config struct {
	// Stub is the fake agent() implementation. nil -> DefaultStub.
	Stub AgentStub
	// Journal seeds the run. nil -> a fresh MemJournal (a Run from scratch).
	// For Resume, pass the prior run's journal.
	Journal Journal
	// AgentLifetimeCap caps live (cache-miss) agent() calls per run. 0 -> 1000.
	AgentLifetimeCap int
	// MaxItemsPerCall caps items/thunks per parallel()/pipeline() call. 0 -> 4096.
	MaxItemsPerCall int
	// ConcurrencyCap caps simultaneous live agent dispatch. 0 -> min(16, cores-2).
	ConcurrencyCap int
	// WatchdogTimeout is the wall-clock budget for any single uninterrupted JS run
	// segment. 0 -> 30s.
	WatchdogTimeout time.Duration
}

// Engine runs workflow scripts. It is safe to reuse across Run/Resume calls; each
// call gets its own goja runtime and goroutine.
type Engine struct {
	cfg Config
}

// New constructs an Engine.
func New(cfg Config) *Engine {
	if cfg.AgentLifetimeCap == 0 {
		cfg.AgentLifetimeCap = defaultAgentLifetimeCap
	}
	if cfg.MaxItemsPerCall == 0 {
		cfg.MaxItemsPerCall = defaultMaxItemsPerCall
	}
	if cfg.WatchdogTimeout == 0 {
		cfg.WatchdogTimeout = defaultWatchdogTimeout
	}
	if cfg.ConcurrencyCap == 0 {
		cfg.ConcurrencyCap = defaultConcurrency()
	}
	if cfg.Stub == nil {
		cfg.Stub = DefaultStub{}
	}
	return &Engine{cfg: cfg}
}

func defaultConcurrency() int {
	c := runtime.NumCPU() - 2
	if c < 1 {
		c = 1
	}
	if c > 16 {
		c = 16
	}
	return c
}

// RunResult is the outcome of a Run/Resume, including the test seams.
type RunResult struct {
	Value       any       // exported top-level result (or nil)
	Meta        *Meta     // parsed meta, if declared
	Status      RunStatus // completed | errored | interrupted
	Err         error     // set on errored/interrupted
	CachedCalls int       // # agent() served from the journal
	LiveCalls   int       // # agent() executed live
	Journal     Journal   // final journal (for chaining Resume)
}

// Run compiles and executes script with args from scratch. Unless cfg.Journal is
// preloaded, the journal starts empty so every agent() runs live.
func (e *Engine) Run(ctx context.Context, script string, args any) (RunResult, error) {
	return e.execute(ctx, script, args)
}

// Resume re-runs the SAME script+args, replaying the journaled prefix and running
// the first divergent call (and everything structurally after it) live. The prior
// run's journal must be set in cfg.Journal. Run and Resume share execute(); the
// only difference is whether the journal is empty (Run) or preloaded (Resume).
func (e *Engine) Resume(ctx context.Context, script string, args any) (RunResult, error) {
	return e.execute(ctx, script, args)
}

func (e *Engine) execute(ctx context.Context, script string, args any) (RunResult, error) {
	jour := e.cfg.Journal
	if jour == nil {
		jour = NewMemJournal()
	}

	// Parse meta from the export-stripped source (static, before running).
	stripped := stripExport(script)
	meta, metaErr := parseMeta(stripped)
	if metaErr != nil {
		return RunResult{Status: StatusErrored, Err: metaErr, Journal: jour}, metaErr
	}

	// The whole run executes on ONE goroutine (the loop goroutine). We marshal the
	// result back to the caller via a channel.
	type outcome struct {
		res RunResult
		err error
	}
	outCh := make(chan outcome, 1)

	go func() {
		outCh <- e.runOnLoopGoroutine(ctx, stripped, args, meta, jour)
	}()

	out := <-outCh
	return out.res, out.err
}

// runOnLoopGoroutine owns the runtime for its entire lifetime. It builds the
// realm, installs host fns, arms the watchdog, runs the wrapped async script, and
// pumps the event loop until the top-level promise settles.
func (e *Engine) runOnLoopGoroutine(ctx context.Context, src string, args any, meta *Meta, jour Journal) (out struct {
	res RunResult
	err error
}) {
	vm := goja.New()

	if err := installDeterminismBans(vm); err != nil {
		out.res = RunResult{Status: StatusErrored, Err: err, Journal: jour, Meta: meta}
		out.err = err
		return
	}

	el := newEventLoop(vm)
	rs := &runState{
		vm:               vm,
		el:               el,
		stub:             e.cfg.Stub,
		jour:             jour,
		stack:            newPathStack(),
		agentLifetimeCap: e.cfg.AgentLifetimeCap,
		maxItemsPerCall:  e.cfg.MaxItemsPerCall,
		sem:              make(chan struct{}, e.cfg.ConcurrencyCap),
	}
	if err := installHostFns(rs, args); err != nil {
		out.res = RunResult{Status: StatusErrored, Err: err, Journal: jour, Meta: meta}
		out.err = err
		return
	}

	// Carry the structural path across every await/.then boundary so a post-await
	// agent() inside an async stage/slot callback reads its STRUCTURAL ordinal, not
	// a timing-dependent one. See pathContextTracker for the full invariant.
	vm.SetAsyncContextTracker(newPathContextTracker(rs.stack))

	// --- watchdog wiring ---
	// One mechanism (vm.Interrupt) for two triggers: the hang deadline and ctx
	// cancellation. We arm a deadline whenever the loop goroutine enters JS and
	// disarm when it leaves (blocks on el.jobs). A single background goroutine
	// watches an atomic deadline + a ctx-cancel channel.
	wd := newWatchdog(vm, e.cfg.WatchdogTimeout)
	el.onEnterJS = wd.arm
	el.onLeaveJS = wd.disarm
	stopWatch := wd.start(ctx)
	defer stopWatch()

	// --- run the wrapped async script ---
	wrapped := "(async function __wf__(){\n" + src + "\n})()"

	var topLevel *goja.Promise
	initPanic := el.safeRunJS(func() {
		v, err := vm.RunScript("workflow.js", wrapped)
		if err != nil {
			panic(err)
		}
		p, ok := v.Export().(*goja.Promise)
		if !ok {
			// A synchronous (non-async) script that returns a non-promise: wrap it.
			pp, resolve, _ := vm.NewPromise()
			_ = resolve(v)
			topLevel = pp
			return
		}
		topLevel = p
	})

	if initPanic != nil {
		out.res = e.mapPanic(initPanic, rs, jour, meta)
		out.err = out.res.Err
		return
	}

	state, result, pumpPanic := el.pump(topLevel)
	if pumpPanic != nil {
		out.res = e.mapPanic(pumpPanic, rs, jour, meta)
		out.err = out.res.Err
		return
	}

	switch state {
	case goja.PromiseStateFulfilled:
		out.res = RunResult{
			Value:       exportValue(result),
			Meta:        meta,
			Status:      StatusCompleted,
			CachedCalls: rs.cachedCalls,
			LiveCalls:   rs.liveCalls,
			Journal:     jour,
		}
	case goja.PromiseStateRejected:
		rerr := &scriptError{value: exportValue(result), text: stringify(result)}
		out.res = RunResult{
			Meta:        meta,
			Status:      StatusErrored,
			Err:         rerr,
			CachedCalls: rs.cachedCalls,
			LiveCalls:   rs.liveCalls,
			Journal:     jour,
		}
		out.err = rerr
	default:
		// Still pending after the pump returned (e.g. interrupted mid-await).
		ierr := &ErrInterrupted{Reason: "workflow did not settle"}
		out.res = RunResult{Meta: meta, Status: StatusInterrupted, Err: ierr, Journal: jour,
			CachedCalls: rs.cachedCalls, LiveCalls: rs.liveCalls}
		out.err = ierr
	}
	return
}

// mapPanic classifies a recovered panic from the loop into a RunResult. An
// *InterruptedError (watchdog/cancel) -> interrupted; a goja exception (e.g. the
// agent cap throw, determinism ban, or a script throw) -> errored.
func (e *Engine) mapPanic(p interface{}, rs *runState, jour Journal, meta *Meta) RunResult {
	base := RunResult{
		Meta:        meta,
		CachedCalls: rs.cachedCalls,
		LiveCalls:   rs.liveCalls,
		Journal:     jour,
	}
	if ie, ok := p.(*goja.InterruptedError); ok {
		reason := "workflow exceeded the watchdog timeout"
		if v := ie.Value(); v != nil {
			if e2, ok := v.(*ErrInterrupted); ok {
				reason = e2.Reason
			} else if s, ok := v.(string); ok {
				reason = s
			}
		}
		base.Status = StatusInterrupted
		base.Err = &ErrInterrupted{Reason: reason}
		return base
	}
	if gerr, ok := p.(error); ok {
		base.Status = StatusErrored
		base.Err = gerr
		return base
	}
	// A panic of a goja Value (host fn throw before any promise rejection path).
	base.Status = StatusErrored
	base.Err = &scriptError{text: stringifyAny(p)}
	return base
}

// exportValue exports a goja Value to a plain Go value (nil for null/undefined).
func exportValue(v goja.Value) any {
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return nil
	}
	return v.Export()
}

func stringify(v goja.Value) string {
	if v == nil {
		return ""
	}
	return v.String()
}

func stringifyAny(v interface{}) string {
	if gv, ok := v.(goja.Value); ok {
		return gv.String()
	}
	if err, ok := v.(error); ok {
		return err.Error()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// scriptError carries a JS-thrown rejection value back to the Go caller.
type scriptError struct {
	value any
	text  string
}

func (e *scriptError) Error() string {
	if e.text != "" {
		return e.text
	}
	return "workflow script error"
}

// --- watchdog ---

type watchdog struct {
	vm      *goja.Runtime
	timeout time.Duration

	deadline atomic.Int64 // unix-nano deadline; 0 = disarmed
	tripped  atomic.Bool
}

func newWatchdog(vm *goja.Runtime, timeout time.Duration) *watchdog {
	return &watchdog{vm: vm, timeout: timeout}
}

func (w *watchdog) arm() {
	w.deadline.Store(time.Now().Add(w.timeout).UnixNano())
}

func (w *watchdog) disarm() {
	w.deadline.Store(0)
}

// start launches the background watcher. It polls the armed deadline and fires
// vm.Interrupt (goroutine-safe) on a hang or on ctx cancellation. Returns a stop
// func to tear the watcher down.
func (w *watchdog) start(ctx context.Context) func() {
	stop := make(chan struct{})
	var once sync.Once
	// Poll fast enough to catch a tight loop well within the timeout, but not so
	// fast as to spin. min(timeout/10, 10ms), floor 1ms.
	tick := w.timeout / 10
	if tick > 10*time.Millisecond {
		tick = 10 * time.Millisecond
	}
	if tick < time.Millisecond {
		tick = time.Millisecond
	}
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ctx.Done():
				if w.tripped.CompareAndSwap(false, true) {
					w.vm.Interrupt(&ErrInterrupted{Reason: "workflow cancelled"})
				}
				return
			case <-t.C:
				dl := w.deadline.Load()
				if dl != 0 && time.Now().UnixNano() >= dl {
					if w.tripped.CompareAndSwap(false, true) {
						w.vm.Interrupt(&ErrInterrupted{Reason: "workflow exceeded the watchdog timeout"})
					}
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
