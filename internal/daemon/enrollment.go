package daemon

import (
	"github.com/victorarias/attn/internal/enrollment"
)

// The daemon's whole view of enrollment. Everything that needs to know whether
// this daemon is a home or an outpost — health, initial_state, and the fence
// that garden and crew surfaces call — goes through these two methods; nothing
// reads the record ad hoc.
//
// The record is re-read on every ask rather than cached at startup, because
// `attn enrollment leave` and a home's sync both rewrite it under a live
// daemon, and a cached answer would keep refusing (or keep allowing) until the
// next restart. It is a file of a few dozen bytes on paths nobody polls.

func (d *Daemon) enrollmentStatus() (enrollment.Status, error) {
	return enrollment.Load(d.dataRoot)
}

// requireHome is the fence: home-level state — the garden, the crew — may live
// only on a home daemon. surface names what is being refused, in the words a
// reader would use ("the garden", "the crew roster"). An unreadable record
// fails closed: attn cannot claim to be a home it cannot prove.
func (d *Daemon) requireHome(surface string) error {
	status, err := d.enrollmentStatus()
	if err != nil {
		d.logf("enrollment: cannot read the record in %s: %v", d.dataRoot, err)
	}
	return status.RequireHome(surface)
}

// homeDaemonIDForEnrollment is what the hub presents when it syncs a remote:
// the id of the home this daemon is. Enrolling a remote says "your garden and
// crew live here", so a daemon that is itself an outpost may not do it — it
// passes the fence first and enrolls nobody when the fence refuses.
func (d *Daemon) homeDaemonIDForEnrollment() string {
	if err := d.requireHome("enrolling another daemon as an outpost"); err != nil {
		d.logf("enrollment: %v", err)
		return ""
	}
	return d.daemonInstanceID
}

// ensureEnrollment resolves enrollment at startup and writes the record on a
// fresh install: a daemon nobody enrolled is its own home, and the record says
// so rather than leaving the question open until someone asks.
func (d *Daemon) ensureEnrollment() error {
	status, err := enrollment.Ensure(d.dataRoot, d.daemonInstanceID)
	if err != nil {
		return err
	}
	d.logf("enrollment: %s (daemon %s)", status.Describe(), status.DaemonID)
	return nil
}
