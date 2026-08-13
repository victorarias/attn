package daemon

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
	"github.com/victorarias/attn/internal/supervise"
)

// The runtime's IPC surface: `attn app logs`, `attn app runtime status`,
// `attn app runtime restart`, and the invocation stream `attn app dev` renders.
//
// `runtime` is addressed here as itself rather than as an app, which is the
// whole reason internal/apps refuses it as an app name: one word cannot mean
// both the shared process and somebody's automation.

// appLogDefaultLines is how many matching lines `attn app logs` returns when the
// caller does not say. Enough to see a handler's last few runs and the error
// that ended them; `--lines` asks for more.
const appLogDefaultLines = 200

// appLogMaxLines bounds one answer. The log is a file the runtime appends to for
// as long as the daemon lives, and a request for all of it is a socket message
// with no ceiling.
const appLogMaxLines = 10000

func (d *Daemon) handleAppLogs(conn net.Conn, msg *protocol.AppLogsMessage) {
	name := strings.TrimSpace(msg.Name)
	whole := name == appRuntimeChildName
	if !whole {
		if err := apps.ValidateName(name); err != nil {
			d.sendError(conn, err.Error())
			return
		}
		if d.store == nil {
			d.sendError(conn, "no database")
			return
		}
		if _, ok, err := d.store.GetApp(name); err != nil {
			d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
			return
		} else if !ok {
			d.sendError(conn, d.unknownAppError("logs", name))
			return
		}
	}

	limit := int(protocol.Deref(msg.Lines))
	if limit <= 0 {
		limit = appLogDefaultLines
	}
	if limit > appLogMaxLines {
		d.sendError(conn, fmt.Sprintf(
			"app logs %s: asked for %d lines, and the most this returns in one answer is %d. Read %s directly for more.",
			name, limit, appLogMaxLines, AppRuntimeLogPath(d.socketPath)))
		return
	}

	path := AppRuntimeLogPath(d.socketPath)
	lines, truncated, err := readAppLog(path, name, whole, limit)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("app logs %s: reading %s: %v", name, path, err))
		return
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppLogsResult: &protocol.AppLogsResult{
			Name: name, Path: path, Lines: lines, Truncated: truncated,
		},
	})
}

// readAppLog tails the shared runtime log, keeping the lines one app wrote.
//
// A log file that is not there is an empty answer, not an error: the runtime
// writes it on its first start, and "no lines yet" is exactly what a caller
// asking before then should hear.
//
// It reads the whole file rather than seeking from the end. The file is one
// daemon's runtime output — bounded by uptime, not by history — and a backwards
// scan that has to respect line boundaries and a tag filter is a lot of
// machinery to save milliseconds on a diagnostic command.
func readAppLog(path, app string, whole bool, limit int) ([]string, bool, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, false, nil
		}
		return nil, false, err
	}
	defer file.Close()

	tag := appRuntimeAppTag(app)
	kept := make([]string, 0, limit)
	truncated := false
	scanner := bufio.NewScanner(file)
	// A handler can print a long line — a stack trace, a JSON body — and the
	// default 64KB would end the scan on it rather than truncating the line.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if !whole {
			if !strings.HasPrefix(line, tag) {
				continue
			}
			line = strings.TrimPrefix(line, tag)
		}
		if len(kept) == limit {
			kept = kept[1:]
			truncated = true
		}
		kept = append(kept, line)
	}
	if err := scanner.Err(); err != nil {
		return nil, false, err
	}
	return kept, truncated, nil
}

func (d *Daemon) handleAppRuntimeStatus(conn net.Conn, _ *protocol.AppRuntimeStatusMessage) {
	result := protocol.AppRuntimeStatusResult{LogPath: AppRuntimeLogPath(d.socketPath)}
	if host, err := resolveAppRuntimeHost(); err != nil {
		result.HostError = protocol.Ptr(err.Error())
	} else {
		result.HostPath = protocol.Ptr(host)
	}
	if d.store != nil {
		rows, err := d.store.ListApps()
		if err != nil {
			d.sendError(conn, fmt.Sprintf("listing apps: %v", err))
			return
		}
		result.Apps = len(rows)
		for _, row := range rows {
			consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(row.Name))
			if err == nil && ok && consumer.Enabled {
				result.AppsEnabled++
			}
		}
	}
	if snapshot, ok := d.appRuntimeSnapshot(); ok {
		info := d.appRuntimeInfo(snapshot)
		result.Runtime = &info
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppRuntimeStatusResult: &result})
}

// handleAppRuntimeRestart bounces the sidecar: kill what is running, start a
// fresh generation. Stop-then-Ensure covers both cases the verb has to serve —
// a healthy runtime gets restarted, and a parked one is revived, because Ensure
// is also un-park.
func (d *Daemon) handleAppRuntimeRestart(conn net.Conn, _ *protocol.AppRuntimeRestartMessage) {
	was := string(supervise.PhaseStopped)
	if snapshot, ok := d.appRuntimeSnapshot(); ok {
		was = string(snapshot.Phase)
	}
	d.ensureAppRuntimeSupervisor().Stop(appRuntimeChildName)
	if err := d.ensureAppRuntime(); err != nil {
		d.sendError(conn, fmt.Sprintf("app runtime restart: %v", err))
		return
	}
	snapshot, _ := d.appRuntimeSnapshot()
	info := d.appRuntimeInfo(snapshot)
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppRuntimeRestartResult: &protocol.AppRuntimeRestartResult{
			Was: was, Runtime: info,
		},
	})
}

// appRuntimeInfo renders a supervision snapshot for the wire, joined to the
// connected process's pid — which the supervisor does not know, because it
// supervises a process and the pid that matters is the one that said hello.
func (d *Daemon) appRuntimeInfo(snapshot supervise.Snapshot) protocol.AppRuntimeInfo {
	info := protocol.AppRuntimeInfo{
		Phase:          string(snapshot.Phase),
		Desired:        string(snapshot.Desired),
		Running:        snapshot.Running,
		Connected:      snapshot.Connected,
		Generation:     int(snapshot.Generation),
		RestartAttempt: int(snapshot.RestartAttempt),
	}
	if !snapshot.StartedAt.IsZero() {
		info.StartedAt = protocol.Ptr(stampForWire(snapshot.StartedAt))
	}
	if !snapshot.ConnectedAt.IsZero() {
		info.ConnectedAt = protocol.Ptr(stampForWire(snapshot.ConnectedAt))
	}
	if !snapshot.NextRestartAt.IsZero() {
		info.NextRestartAt = protocol.Ptr(stampForWire(snapshot.NextRestartAt))
	}
	if !snapshot.ParkedAt.IsZero() {
		info.ParkedAt = protocol.Ptr(stampForWire(snapshot.ParkedAt))
	}
	if snapshot.LastExit != nil {
		info.LastExit = protocol.Ptr(snapshot.LastExit.String())
	}
	if runtime := d.appRuntimeConnected(); runtime != nil {
		info.Pid = protocol.Ptr(runtime.pid)
	}
	return info
}

// ---------------------------------------------------------------------------
// The invocation stream
// ---------------------------------------------------------------------------

// appWatcher is one open `app_watch` connection.
//
// Deliveries are dropped rather than queued when the buffer fills: this is a
// developer watching their handlers run, and a slow reader must not be able to
// slow down the delivery loop that feeds it. A watcher that misses a burst can
// read the whole record back with `attn app status`.
type appWatcher struct {
	app    string
	events chan protocol.AppInvocationInfo
}

func (d *Daemon) addAppWatcher(watcher *appWatcher) {
	d.appWatcherMu.Lock()
	defer d.appWatcherMu.Unlock()
	if d.appWatchers == nil {
		d.appWatchers = make(map[*appWatcher]struct{})
	}
	d.appWatchers[watcher] = struct{}{}
}

func (d *Daemon) removeAppWatcher(watcher *appWatcher) {
	d.appWatcherMu.Lock()
	defer d.appWatcherMu.Unlock()
	delete(d.appWatchers, watcher)
}

// notifyAppWatchers fans one recorded invocation out to whoever is watching that
// app. Called from the delivery path, so it must never block.
func (d *Daemon) notifyAppWatchers(info protocol.AppInvocationInfo, app string) {
	d.appWatcherMu.Lock()
	watchers := make([]*appWatcher, 0, len(d.appWatchers))
	for watcher := range d.appWatchers {
		if watcher.app == app {
			watchers = append(watchers, watcher)
		}
	}
	d.appWatcherMu.Unlock()
	for _, watcher := range watchers {
		select {
		case watcher.events <- info:
		default:
		}
	}
}

func (d *Daemon) handleAppWatch(conn net.Conn, msg *protocol.AppWatchMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	watcher := &appWatcher{app: name, events: make(chan protocol.AppInvocationInfo, 64)}
	d.addAppWatcher(watcher)
	defer d.removeAppWatcher(watcher)

	// The caller learns the subscription is live before the first invocation, so
	// `attn app dev` can say it is watching rather than sitting silent.
	if err := json.NewEncoder(conn).Encode(protocol.Response{Ok: true}); err != nil {
		return
	}

	// A client that goes away between invocations would otherwise sit here until
	// the daemon stops. Reading from the connection is how that is noticed: the
	// caller sends nothing, so any read that returns means the socket closed.
	gone := make(chan struct{})
	go func() {
		defer close(gone)
		_, _ = conn.Read(make([]byte, 1))
	}()

	encoder := json.NewEncoder(conn)
	for {
		select {
		case <-d.done:
			return
		case <-gone:
			return
		case info := <-watcher.events:
			result := info
			if err := encoder.Encode(protocol.Response{
				Ok:             true,
				AppWatchResult: &protocol.AppWatchResult{Invocation: result},
			}); err != nil {
				return
			}
		}
	}
}

// appInvocationForWire renders a recorded invocation, so the streamed shape and
// the one `attn app status` returns cannot drift.
func appInvocationForWire(id int64, inv store.AppInvocation) protocol.AppInvocationInfo {
	return protocol.AppInvocationInfo{
		ID:           int(id),
		VersionID:    int(inv.VersionID),
		EventSeq:     int(inv.EventSeq),
		EventName:    inv.EventName,
		EventSubject: inv.EventSubject,
		Handler:      inv.Handler,
		Status:       inv.Status,
		Error:        inv.Error,
		DurationMs:   int(inv.Duration.Milliseconds()),
		StartedAt:    stampForWire(inv.StartedAt),
	}
}

// appStallForWire renders the auto-disable clock, including when it fires.
func appStallForWire(stall appStall) protocol.AppStallInfo {
	return protocol.AppStallInfo{
		EventSeq:   int(stall.seq),
		EventName:  stall.eventName,
		Since:      stampForWire(stall.since),
		Attempts:   stall.attempts,
		LastError:  stall.lastError,
		DisablesAt: stampForWire(stall.since.Add(appAutoDisableStall)),
	}
}
