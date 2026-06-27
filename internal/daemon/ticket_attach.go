package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/store"
)

// handleTicketAttach hands a file to the calling agent's bound ticket (slice 4e).
// Like ticket status, the session is the assignee, so the daemon resolves the
// ticket from the session rather than trusting a caller-supplied id — an agent
// attaches only to its own active ticket. The file is copied into the ticket's
// store directory (.attn/tickets/<id>/) and recorded with the agent as author, so
// the handover reads as self-attached on the activity thread; the chief is then
// notified and the board refreshed (the same fan-out as the agent's status path).
func (d *Daemon) handleTicketAttach(conn net.Conn, msg *protocol.TicketAttachMessage) {
	sourceSessionID := strings.TrimSpace(msg.SourceSessionID)
	if sourceSessionID == "" {
		d.sendError(conn, "ticket attach: source_session_id is required")
		return
	}
	sourcePath := strings.TrimSpace(msg.SourcePath)
	if sourcePath == "" {
		d.sendError(conn, "ticket attach: source_path is required")
		return
	}
	// The display name the user sees; fall back to the source basename. Basename
	// here also neutralizes any path in the caller-supplied filename.
	filename := filepath.Base(strings.TrimSpace(msg.Filename))
	if filename == "" || filename == "." || filename == ".." || filename == string(filepath.Separator) {
		filename = filepath.Base(sourcePath)
	}

	ticket, err := d.store.ActiveTicketForSession(sourceSessionID)
	if err != nil {
		d.sendError(conn, "ticket attach: "+err.Error())
		return
	}
	if ticket == nil {
		d.sendError(conn, "ticket attach: no active ticket bound to this session")
		return
	}

	dest, err := d.copyTicketAttachment(ticket.ID, sourcePath, filename)
	if err != nil {
		d.sendError(conn, "ticket attach: "+err.Error())
		return
	}

	note := strings.TrimSpace(protocol.Deref(msg.Note))
	att, err := d.store.AddTicketAttachment(store.TicketAttachment{
		TicketID: ticket.ID,
		Filename: filename,
		Path:     dest,
		Note:     note,
	}, sourceSessionID, time.Now())
	if err != nil {
		d.sendError(conn, "ticket attach: "+err.Error())
		return
	}

	_ = json.NewEncoder(conn).Encode(protocol.Response{
		Ok: true,
		TicketAttachResult: &protocol.TicketAttachResult{
			TicketID: ticket.ID,
			Filename: att.Filename,
		},
	})
	// The agent attached to its own ticket; notify the other observers (the chief)
	// and refresh the app's board, mirroring the status forward channel.
	d.notifyTicketObservers(ticket.ID)
	d.broadcastTicketsUpdated()
}

// copyTicketAttachment copies the file at sourcePath into the ticket's store
// directory (.attn/tickets/<id>/), returning the absolute destination path. The
// on-disk name is deduped so two attachments sharing a basename never clobber each
// other; the original filename is what the ticket displays. The directory lives
// under .attn/, written with direct filesystem I/O (not the notebook.Store APIs).
func (d *Daemon) copyTicketAttachment(ticketID, sourcePath, filename string) (string, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", fmt.Errorf("read source file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%q is a directory, not a file", sourcePath)
	}

	root, err := d.notebookRoot()
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(root) == "" {
		return "", errors.New("notebook is not configured")
	}

	dir := notebook.TicketAttachmentsDir(root, ticketID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return copyIntoUniqueAttachment(dir, filename, sourcePath)
}

// uniqueAttachmentPath returns a path in dir for filename that does not collide
// with an existing file, suffixing the base name (report.md -> report-2.md) until
// a free name is found.
func uniqueAttachmentPath(dir, filename string) string {
	candidate := filepath.Join(dir, filename)
	if !fileExists(candidate) {
		return candidate
	}
	ext := filepath.Ext(filename)
	base := strings.TrimSuffix(filename, ext)
	for i := 2; ; i++ {
		candidate = filepath.Join(dir, fmt.Sprintf("%s-%d%s", base, i, ext))
		if !fileExists(candidate) {
			return candidate
		}
	}
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// maxAttachmentNameAttempts bounds the dedup retry loop. A collision only advances
// the suffix (report.md -> report-2.md ...), so the loop converges in a step or two
// even under a same-name race; the cap is a backstop against a pathological spin.
const maxAttachmentNameAttempts = 10_000

// copyIntoUniqueAttachment copies src into dir under filename, deduping the on-disk
// name so two attachments sharing a basename never clobber each other. Name
// selection and the exclusive create are one bounded loop because O_EXCL is the
// real authority: if another attach claims the chosen name between the existence
// check and the create (a TOCTOU race), we advance to the next name instead of
// surfacing a raw EEXIST. A mid-copy failure removes the partial file it just
// created, so a failed attach never leaves an untracked orphan in the ticket store.
func copyIntoUniqueAttachment(dir, filename, src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()

	for attempt := 0; attempt < maxAttachmentNameAttempts; attempt++ {
		dest := uniqueAttachmentPath(dir, filename)
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, os.ErrExist) {
			continue // lost the name race; re-pick and retry
		}
		if err != nil {
			return "", err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			os.Remove(dest)
			return "", err
		}
		if err := out.Close(); err != nil {
			os.Remove(dest)
			return "", err
		}
		return dest, nil
	}
	return "", fmt.Errorf("could not find a free attachment name for %q", filename)
}
