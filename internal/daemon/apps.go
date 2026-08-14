package daemon

import (
	"fmt"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/apps"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The app registry's daemon half: the IPC surface `attn app` speaks.
//
// This is the read-and-flip side of the registry. What it deliberately does NOT
// do is invent state: an app's enabled bit is its bus consumer's and is read
// from there, and an app with no consumer reports that rather than a default.
// Everything a running app runtime knows — is it loaded, is it stalled, what did
// it last fail on — is absent here because nothing observes it yet, and a status
// that answers a question it cannot answer is worse than one that says so.
//
// See docs/plans/2026-08-06-ext-a4-app-registry-and-runtime.md.

// recentInvocationLimit is how many invocations `attn app status` carries back.
// Enough to see a failure pattern, small enough that a status call never hauls
// an app's whole history over the socket; `attn app logs` is the surface for
// more.
const recentInvocationLimit = 10

// recentVersionLimit is how many version ids `attn app status` carries back.
//
// Rollback names ids in its refusals and nothing else lists them, so status has
// to. Ten because it is the same answer shape as the invocations above and a
// rollback target is a version the operator still remembers applying; the count
// beside the list is what keeps a truncated answer honest. An AppVersionInfo is
// ~200 bytes on the wire, so ten is ~2KB on a call that already carries ten
// invocations.
const recentVersionLimit = 10

// servingHistoryLimit is how many steps of an app's serving history
// `attn app status` carries back, on the same wire budget and for the same
// reason as recentVersionLimit above. Ten is far past any real walk: a step is
// pushed only when the serving version actually changes, and an operator
// hunting for the version that worked gives up long before ten bare rollbacks.
// The list says when it was cut, so a longer chain is visible rather than
// silently ending.
const servingHistoryLimit = 10

// appEnabledChanged is FactAppEnabledChanged's payload.
type appEnabledChanged struct {
	Name     string `json:"name"`
	Consumer string `json:"consumer"`
	Enabled  bool   `json:"enabled"`
}

// appRemoved is FactAppRemoved's payload. It names the consumer and namespace
// because a consumer of this fact cannot look them up: the registry row is gone.
type appRemoved struct {
	Name      string `json:"name"`
	Consumer  string `json:"consumer"`
	Namespace string `json:"namespace"`
}

func (d *Daemon) handleAppList(conn net.Conn, _ *protocol.AppListMessage) {
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	rows, err := d.store.ListApps()
	if err != nil {
		d.sendError(conn, fmt.Sprintf("listing apps: %v", err))
		return
	}
	head, err := d.busHead()
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the event log: %v", err))
		return
	}
	summaries := make([]protocol.AppSummary, 0, len(rows))
	for _, row := range rows {
		summary, err := d.appSummary(row, head)
		if err != nil {
			d.sendError(conn, err.Error())
			return
		}
		summaries = append(summaries, summary)
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok:            true,
		AppListResult: &protocol.AppListResult{Apps: summaries},
	})
}

func (d *Daemon) handleAppStatus(conn net.Conn, msg *protocol.AppStatusMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	row, ok, err := d.store.GetApp(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	}
	if !ok {
		d.sendError(conn, d.unknownAppError("status", name))
		return
	}
	head, err := d.busHead()
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the event log: %v", err))
		return
	}
	summary, err := d.appSummary(row, head)
	if err != nil {
		d.sendError(conn, err.Error())
		return
	}
	versions, err := d.store.CountAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting versions of app %q: %v", name, err))
		return
	}
	invocations, err := d.store.CountAppInvocations(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting invocations of app %q: %v", name, err))
		return
	}
	recent, err := d.store.ListAppInvocations(name, recentInvocationLimit)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading invocations of app %q: %v", name, err))
		return
	}
	allVersions, err := d.store.ListAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading versions of app %q: %v", name, err))
		return
	}
	history, steps, err := d.store.ListAppServingHistory(name, servingHistoryLimit)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the serving history of app %q: %v", name, err))
		return
	}
	recentVersions := allVersions
	if len(recentVersions) > recentVersionLimit {
		recentVersions = recentVersions[:recentVersionLimit]
	}

	result := protocol.AppStatusResult{App: summary, Versions: versions, Invocations: invocations}
	for _, version := range recentVersions {
		result.RecentVersions = append(result.RecentVersions, protocol.AppVersionInfo{
			ID:           int(version.ID),
			ContentHash:  version.ContentHash,
			ArtifactPath: version.ArtifactPath,
			CreatedAt:    stampForWire(version.CreatedAt),
		})
	}
	// The history walks past the newest versions once an app has been rolled
	// back, so its rows come from the whole list rather than the capped one.
	byID := make(map[int64]store.AppVersion, len(allVersions))
	for _, version := range allVersions {
		byID[version.ID] = version
	}
	for _, id := range history {
		version, ok := byID[id]
		if !ok {
			continue
		}
		result.ServingHistory = append(result.ServingHistory, protocol.AppVersionInfo{
			ID:           int(version.ID),
			ContentHash:  version.ContentHash,
			ArtifactPath: version.ArtifactPath,
			CreatedAt:    stampForWire(version.CreatedAt),
		})
	}
	if steps > 0 {
		result.ServingHistorySteps = protocol.Ptr(steps)
	}
	for _, inv := range recent {
		result.Recent = append(result.Recent, appInvocationForWire(inv.ID, inv))
	}
	// The two things only the running daemon knows: whether the shared runtime is
	// up, and whether this app is on the auto-disable clock. Both absent when
	// there is nothing to report, never defaulted.
	if snapshot, ok := d.appRuntimeSnapshot(); ok {
		info := d.appRuntimeInfo(snapshot)
		result.Runtime = &info
	}
	if stall, ok := d.appStallSnapshot(name); ok {
		info := appStallForWire(stall)
		result.Stall = &info
	}
	d.sendDocResponse(conn, protocol.Response{Ok: true, AppStatusResult: &result})
}

// handleAppSetEnabled flips the app's bus consumer bit, which IS the app's
// enabled state — there is no second bit anywhere, by design.
//
// It refuses an app with no consumer instead of creating one. A consumer carries
// a cursor, and minting one here would silently decide where an app that has
// never run should start reading from; that decision belongs to whatever loads
// the app, not to the enable verb.
func (d *Daemon) handleAppSetEnabled(conn net.Conn, msg *protocol.AppSetEnabledMessage) {
	name := strings.TrimSpace(msg.Name)
	verb := "disable"
	if msg.Enabled {
		verb = "enable"
	}
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
		d.sendError(conn, d.unknownAppError(verb, name))
		return
	}
	consumer := apps.ConsumerName(name)
	if _, ok, err := d.store.GetBusConsumer(consumer); err != nil {
		d.sendError(conn, fmt.Sprintf("reading the bus consumer for app %q: %v", name, err))
		return
	} else if !ok {
		d.sendError(conn, fmt.Sprintf(
			"app %q has no bus consumer (%s), so there is no enabled bit to %s: nothing is delivering facts to it. "+
				"A consumer is registered when a version is applied or rolled onto, and again when the daemon starts; `attn app status %s` shows what exists.",
			name, consumer, verb, name))
		return
	}
	// Between the read above and this write the consumer may have been
	// unregistered — `attn app remove` running beside this one. Reporting success
	// then would answer for a consumer that is gone and publish a fact about it.
	flipped, changed, err := d.store.SetAppBusConsumerEnabled(name, msg.Enabled, time.Now())
	if err != nil {
		d.sendError(conn, fmt.Sprintf("%s app %q: %v", verb, name, err))
		return
	}
	if !flipped {
		d.sendError(conn, fmt.Sprintf(
			"app %q: its bus consumer %s was removed while %s was running, so nothing was changed. "+
				"`attn app status %s` shows what is left.", name, consumer, verb, name))
		return
	}
	if msg.Enabled && changed {
		// Enabling is the way back from an auto-disable, so it clears both streaks
		// that cause one. Without this the app would be disabled again on its very
		// next failure, against a clock it never got to restart.
		d.clearAppStall(name)
		d.clearAppCrashes(name)
	}
	if changed {
		d.publishFact(FactAppEnabledChanged, name, appEnabledChanged{
			Name: name, Consumer: consumer, Enabled: msg.Enabled,
		})
	}
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppSetEnabledResult: &protocol.AppSetEnabledResult{
			Name: name, Consumer: consumer, Enabled: msg.Enabled,
		},
	})
}

// handleAppRemove uninstalls an app: the consumer's delivery loop stops and its
// row goes, then the registry row goes.
//
// The consumer is unregistered through the bus rather than deleted from the
// store, because the row is not the whole of it — a live delivery loop reading a
// registration that vanished underneath it retries that error forever. Bus
// Unregister is the one place that stops the loop first and deletes second.
//
// It works on a half-installed app: a stray consumer with no registry row, or a
// registry row with no consumer, is exactly the state that needs a way out, and
// refusing to clean it would leave an orphaned enabled consumer pinning the
// event log's retention floor against a consumer nobody serves.
func (d *Daemon) handleAppRemove(conn net.Conn, msg *protocol.AppRemoveMessage) {
	name := strings.TrimSpace(msg.Name)
	if err := apps.ValidateName(name); err != nil {
		d.sendError(conn, err.Error())
		return
	}
	if d.store == nil {
		d.sendError(conn, "no database")
		return
	}
	_, appExists, err := d.store.GetApp(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading app %q: %v", name, err))
		return
	}
	consumer := apps.ConsumerName(name)
	_, consumerExists, err := d.store.GetBusConsumer(consumer)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("reading the bus consumer for app %q: %v", name, err))
		return
	}
	if !appExists && !consumerExists {
		d.sendError(conn, d.unknownAppError("remove", name))
		return
	}

	if consumerExists {
		if err := d.unregisterConsumer(consumer); err != nil {
			d.sendError(conn, fmt.Sprintf("removing app %q: stopping its bus consumer %s: %v", name, consumer, err))
			return
		}
	}
	if _, err := d.store.DeleteApp(name); err != nil {
		d.sendError(conn, fmt.Sprintf("removing app %q: %v", name, err))
		return
	}

	versions, err := d.store.CountAppVersions(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting versions of app %q: %v", name, err))
		return
	}
	invocations, err := d.store.CountAppInvocations(name)
	if err != nil {
		d.sendError(conn, fmt.Sprintf("counting invocations of app %q: %v", name, err))
		return
	}
	namespace := apps.Namespace(name)
	d.publishFact(FactAppRemoved, name, appRemoved{Name: name, Consumer: consumer, Namespace: namespace})
	d.sendDocResponse(conn, protocol.Response{
		Ok: true,
		AppRemoveResult: &protocol.AppRemoveResult{
			Name:            name,
			ConsumerRemoved: consumerExists,
			VersionsKept:    versions,
			InvocationsKept: invocations,
			NamespaceKept:   namespace,
		},
	})
}

// unregisterConsumer stops and deletes a durable consumer. It goes through the
// bus when there is one; a daemon assembled without a bus (tests, and a store
// opened for a one-shot command) still has to delete the row, or the uninstall
// would leave the orphan the whole verb exists to remove.
func (d *Daemon) unregisterConsumer(consumer string) error {
	if d.eventBus != nil {
		return d.eventBus.Unregister(consumer)
	}
	return d.store.DeleteBusConsumer(consumer)
}

// appSummary joins a registry row to the two things that are not in it: the
// version it points at, and the consumer carrying its enabled state.
func (d *Daemon) appSummary(row store.App, head int64) (protocol.AppSummary, error) {
	summary := protocol.AppSummary{
		Name:      row.Name,
		CreatedAt: stampForWire(row.CreatedAt),
		UpdatedAt: stampForWire(row.UpdatedAt),
	}
	if row.CurrentVersionID != 0 {
		version, ok, err := d.store.GetAppVersion(row.CurrentVersionID)
		if err != nil {
			return protocol.AppSummary{}, fmt.Errorf("reading version %d of app %q: %w", row.CurrentVersionID, row.Name, err)
		}
		if ok {
			summary.CurrentVersion = &protocol.AppVersionInfo{
				ID:           int(version.ID),
				ContentHash:  version.ContentHash,
				ArtifactPath: version.ArtifactPath,
				CreatedAt:    stampForWire(version.CreatedAt),
			}
			// From the serving version's frozen declaration, not the manifest on
			// disk: after a rollback those differ, and what docks is what serves.
			summary.Views = appViewsForWire(version.Declaration, d.logf)
			summary.Commands = appDeclaredCommands(version.Declaration, d.logf)
		}
	}
	consumer, ok, err := d.store.GetBusConsumer(apps.ConsumerName(row.Name))
	if err != nil {
		return protocol.AppSummary{}, fmt.Errorf("reading the bus consumer for app %q: %w", row.Name, err)
	}
	if ok {
		lag := head - consumer.Cursor
		if lag < 0 {
			lag = 0
		}
		summary.Consumer = &protocol.AppConsumerInfo{
			Name:    consumer.Name,
			Enabled: consumer.Enabled,
			Cursor:  int(consumer.Cursor),
			Lag:     int(lag),
			Filter:  consumer.Filter,
		}
	}
	return summary, nil
}

func (d *Daemon) busHead() (int64, error) {
	_, head, err := d.store.BusBounds()
	return head, err
}

// unknownAppError names what is there instead of what is not. An agent reading
// "no app named x" and nothing else has to go find another command to learn what
// the alternatives are; this answers that in the same breath, and calls out the
// half-installed case rather than letting it read as "never existed".
func (d *Daemon) unknownAppError(verb, name string) string {
	msg := fmt.Sprintf("app %s: no app named %q is registered", verb, name)
	if rows, err := d.store.ListApps(); err == nil {
		if len(rows) == 0 {
			msg += "; no apps are registered"
		} else {
			names := make([]string, 0, len(rows))
			for _, row := range rows {
				names = append(names, row.Name)
			}
			sort.Strings(names)
			msg += "; registered apps: " + strings.Join(names, ", ")
		}
	}
	// A name with history or a stray consumer is a different situation from a
	// name that never existed, and saying which is what tells the reader whether
	// there is anything left to clean up.
	var leftovers []string
	if versions, err := d.store.CountAppVersions(name); err == nil && versions > 0 {
		leftovers = append(leftovers, fmt.Sprintf("%d version(s) of it remain as history", versions))
	}
	if _, ok, err := d.store.GetBusConsumer(apps.ConsumerName(name)); err == nil && ok {
		leftovers = append(leftovers, fmt.Sprintf("its bus consumer %s still exists (`attn app remove %s` deletes it)", apps.ConsumerName(name), name))
	}
	if len(leftovers) > 0 {
		msg += ". " + strings.Join(leftovers, "; ")
	}
	return msg
}

// stampForWire renders a stored timestamp for the protocol. Zero means "not
// recorded", which travels as the empty string rather than year 1.
func stampForWire(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
