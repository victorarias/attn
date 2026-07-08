package daemon

import (
	"encoding/json"
	"net"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// handleTicketCreate mints a standalone backlog ticket — unbound (no assignee, no
// session), starting in todo. Unlike delegation, nothing is dispatched: this is the
// user capturing work to do later. The calling session authors the created event but
// is not the assignee, so the ticket sits in the backlog until someone picks it up.
// An explicit id pins the slug — a hard fail on a malformed or taken id, so the user
// learns immediately and can rename it. Without one, the slug is derived from the
// title and auto-suffixed on collision, exactly like delegation. There are no
// participants to notify, so the only side effect is the board re-broadcast.
func (d *Daemon) handleTicketCreate(conn net.Conn, msg *protocol.TicketCreateMessage) {
	author := strings.TrimSpace(msg.SourceSessionID)
	if author == "" {
		d.sendError(conn, "ticket new: source_session_id is required")
		return
	}
	title := strings.TrimSpace(msg.Title)
	if title == "" {
		d.sendError(conn, "ticket new: title is required")
		return
	}
	desc := ""
	if msg.Description != nil {
		desc = strings.TrimSpace(*msg.Description)
	}
	explicitID := ""
	if msg.ID != nil {
		explicitID = strings.TrimSpace(*msg.ID)
	}
	now := time.Now()

	var created *store.Ticket
	if explicitID != "" {
		// A user-chosen id is pinned, not auto-suffixed: surface ValidateTicketID and
		// the ErrTicketIDTaken guidance verbatim so the user fixes or renames it.
		t, err := d.store.CreateTicket(store.Ticket{
			ID:          explicitID,
			Title:       title,
			Description: desc,
			Status:      store.TicketStatusTodo,
		}, author, now)
		if err != nil {
			d.sendError(conn, "ticket new: "+err.Error())
			return
		}
		created = t
	} else {
		t, err := d.createTicketWithUniqueSlug(store.Ticket{
			Title:       title,
			Description: desc,
			Status:      store.TicketStatusTodo,
		}, ticketSlug(title), author, "", now)
		if err != nil {
			d.sendError(conn, "ticket new: "+err.Error())
			return
		}
		created = t
	}

	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketCreateResult: &protocol.TicketCreateResult{
			TicketID: created.ID,
			Status:   protocol.TicketStatus(created.Status),
			Title:    created.Title,
		},
	})
	// A new backlog card appeared; refresh the app's board. The ticket is unbound
	// (no assignee/participants), so there is nobody to notify.
	d.broadcastTicketsUpdated()
}
