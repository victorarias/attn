package daemon

import "github.com/victorarias/attn/internal/garden"

// The signpost the ticket cutover owed and did not leave.
//
// Converting the backlog moved every unbound todo into the garden and left
// in-flight tickets on the retired board to drain themselves
// (garden_backlog.go). They do not all drain: a session that dies stamps its
// ticket crashed (ticket_crash.go), and a crashed ticket sits on a surface the
// garden era gives nobody a reason to open. A user reading the garden sees her
// converted todos and concludes the rest are gone.
//
// So the garden's own surfaces carry the count. It is visibility only — nothing
// here reads, writes, or replants a ticket — and it is driven purely by the
// count, so a garden with nothing stranded says nothing at all.

// countStrandedTickets is the number the notice names: unarchived crashed or
// failed tickets. Behind the home fence like every garden read, because an
// outpost holds no board of its own and must not report one.
//
// It answers 0 on error. The notice is a pointer, not a record: a count that
// cannot be read is worth a log line, never a failed listing or a broken push.
func (d *Daemon) countStrandedTickets() int {
	if d.store == nil {
		return 0
	}
	if err := d.requireHome(garden.Surface); err != nil {
		return 0
	}
	count, err := d.store.CountStrandedTickets()
	if err != nil {
		d.logf("garden: counting tickets stranded on the retired board: %v", err)
		return 0
	}
	return count
}

// strandedTicketsField renders the count for the wire: absent when zero, so a
// healthy garden carries no field at all and every client's "is there a notice"
// test is the same question.
func (d *Daemon) strandedTicketsField() *int {
	count := d.countStrandedTickets()
	if count == 0 {
		return nil
	}
	return &count
}
