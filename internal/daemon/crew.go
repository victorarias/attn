package daemon

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/crew"
	"github.com/victorarias/attn/internal/docstore"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// The crew's daemon half: the registry collection, the binding a session
// launches with, and the single-holder rule over it.
//
// The daemon is where a binding is decided. Registration carries the member
// name, the daemon resolves it against the registry and writes the claim
// against the revision it read — so two launches racing for the same member
// produce one binding and one named refusal, not two trellises.
//
// Every crew verb passes the enrollment fence first: the crew has exactly one
// owner — the home daemon — and an outpost holds no part of it.
//
// Whether a stored binding still binds is judged at read, the same liveness
// rule the garden's Tender.Holds uses: a binding naming a session the daemon
// no longer knows has released on its own. Session teardown also clears the
// record, so a roster read shows the truth rather than a stale day.

// ensureCrewCollections declares the registry at startup. Like the garden's,
// it runs on every daemon, home or outpost, because a declaration is schema
// and not state: an outpost's roster stays empty (the fence refuses every
// write), and declaring it anyway means `attn enrollment leave` makes a daemon
// a working home immediately rather than at its next restart.
func (d *Daemon) ensureCrewCollections() {
	if d.store == nil {
		return
	}
	schema := crew.MembersSchema()
	redeclared, err := d.store.DefineDocumentCollection(schema, time.Now())
	if err != nil {
		d.logf("crew: declaring %s/%s: %v", schema.Namespace, schema.Collection, err)
		return
	}
	if redeclared {
		d.publishCollectionRedeclared(schema.Namespace, schema.Collection)
	}
}

// importCrewHomes registers every home under `<data-dir>/crew/` that the
// registry does not know yet. Files are canonical: the import records where a
// home lives, never what it says, and an existing registry record is never
// touched — the write is create-only, so re-running at every startup costs
// nothing and picks up a home the user added by hand since the last one.
func (d *Daemon) importCrewHomes() {
	if d.store == nil {
		return
	}
	if err := d.requireHome(crew.Surface); err != nil {
		// An outpost imports nothing; not an error — startup is not a crew ask.
		return
	}
	members, err := crew.ScanHomes(filepath.Join(d.dataRoot, crew.HomesDirName), d.logf)
	if err != nil {
		d.logf("crew: %v", err)
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		d.logf("crew: importing homes: %v", err)
		return
	}
	for _, member := range members {
		if err := d.writeCrewMember(*schema, member, docstore.ExpectAbsent); err != nil {
			if docstore.IsConflict(err) {
				continue // already registered; the record is authoritative
			}
			d.logf("crew: importing %s: %v", member.ID, err)
			continue
		}
		d.publishFact(FactCrewRegistered, member.ID, nil)
		d.logf("crew: imported member %s from %s", member.ID, member.HomeDir)
	}
}

// crewCollection reads the members declaration, which carries the minted table
// name every query compiles against.
func (d *Daemon) crewCollection() (*docstore.CollectionSchema, error) {
	if d.store == nil {
		return nil, errors.New("no database")
	}
	return d.collectionFor(crew.Namespace, crew.CollectionMembers)
}

// writeCrewMember stores one member record against the revision the caller
// read (docstore.ExpectAbsent for a create), committing the document with the
// docstore's own change fact so live queries on the roster wake like any other
// write.
func (d *Daemon) writeCrewMember(schema docstore.CollectionSchema, member crew.Member, expected int64) error {
	body, err := member.Encode()
	if err != nil {
		return err
	}
	fact := documentChangedFact(crew.Namespace, crew.CollectionMembers, member.ID, false)
	written, err := d.store.CommitDocumentWrite(store.DocumentWrite{
		Schema: schema, ID: member.ID, Body: body, Expected: &expected,
	}, fact, time.Now())
	if err != nil {
		return err
	}
	d.announceCommittedWrite(fact, written.Seq)
	return nil
}

// readCrewMembers reads the whole roster. The crew is people the user named
// one by one — three today — so docstore.MaxLimit is not a bound anything
// real approaches; a roster past it would be a different product.
//
// Every crew read is this one query, so one receipt covers them all: measured
// 2026-08-14 at a three-member roster, 25µs on an M5. That is what a session
// broadcast pays, and what a garden action pays to resolve its tender.
func (d *Daemon) readCrewMembers() ([]crew.Member, map[string]docstore.Document, error) {
	read, _, err := d.runDocQuery(docstore.Query{
		Namespace:  crew.Namespace,
		Collection: crew.CollectionMembers,
		Sort:       &docstore.Sort{Field: docstore.FieldCreatedAt, Desc: false},
		Limit:      docstore.MaxLimit,
	})
	if err != nil {
		return nil, nil, err
	}
	members := make([]crew.Member, 0, len(read.Documents))
	docs := make(map[string]docstore.Document, len(read.Documents))
	for _, doc := range read.Documents {
		member, err := crew.Decode(doc.Body)
		if err != nil {
			// One unreadable record must not blank the roster; name it and go on.
			d.logf("crew: member %s has an unreadable record: %v", doc.ID, err)
			continue
		}
		members = append(members, member)
		docs[member.ID] = doc
	}
	return members, docs, nil
}

// updateCrewMember reads a member, lets mutate decide its new body, and writes
// it against the revision it was read at — remaking the decision when the record
// moves underneath, because a conflict is not a refusal. mutate returns false to
// abandon the write without an error: nothing needed changing, which a caller
// racing the record's own owner must be able to say. Three attempts is a
// tripwire; two writers contending is one retry.
func (d *Daemon) updateCrewMember(memberID string, mutate func(*crew.Member) (bool, error)) (crew.Member, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return crew.Member{}, err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return crew.Member{}, err
	}
	const attempts = 3
	for range attempts {
		members, docs, err := d.readCrewMembers()
		if err != nil {
			return crew.Member{}, err
		}
		member, ok := crew.Resolve(memberID, members)
		if !ok {
			return crew.Member{}, fmt.Errorf("no crew member %q is registered", memberID)
		}
		write, err := mutate(&member)
		if err != nil || !write {
			return member, err
		}
		err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
		if err == nil {
			return member, nil
		}
		if !docstore.IsConflict(err) {
			return crew.Member{}, err
		}
	}
	return crew.Member{}, fmt.Errorf("the registry record for %q was rewritten under all %d attempts to update it; try again", memberID, attempts)
}

// crewBindingLive reports whether a stored binding still binds: a non-empty
// session the daemon still knows. The read-time judgment, shared by the
// single-holder claim, the roster read, and session decoration.
func (d *Daemon) crewBindingLive(member crew.Member) bool {
	return member.BindingSession != "" && d.sessionExists(member.BindingSession)
}

// claimCrewBinding stamps sessionID as memberName's active binding — the
// identity mechanism: the session is the member because this claim succeeded
// at its launch. It refuses an unregistered name, and refuses a member whose
// current day is still live, because two agents with the same identity never
// run at once. Re-claiming the binding a session already holds is idempotent,
// so a client re-announcing a live session keeps its identity, and claiming a
// second member for one session moves it rather than doubling it.
//
// A conflict is not a refusal — the record moved between the read and the
// write, so the decision is remade against what is there now. Three attempts
// is a tripwire — two launches contending is one retry.
func (d *Daemon) claimCrewBinding(memberName, sessionID string) (string, error) {
	if err := d.requireHome(crew.Surface); err != nil {
		return "", err
	}
	schema, err := d.crewCollection()
	if err != nil {
		return "", err
	}
	const attempts = 3
	for range attempts {
		members, docs, err := d.readCrewMembers()
		if err != nil {
			return "", err
		}
		member, ok := crew.Resolve(memberName, members)
		if !ok {
			return "", fmt.Errorf("no crew member %q is registered; `attn crew list` names the roster", memberName)
		}
		if member.BindingSession == sessionID {
			return member.ID, nil
		}
		if d.crewBindingLive(member) {
			return "", fmt.Errorf("%s is already awake in session %s; two agents with the same identity never run at once — wait for that day to end, or wake another member",
				member.ID, shortSessionID(member.BindingSession))
		}
		// Past every refusal, so the claim is going to land: drop any other name
		// this session already answered to. A refused claim is not reached here,
		// and leaves the session the member it already was.
		d.releaseCrewBindingsExcept(*schema, members, docs, member.ID, sessionID)
		member.BindingSession = sessionID
		err = d.writeCrewMember(*schema, member, docs[member.ID].Rev)
		if err == nil {
			d.publishFact(FactCrewBound, member.ID, nil)
			d.logf("crew: session %s bound as %s", sessionID, member.ID)
			return member.ID, nil
		}
		if !docstore.IsConflict(err) {
			return "", err
		}
	}
	return "", fmt.Errorf("the registry record for %q was rewritten under all %d attempts to bind it; try again", memberName, attempts)
}

// releaseCrewBindingIfSession clears any binding naming sessionID. Called from
// every session-removal path, mirroring the chief-of-staff role clear; a path
// that misses it costs nothing but a stale record the liveness judgment
// already ignores. It is quiet by design — most sessions were never bound, and
// on an outpost the roster is empty — so it reads before it fences.
func (d *Daemon) releaseCrewBindingIfSession(sessionID string) {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	schema, err := d.crewCollection()
	if err != nil {
		return
	}
	members, docs, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster to release %s: %v", sessionID, err)
		}
		return
	}
	d.releaseCrewBindingsExcept(*schema, members, docs, "", sessionID)
}

// releaseCrewBindingsExcept clears every binding naming sessionID other than
// keepID's, against a roster the caller already read. One session answers to
// one name: a session that becomes somebody drops whoever it was first.
func (d *Daemon) releaseCrewBindingsExcept(schema docstore.CollectionSchema, members []crew.Member, docs map[string]docstore.Document, keepID, sessionID string) {
	for _, member := range members {
		if member.BindingSession != sessionID || member.ID == keepID {
			continue
		}
		member.BindingSession = ""
		if err := d.writeCrewMember(schema, member, docs[member.ID].Rev); err != nil {
			d.logf("crew: releasing %s's binding for session %s: %v", member.ID, sessionID, err)
			continue
		}
		d.publishFact(FactCrewReleased, member.ID, nil)
		d.logf("crew: session %s released %s's binding", sessionID, member.ID)
	}
}

// crewMembersBySession maps live bindings for one broadcast, so decorating a
// session list costs one roster read rather than one per session. Empty —
// never an error — when the roster is empty or unreadable: decoration must not
// fail a broadcast. Receipt for the read on readCrewMembers.
func (d *Daemon) crewMembersBySession() map[string]string {
	if d.store == nil {
		return nil
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for broadcast: %v", err)
		}
		return nil
	}
	var out map[string]string
	for _, member := range members {
		if !d.crewBindingLive(member) {
			continue
		}
		if out == nil {
			out = make(map[string]string)
		}
		out[member.BindingSession] = member.ID
	}
	return out
}

// crewMemberBoundTo names the member a session is living as, or "" when it is
// nobody. Read the roster rather than the session record: CrewMember is a
// broadcast decoration, so it is nil on everything d.store.Get returns.
func (d *Daemon) crewMemberBoundTo(sessionID string) string {
	if d.store == nil || strings.TrimSpace(sessionID) == "" {
		return ""
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for session %s: %v", sessionID, err)
		}
		return ""
	}
	for _, member := range members {
		if member.BindingSession == sessionID && d.crewBindingLive(member) {
			return member.ID
		}
	}
	return ""
}

// decorateCrewMember marks the session living a member's current day. Mirrors
// decorateChiefOfStaffWithSessionID: set only when bound, cleared otherwise so
// it round-trips as an omitted field.
func (d *Daemon) decorateCrewMember(session *protocol.Session, membersBySession map[string]string) {
	if session == nil {
		return
	}
	if member := membersBySession[session.ID]; member != "" {
		session.CrewMember = protocol.Ptr(member)
		return
	}
	session.CrewMember = nil
}

// resolveTenderMember snaps a garden actor's free-string member name to its
// registered id where one exists — `Tender.Is` then compares real addresses —
// and fills the name from the session's own binding when the caller named
// nobody, because a bound session IS its member. An unregistered name passes
// through untouched: the registry never becomes a requirement to tend.
func (d *Daemon) resolveTenderMember(memberName, sessionID string) string {
	memberName = strings.TrimSpace(memberName)
	if memberName == "" {
		return d.crewMemberBoundTo(sessionID)
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster to resolve %q: %v", memberName, err)
		}
		return memberName
	}
	if member, ok := crew.Resolve(memberName, members); ok {
		return member.ID
	}
	return memberName
}

// IPC handlers.

func (d *Daemon) sendCrewError(conn net.Conn, verb string, err error) {
	d.sendError(conn, fmt.Sprintf("crew %s: %v", verb, err))
}

// crewMemberWire is the one projection of a member onto the wire, shared by the
// roster read, the set result, and the sidebar's push.
func (d *Daemon) crewMemberWire(member crew.Member) protocol.CrewMember {
	wire := protocol.CrewMember{
		ID:          member.ID,
		CharterPath: member.CharterPath,
		HomeDir:     member.HomeDir,
	}
	if member.CWD != "" {
		wire.Cwd = protocol.Ptr(member.CWD)
	}
	wire.AwarenessDirs = append([]string{}, member.AwarenessDirs...)
	// The wire carries only a binding that still binds: whether a session is
	// live is judged here, at read, so a caller never has to.
	if d.crewBindingLive(member) {
		wire.BindingSession = protocol.Ptr(member.BindingSession)
	}
	return wire
}

// crewForBroadcast is the payload of both initial_state and crew_updated. An
// outpost has no roster to push; not an error here, because initial_state is
// not a crew command and a refusal in a snapshot would be noise on every
// connect.
func (d *Daemon) crewForBroadcast() []protocol.CrewMember {
	if d.store == nil {
		return nil
	}
	if err := d.requireHome(crew.Surface); err != nil {
		return nil
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		if !docstore.IsUndeclaredCollection(err) {
			d.logf("crew: reading roster for broadcast: %v", err)
		}
		return nil
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		out = append(out, d.crewMemberWire(member))
	}
	return out
}

// projectCrewRoster re-pushes the roster to every client. The sidebar draws
// every member, awake or asleep, so there is nothing smaller to send than the
// whole list.
func (d *Daemon) projectCrewRoster() {
	if d.store == nil {
		return
	}
	d.projectSnapshot(snapshotCrew, func() {
		members := d.crewForBroadcast()
		if d.wsHub == nil {
			return
		}
		d.broadcastMessage(&protocol.CrewUpdatedMessage{
			Event:   protocol.EventCrewUpdated,
			Members: members,
		})
	})
}

func (d *Daemon) handleCrewList(conn net.Conn, _ *protocol.CrewListMessage) {
	if err := d.requireHome(crew.Surface); err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	members, _, err := d.readCrewMembers()
	if err != nil {
		d.sendCrewError(conn, "list", err)
		return
	}
	out := make([]protocol.CrewMember, 0, len(members))
	for _, member := range members {
		out = append(out, d.crewMemberWire(member))
	}
	d.sendGardenResponse(conn, protocol.Response{
		Ok:             true,
		CrewListResult: &protocol.CrewListResult{Members: out},
	})
}
