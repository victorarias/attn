package daemon

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/appbuild"
	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The app registry as the UI sees it: what is mountable, and the report that
// comes back when one of those mounts crashes.
//
// A4 published `app.version.changed`, `app.enabled.changed` and `app.removed`
// with no projection, because the registry had no UI surface. It has one now, so
// each of the three re-pushes this snapshot — which is the whole reload
// mechanism: a version flip moves an app's content hash, and a mounted tile
// remounts because its bundle URL moved.
//
// Design: docs/plans/2026-08-13-ext-a5-ui-host-and-app-sdk.md, "The app UI
// registry" and "Reload".

// appViewCrashEvent is the event name a render crash is recorded against. A
// crash has no fact behind it, so it names itself rather than borrowing a bus
// fact's name and claiming a seq it does not have.
const appViewCrashEvent = "app.view.crashed"

// appViewCrashErrorLimit bounds what a client can write into the invocation log
// in one report.
//
// The tripwire is the log, not the message: a component stack from a deep tree
// is a few kilobytes, and this is ~8x past the largest React has produced in
// this app. A crash loop is bounded by mount attempts, and the invocation log
// already caps rows per app — this caps how big one of those rows can get. Over
// the limit the text is truncated with a line saying so, never dropped: half a
// stack still names the component.
const appViewCrashErrorLimit = 32 * 1024

// appRegistryForWire is every app on THIS daemon, with the views its serving
// version declares.
//
// Deliberately not built from appSummary: that answer joins the bus consumer's
// cursor and lag, which costs a bus-head read per push and means nothing to a
// tile. What a tile needs is the content hash (its bundle URL), the version id
// (what to stamp a crash with) and the enabled bit.
func (d *Daemon) appRegistryForWire() []protocol.AppRegistryEntry {
	if d.store == nil {
		return nil
	}
	rows, err := d.store.ListApps()
	if err != nil {
		d.logf("apps: listing apps for the UI registry: %v", err)
		return nil
	}
	out := make([]protocol.AppRegistryEntry, 0, len(rows))
	for _, row := range rows {
		entry, err := d.appRegistryEntry(row)
		if err != nil {
			// One unreadable app must not blank the picker for every other one.
			d.logf("apps: describing app %s for the UI registry: %v", row.Name, err)
			continue
		}
		out = append(out, entry)
	}
	return out
}

func (d *Daemon) appRegistryEntry(row store.App) (protocol.AppRegistryEntry, error) {
	entry := protocol.AppRegistryEntry{Name: row.Name, Views: []protocol.AppViewInfo{}}
	if consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(row.Name)); err != nil {
		return protocol.AppRegistryEntry{}, fmt.Errorf("reading its bus consumer: %w", err)
	} else if ok {
		entry.Enabled = consumer.Enabled
	}
	if row.CurrentVersionID == 0 {
		return entry, nil
	}
	version, ok, err := d.store.GetAppVersion(row.CurrentVersionID)
	if err != nil {
		return protocol.AppRegistryEntry{}, fmt.Errorf("reading version %d: %w", row.CurrentVersionID, err)
	}
	if !ok {
		return entry, nil
	}
	entry.VersionID = protocol.Ptr(int(version.ID))
	entry.ContentHash = protocol.Ptr(version.ContentHash)
	if description := appDeclarationDescription(version.Declaration); description != "" {
		entry.Description = protocol.Ptr(description)
	}
	entry.Views = appViewsForWire(version.Declaration, d.logf)
	return entry, nil
}

// appViewsForWire reads a version's views out of its frozen declaration.
//
// The declaration is the only description of a version the daemon holds, and it
// is what makes a rollback honest: an old version offers the views it was built
// with, not the ones the manifest names today.
func appViewsForWire(declaration string, logf func(string, ...any)) []protocol.AppViewInfo {
	views, err := appbuild.DeclaredViews(declaration)
	if err != nil {
		// A declaration this daemon recorded should always parse; if it does not,
		// an app with no views is the safe answer — nothing mounts, rather than
		// something mounting under a name that was never validated.
		if logf != nil {
			logf("apps: reading the views of a stored declaration: %v", err)
		}
		return []protocol.AppViewInfo{}
	}
	out := make([]protocol.AppViewInfo, 0, len(views))
	for _, v := range views {
		info := protocol.AppViewInfo{Name: v.Name, Kind: v.Kind, Title: v.Title}
		if v.Params != nil {
			info.ParamsLabel = protocol.Ptr(v.Params.Label)
			if v.Params.Placeholder != "" {
				info.ParamsPlaceholder = protocol.Ptr(v.Params.Placeholder)
			}
		}
		out = append(out, info)
	}
	return out
}

// appDeclarationDescription reads the manifest's description back out of a
// frozen declaration, for the one line the dock picker shows under a view. A
// description is decoration: a declaration that will not parse costs the picker
// a subtitle, not the app.
func appDeclarationDescription(declaration string) string {
	var snapshot struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal([]byte(declaration), &snapshot); err != nil {
		return ""
	}
	return strings.TrimSpace(snapshot.Description)
}

// projectAppsUpdated re-pushes the whole registry. A snapshot rather than a
// delta because every mounted tile has to re-decide what it is serving, and the
// list is a handful of rows.
func (d *Daemon) projectAppsUpdated() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotApps, func() {
		apps := d.appRegistryForWire()
		// AppsUpdatedMessage is its own top-level type, so the hub's
		// WebSocketEvent-only broadcast listener cannot see it; tests use this hook.
		if d.appsBroadcastHook != nil {
			d.appsBroadcastHook(apps)
		}
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.AppsUpdatedMessage{
			Event: protocol.EventAppsUpdated,
			Apps:  apps,
		})
	})
}

// handleAppViewCrash records a caught render error as an invocation of the app,
// stamped with the version that served the bundle.
//
// It deliberately does NOT advance the app's stall clock. That clock counts
// deliveries of one bus event failing over and over, because such an app holds
// the durable log's retention floor open for every other consumer. A crashing
// tile pins nothing: nobody is waiting on it, and charging it toward
// auto-disable would let a person opening a broken workspace disable an app
// whose handlers are healthy.
func (d *Daemon) handleAppViewCrash(_ *wsClient, msg *protocol.AppViewCrashMessage) {
	name := strings.TrimSpace(msg.App)
	if err := apps.ValidateName(name); err != nil {
		d.logf("apps: a view crash report named app %q: %v", msg.App, err)
		return
	}
	view := strings.TrimSpace(msg.View)
	if err := apps.ValidateViewName(view); err != nil {
		d.logf("apps: a view crash report of app %s named view %q: %v", name, msg.View, err)
		return
	}
	if d.store == nil {
		return
	}
	// The version has to be one of this app's: the report arrives from a client
	// and the invocation log's whole value is that "which version ran" is
	// answerable. A stamp that names another app's version, or none, is dropped
	// rather than recorded under a lie.
	version, ok, err := d.store.GetAppVersion(int64(msg.VersionID))
	if err != nil {
		d.logf("apps: reading version %d for a view crash report of %s: %v", msg.VersionID, name, err)
		return
	}
	if !ok || version.AppName != name {
		d.logf("apps: dropping a view crash report of %s/%s: version %d is not a version of that app", name, view, msg.VersionID)
		return
	}

	text := strings.TrimSpace(msg.Error)
	if text == "" {
		text = "the view threw while rendering and the boundary caught no message"
	}
	if len(text) > appViewCrashErrorLimit {
		text = text[:appViewCrashErrorLimit] + fmt.Sprintf("\n… truncated at %d bytes", appViewCrashErrorLimit)
	}
	now := time.Now()
	d.logf("apps: view %s/%s crashed while rendering at version %d: %s", name, view, version.ID, firstLine(text))
	d.recordAppInvocation(store.AppInvocation{
		AppName:      name,
		VersionID:    version.ID,
		Kind:         store.AppInvocationKindView,
		EventName:    appViewCrashEvent,
		EventSubject: strings.TrimSpace(msg.TileID),
		Handler:      apps.ViewLabel(view),
		Status:       appInvocationStatusError,
		Error:        text,
		StartedAt:    now,
	})
	// The invocation is the countable record; the log is where the author looks.
	// `attn app logs <app>` is what the crashed tile tells them to run, and an
	// invocation row truncates the stack that says which line threw.
	if err := appendAppLogLines(AppRuntimeLogPath(d.socketPath), name, fmt.Sprintf(
		"view %s crashed while rendering (version %d)\n%s", view, version.ID, text)); err != nil {
		d.logf("apps: writing the view crash of %s/%s to the app log: %v", name, view, err)
	}
}

// appendAppLogLines adds one app's lines to the shared runtime log, each under
// the tag `attn app logs` filters by.
//
// The supervisor's capture holds the same file open with O_APPEND, so both
// writers land at the end; the whole block goes out in one write so a crash's
// stack cannot interleave with what a handler is printing at the same moment.
func appendAppLogLines(path, app, text string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()

	tag := appRuntimeAppTag(app)
	var block strings.Builder
	for _, line := range strings.Split(text, "\n") {
		block.WriteString(tag)
		block.WriteString(line)
		block.WriteByte('\n')
	}
	_, err = file.WriteString(block.String())
	return err
}
