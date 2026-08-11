package daemon

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/config"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// The app runtime's daemon half: one supervised Bun sidecar that runs the
// handlers of every installed app.
//
// One process, not one per app. Isolation between apps here is failure
// attribution, not an OS boundary — the daemon knows which app's handler was
// running when something threw, and charges it to that app. What it must never
// do is charge an app for the process dying: a sidecar-wide crash has no culprit,
// and blaming whichever app happened to be mid-dispatch would auto-disable
// innocent apps every time the runtime was restarted. Every failure below is
// therefore classified before it is recorded (see appFailureClass).
//
// Supervision — backoff, generation fencing, the stability window, the
// disconnect grace and the give-up tripwire — is internal/supervise's, the same
// machinery the plugin runtime uses. This file is only how a runtime binary
// becomes a process, and what happens when there is no process to dispatch to.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

const (
	// appRuntimeChildName is the supervised child's name. It is also the
	// reserved app name (internal/apps), because `attn app logs runtime` and
	// `attn app runtime status` both have to mean one thing.
	appRuntimeChildName = "runtime"

	// appRuntimeBinaryName is the compiled sidecar. The name lives in
	// internal/apps because the build, this daemon and the hub's remote install
	// all have to agree on it.
	appRuntimeBinaryName = apps.RuntimeHostBinaryName

	// appRuntimeAPIVersion is the host contract this daemon speaks. A host
	// presenting anything else is refused at hello rather than half-served: the
	// case this guards is an old binary inside a stale app bundle meeting a new
	// daemon, where every symptom of the skew would otherwise appear later and
	// somewhere else.
	appRuntimeAPIVersion = 1
)

// appRuntimeHostOverride lets a test — and a developer running a checkout's
// daemon against a stage dir — point at a specific host binary.
const appRuntimeHostOverride = "ATTN_APP_RUNTIME_HOST"

// resolveAppRuntimeHost finds the sidecar binary.
//
// Two locations, in order, and no PATH search: the runtime is a compiled
// artifact attn ships, not a tool the user installs, and resolving it from PATH
// is how a daemon launched by the macOS app (whose PATH is minimal) would find a
// different one — or a stranger's.
func resolveAppRuntimeHost() (string, error) {
	if override := strings.TrimSpace(os.Getenv(appRuntimeHostOverride)); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("%s points at %s, which is not there (%v)", appRuntimeHostOverride, override, err)
		}
		return override, nil
	}
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("locating this daemon's own executable to find %s beside it: %w", appRuntimeBinaryName, err)
	}
	executable, err = filepath.EvalSymlinks(executable)
	if err != nil {
		return "", fmt.Errorf("resolving this daemon's own executable to find %s beside it: %w", appRuntimeBinaryName, err)
	}

	candidates := appRuntimeHostCandidates(executable, config.Profile())
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate, nil
		}
	}
	return "", fmt.Errorf(
		"the app runtime binary %s is not installed; looked in %s. It is built by `make build-app-runtime-host` (or any `make install`), and %s overrides the location",
		appRuntimeBinaryName, strings.Join(candidates, " and "), appRuntimeHostOverride)
}

// appRuntimeHostCandidates lists where a daemon at `executable` looks for its
// sidecar, in order.
func appRuntimeHostCandidates(executable, profile string) []string {
	candidates := []string{}
	// Inside a .app the daemon is Contents/MacOS/attn and Tauri stages resources
	// under Contents/Resources.
	binDir := filepath.Dir(executable)
	contentsDir := filepath.Dir(binDir)
	if filepath.Base(binDir) == "MacOS" && filepath.Base(contentsDir) == "Contents" {
		candidates = append(candidates, filepath.Join(contentsDir, "Resources", "app-runtime", appRuntimeBinaryName))
	}
	// A checkout, and every Linux install: beside the daemon binary. A named
	// profile looks for its own copy first — profile-isolated daemons on a remote
	// share one `~/.local/bin`, and each needs the sidecar built from the same
	// source as the binary beside it. A checkout is the case that keeps the
	// unsuffixed name last: there `./attn` serves whatever profile is exported.
	if profile != "" {
		candidates = append(candidates, filepath.Join(binDir, apps.RuntimeHostBinaryNameForProfile(profile)))
	}
	return append(candidates, filepath.Join(binDir, appRuntimeBinaryName))
}

// appRuntimeLogDir sits beside the plugin log tree rather than under the app
// artifact store: everything under appsDir is named after an app, and a `log`
// directory there would be one collision away from an app called log.
func appRuntimeLogDir(socketPath string) string {
	return filepath.Join(filepath.Dir(socketPath), "app-runtime-log")
}

// AppRuntimeLogPath is where the shared sidecar's captured output lands.
// `attn app logs` reads it and filters by the per-app tag the host writes.
func AppRuntimeLogPath(socketPath string) string {
	return filepath.Join(appRuntimeLogDir(socketPath), appRuntimeChildName+".log")
}

// appRuntimeAppTag is the prefix the host puts on every line an app's handler
// printed. It is the whole of `attn app logs <name>`'s filter, and it is
// duplicated in apphost/src/index.ts because the two sides are different
// languages; the parity test is TestAppLogTagMatchesTheHost.
func appRuntimeAppTag(app string) string { return "[app " + app + "] " }

// appRuntimeSelfTag prefixes the host's own lines — everything not written from
// inside a handler.
const appRuntimeSelfTag = "[runtime] "

// ensureAppRuntimeSupervisor builds the supervisor on first use.
//
// The tunable half comes from appRuntimeSupervise — the clock and the give-up
// tripwire, which a test overrides so a crash loop can be watched without
// waiting out the real backoff schedule. Everything the daemon owns is set here
// and cannot be overridden.
func (d *Daemon) ensureAppRuntimeSupervisor() *supervise.Supervisor {
	d.appRuntimeMu.Lock()
	defer d.appRuntimeMu.Unlock()
	if d.appRuntimeSupervisor == nil {
		options := d.appRuntimeSupervise
		options.LogDir = appRuntimeLogDir(d.socketPath)
		options.OnChange = func(string) {
			d.publishFact(FactAppRuntimeChanged, appRuntimeChildName, nil)
		}
		options.OnGiveUp = d.notifyAppRuntimeParked
		options.Logf = d.logf
		d.appRuntimeSupervisor = supervise.New(options)
	}
	return d.appRuntimeSupervisor
}

// ensureAppRuntime starts the sidecar, or adopts it if it is already running.
// It doubles as un-park: supervise.Ensure resets the restart counter, which is
// what `attn app runtime restart` needs after a crash loop.
func (d *Daemon) ensureAppRuntime() error {
	return d.startAppRuntime(true)
}

// startAppRuntimeForDispatch is the dispatch path's way in: it starts the
// runtime lazily, so an app installed into a daemon that has never run one needs
// no restart, but it leaves a parked runtime parked and answers ErrParked.
//
// Reviving here would make parking unreachable. Every delivery attempt passes
// through this, and the bus retries a failing delivery forever, so a reviving
// call would hand the crash loop a fresh restart budget every couple of minutes:
// the runtime would never rest at parked, and each parking on the way would
// raise its own critical notification. Measured on a broken host: three parkings
// and three notifications in five and a half minutes.
func (d *Daemon) startAppRuntimeForDispatch() error {
	return d.startAppRuntime(false)
}

func (d *Daemon) startAppRuntime(revive bool) error {
	host, err := resolveAppRuntimeHost()
	if err != nil {
		return err
	}
	// The artifact store is the sidecar's working directory, and a daemon whose
	// apps have all been removed — or that has never had one — does not have it.
	// exec refuses to chdir into a directory that is not there, so the runtime
	// would fail to start for a reason that has nothing to do with the runtime.
	if err := os.MkdirAll(d.appsDir, 0o755); err != nil {
		return fmt.Errorf("creating the app artifact directory %s to start the runtime in: %w", d.appsDir, err)
	}
	start := func(req supervise.StartRequest) (supervise.Process, error) {
		cmd := exec.Command(host)
		cmd.Dir = d.appsDir
		cmd.Env = d.appRuntimeEnv(req.Generation)
		process, err := supervise.StartCommand(cmd, req.Log)
		if err != nil {
			return nil, fmt.Errorf("start the app runtime (%s): %w", host, err)
		}
		return process, nil
	}
	supervisor := d.ensureAppRuntimeSupervisor()
	if revive {
		return supervisor.Ensure(appRuntimeChildName, start)
	}
	return supervisor.EnsureUnlessParked(appRuntimeChildName, start)
}

// appRuntimeEnv is what the sidecar is launched with. It is the plugin
// environment's shape — a scrubbed base plus explicit overrides — because the
// daemon may itself be running inside an agent session whose CLAUDE_CODE_*
// variables would otherwise leak into app code.
func (d *Daemon) appRuntimeEnv(generation uint64) []string {
	return d.pluginCommandEnv(
		"ATTN_SOCKET_PATH="+d.socketPath,
		"ATTN_APP_RUNTIME_GENERATION="+strconv.FormatUint(generation, 10),
	)
}

// stopAppRuntime ends supervision. Called from daemon shutdown; a stopped
// runtime is restarted by the next Ensure.
func (d *Daemon) stopAppRuntime() {
	d.appRuntimeMu.Lock()
	supervisor := d.appRuntimeSupervisor
	d.appRuntimeMu.Unlock()
	if supervisor != nil {
		supervisor.Shutdown()
	}
}

// appRuntimeSnapshot reports supervision state, and whether there is a
// supervisor at all. A daemon that has never had an app has never built one, and
// saying "no runtime has been started" is a different answer from "stopped".
func (d *Daemon) appRuntimeSnapshot() (supervise.Snapshot, bool) {
	d.appRuntimeMu.Lock()
	supervisor := d.appRuntimeSupervisor
	d.appRuntimeMu.Unlock()
	if supervisor == nil {
		return supervise.Snapshot{}, false
	}
	return supervisor.Snapshot(appRuntimeChildName)
}

// notificationKindAppRuntimeParked marks the notification written when the
// sidecar crash-looped past the give-up tripwire.
const notificationKindAppRuntimeParked = "app_runtime_parked"

// notifyAppRuntimeParked is the supervisor's OnGiveUp sink. Unlike a parked
// plugin, a parked app runtime has a way back — `attn app runtime restart` — so
// the copy names it.
func (d *Daemon) notifyAppRuntimeParked(_ string, snapshot supervise.Snapshot) {
	detail := ""
	if snapshot.LastExit != nil {
		detail = snapshot.LastExit.String()
	}
	d.logf("app runtime parked after %d restarts without a stable connection: %s", snapshot.RestartAttempt, detail)
	if d.store == nil {
		return
	}
	record, err := d.store.AddNotification(store.NotificationRecord{
		Kind:     notificationKindAppRuntimeParked,
		Severity: store.NotificationCritical,
		Title:    "Apps stopped running",
		Body: fmt.Sprintf(
			"attn restarted the shared app runtime %d times without it ever staying up, and has stopped trying. No app's handlers are running. `attn app runtime status` shows why it exited; `attn app runtime restart` tries again.",
			snapshot.RestartAttempt),
		Detail:     detail,
		SourceKind: "app_runtime",
		SourceID:   appRuntimeChildName,
	}, d.appNow())
	if err != nil {
		d.logf("notifications: add app-runtime-parked notification: %v", err)
		return
	}
	d.publishFact(FactNotificationCreated, record.ID, nil)
}

// appNow is the daemon's clock for everything app-runtime — the stall clock most
// of all, which measures a fifteen-minute window no test may wait out.
func (d *Daemon) appNow() time.Time {
	if d.appClock != nil {
		return d.appClock()
	}
	return time.Now()
}

// ---------------------------------------------------------------------------
// Dispatch
// ---------------------------------------------------------------------------

// appDispatchTimeout bounds one handler run.
//
// It exists because of what has no other way out: a handler that awaits
// something that never resolves would hold its app's delivery forever, recording
// no failure, never advancing the stall clock, and pinning the event log's
// retention floor for everybody. A hung handler must become a failure the app
// owns.
//
// The number is a tripwire, not a budget. A scaffolded handler doing real
// document work was measured at 0–1ms warm (receipt in the plan doc); sixty
// seconds is four to five orders of magnitude past that, so an app has to be
// broken to feel it.
const appDispatchTimeout = 60 * time.Second

// appDispatchRequest is `app.dispatch`, the daemon's one call into the sidecar.
//
// Handler is resolved here rather than in the host: the daemon holds the frozen
// declaration and the same pattern matching the bus filter uses, and a second
// implementation of that rule in TypeScript is one free to drift.
//
// Artifact is an absolute path, and that is the whole of the hot-reload story:
// versions are content-addressed, so each one has its own path, `import()`
// caches by path, and a dispatch that started on the old version keeps the module
// it started on while the next dispatch gets the new one.
type appDispatchRequest struct {
	Dispatch    string           `json:"dispatch"`
	App         string           `json:"app"`
	VersionID   int64            `json:"version_id"`
	Artifact    string           `json:"artifact"`
	Handler     string           `json:"handler"`
	Collections []string         `json:"collections"`
	Event       appDispatchEvent `json:"event"`
}

type appDispatchEvent struct {
	Name        string `json:"name"`
	Subject     string `json:"subject"`
	Seq         int64  `json:"seq"`
	Payload     any    `json:"payload"`
	PublishedAt string `json:"published_at"`
}

// appDispatchResult is the host's answer. A handler that threw comes back as
// ok:false with the text — a normal answer, not an RPC failure — which is
// exactly how an app's fault is told apart from the runtime's.
type appDispatchResult struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// appDispatch is one in-flight handler run, as the daemon sees it.
//
// It is the reason an app cannot address another app's documents: a collection
// callback from the sidecar carries this dispatch's id and a collection name,
// and the daemon resolves the namespace from its own record here. The wire has
// no namespace field for an app to fill in.
type appDispatch struct {
	id          string
	app         string
	namespace   string
	versionID   int64
	collections map[string]struct{}
}

// registerAppDispatch mints an id and records the dispatch so its callbacks can
// be resolved.
func (d *Daemon) registerAppDispatch(dispatch *appDispatch) {
	d.appDispatchMu.Lock()
	defer d.appDispatchMu.Unlock()
	if d.appDispatches == nil {
		d.appDispatches = make(map[string]*appDispatch)
	}
	d.appDispatchSeq++
	dispatch.id = strconv.FormatUint(d.appDispatchSeq, 10)
	d.appDispatches[dispatch.id] = dispatch
}

func (d *Daemon) releaseAppDispatch(id string) {
	d.appDispatchMu.Lock()
	defer d.appDispatchMu.Unlock()
	delete(d.appDispatches, id)
}

// lookupAppDispatch resolves a collection callback to the dispatch that is
// allowed to make it. A callback for an id that is no longer in flight is a
// handler that kept a reference to its context and used it after returning, and
// it is refused rather than served against whatever app is running now.
func (d *Daemon) lookupAppDispatch(id string) (*appDispatch, error) {
	d.appDispatchMu.Lock()
	defer d.appDispatchMu.Unlock()
	dispatch, ok := d.appDispatches[id]
	if !ok {
		return nil, fmt.Errorf(
			"dispatch %s is not in flight; a collection can only be reached from inside the handler it was given to, and this call arrived after that handler returned",
			id)
	}
	return dispatch, nil
}

// appRuntimeFailure marks an error as the runtime's rather than the app's, so
// the delivery path classifies it without matching on text.
type appRuntimeFailure struct{ err error }

func (e *appRuntimeFailure) Error() string { return e.err.Error() }
func (e *appRuntimeFailure) Unwrap() error { return e.err }

func runtimeFailure(format string, args ...any) error {
	return &appRuntimeFailure{err: fmt.Errorf(format, args...)}
}

// isRuntimeFailure reports whether a dispatch failure belongs to the runtime.
func isRuntimeFailure(err error) bool {
	var failure *appRuntimeFailure
	return errors.As(err, &failure)
}

// appRuntimeConnected returns the live sidecar connection, or nil.
func (d *Daemon) appRuntimeConnected() *appRuntimeConnection {
	d.appRuntimeMu.Lock()
	defer d.appRuntimeMu.Unlock()
	return d.appRuntimeConn
}
