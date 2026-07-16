package daemon

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/notebook"
	"github.com/victorarias/attn/internal/protocol"
)

// ticketArtifacts enumerates the filesystem-canonical artifact index for one
// ticket. Only direct, regular, visible files are current artifacts.
func (d *Daemon) ticketArtifacts(ticketID string) ([]protocol.TicketArtifact, error) {
	root, err := d.notebookRoot()
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(root) == "" {
		return nil, fmt.Errorf("notebook is not configured")
	}
	dir := notebook.TicketArtifactsDir(root, ticketID)
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return []protocol.TicketArtifact{}, nil
	}
	if err != nil {
		return nil, err
	}
	artifacts := make([]protocol.TicketArtifact, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") || entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		info, infoErr := entry.Info()
		if infoErr != nil || !info.Mode().IsRegular() {
			continue
		}
		artifacts = append(artifacts, protocol.TicketArtifact{
			Filename:     name,
			NotebookPath: filepath.ToSlash(filepath.Join("tickets", ticketID, name)),
			Path:         filepath.Join(dir, name),
		})
	}
	sort.Slice(artifacts, func(i, j int) bool { return artifacts[i].Filename < artifacts[j].Filename })
	return artifacts, nil
}
