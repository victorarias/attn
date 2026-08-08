# Daemon Children Are Reaped From Files, Not From SQLite

Date: 2026-08-08

## Decision

Every long-lived process the daemon spawns writes a **durable record as its own
file** at spawn time — one JSON file per process, in a registry directory under
the data dir — and is reaped by iterating that directory. The records do not go
in the store, and the reaper never opens the database.

| child | registry | reaped by |
|---|---|---|
| pty-worker | `<dataDir>/workers/<daemon-instance>/registry` | `internal/ptyworker` |
| conversation host | `<dataDir>/hosts/registry` | `internal/procreap`, 3s grace |
| plugin runtime driver | `<dataDir>/plugin-runtime/registry` | `internal/procreap`, 3s grace |

`internal/procreap` owns the record format and the signal-based reap loop for
the last two. Workers keep their own reaper (`internal/ptyworker/reap.go`)
because they are a different animal: they *deliberately* outlive a daemon
restart to be re-adopted, and they expose an authenticated control socket, so
their reap asks the worker to remove itself and only falls back to signalling a
positively-identified pid. What all three share is the thing this decision is
about — the record is a file on disk, and finding it needs no database and no
daemon.

The store stays authoritative for everything a *live* daemon knows about these
processes. A registry is not a second copy of that; it carries only what a
stranger needs to shut something down safely — for procreap, the pid, pgid,
command, and the birth-time stamp that proves the pid is still the process we
recorded.

## Why

**The reaper's job starts where the daemon's guarantees ended.** Hosts and
drivers never outlive their daemon on purpose: shutdown kills them all, and a
host self-exits on stdin EOF. A live entry in those registries therefore means
the daemon died *without* running shutdown — SIGKILL, a crash, a power cut. That
is precisely the moment the database is least trustworthy, so recovery state
must not live inside the artifact whose failure it recovers from. Workers reach
the same conclusion from the other end: they are built to survive a daemon
restart, so the record has to be readable while no daemon exists at all.

**Opening the store is a write.** `store.OpenDB` (`internal/store/sqlite.go`)
creates the base schema and runs the full migration chain, taking a
pre-migration backup on the way. Reading three pids out of the database would
mean migrating it — and then deleting the directory it lives in, since the
caller is `attn profile clean`. Opening `mode=ro` avoids the migration but not
the deeper problem: a WAL left behind by a process that died mid-write needs
write access to recover, so read-only is the open most likely to fail on exactly
the crashed daemon being cleaned up after.

**There is no query.** Reaping is "iterate everything and signal it": no
filters, no joins, no ordering, a handful of records read once. Indexes and
transactions buy nothing here, and the write side gets worse — the record has to
land immediately after `cmd.Start()`, and an atomic tmp+rename does that without
contending with a daemon that is mid-transaction.

**It must work with no daemon at all.** `attn profile clean` runs against a data
dir whose daemon is already gone, possibly written by a different build. A
directory of small self-describing files needs no schema agreement and no
process to serve it.

## Rules this imposes

1. **Reap before removing a data dir.** Deleting a registry strands whatever it
   described — those processes are reparented to init and findable through
   nothing else.
2. **A record is written at spawn and removed by the waiter**, not on a timer
   and not at shutdown. A record that outlives its process is normal and reaps
   as `ReapAlreadyGone`; a process that outlives its record is unreachable.
3. **The identity stamp is the safety property, not a detail.** A recorded pid
   is signalled only while its birth time still matches, because a pid alone is
   a name the kernel reuses. Resolutions and their measured reuse floors live in
   `procstart_darwin.go` and `procstart_linux.go`; there is deliberately no
   generic fallback, so an unsupported platform fails to compile rather than
   reaping on a stamp nobody has checked.
4. **No outcome is silent.** An undecodable record reports `ReapUnreadable`
   rather than being skipped — silence there reads as "nothing was registered",
   which is the opposite of the truth. Same for a survivor and for a pid the
   reaper refused to identify: both name the pid so a human can finish the job.
5. **A new daemon child joins a `procreap` registry** rather than growing a
   third scheme. Prefer an authenticated shutdown path like the worker's when
   the child already has one; signalling a stamped pid is the fallback for
   children that do not. Either way, if a child cannot be reaped from disk
   alone, that is the bug.

## When this flips

The argument holds because the reaper only ever iterates. It stops holding the
moment the reaper has to *answer* something — reap only one workspace's
children, show strays in the UI, attribute a driver back to the session that
asked for it. Those are queries over attributed state, which is what the store
is for; at that point the files become a cache of it and should move rather than
grow an index over a directory.

The general rule, worth keeping even if the mechanism changes: **state that
exists to clean up after a crash lives outside the thing that crashes.** Same
reason a pid file is not a row.
