package daemon

import (
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/hooks"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	pluginInstructionKindWorkspace = "workspace"
	pluginInstructionKindChief     = "chief"
)

type pluginLaunchInstructions struct {
	Kind            string `json:"kind"`
	Content         string `json:"content"`
	WorkspaceID     string `json:"workspace_id,omitempty"`
	ContextPath     string `json:"context_path,omitempty"`
	ContextRevision int    `json:"context_revision,omitempty"`
	NotebookRoot    string `json:"notebook_root,omitempty"`
}

// preparePluginLaunchInstructions composes the same attn-owned launch guidance
// used by built-in agents. The rollback removes only a checkout that this call
// created; an existing or locally modified checkout is never removed.
func (d *Daemon) preparePluginLaunchInstructions(sessionID, workspaceID string, isChief bool) (*pluginLaunchInstructions, func(), error) {
	rollback := func() {}
	if isChief {
		root, _, err := d.ensureNotebookScaffold()
		if err != nil {
			return nil, rollback, fmt.Errorf("prepare chief notebook: %w", err)
		}
		if strings.TrimSpace(root) == "" {
			return nil, rollback, fmt.Errorf("prepare chief guidance: notebook root is empty")
		}
		return &pluginLaunchInstructions{
			Kind: pluginInstructionKindChief,
			Content: hooks.Launch{
				NotebookRoot: root,
				Garden:       d.gardenPrimeForLaunch(sessionID),
				Crew:         d.crewPrimeForLaunch(sessionID),
			}.Instructions(),
			WorkspaceID:  workspaceID,
			NotebookRoot: root,
		}, rollback, nil
	}

	contextPath, metadataPath := workspaceContextCheckoutPaths(d.dataRoot, sessionID)
	_, contextErr := os.Stat(contextPath)
	_, metadataErr := os.Stat(metadataPath)
	created := os.IsNotExist(contextErr) && os.IsNotExist(metadataErr)
	session := &protocol.Session{ID: sessionID, WorkspaceID: workspaceID}
	result, err := d.checkoutWorkspaceContextForSession(session, false)
	if err != nil {
		if created {
			_ = os.RemoveAll(workspaceContextCheckoutDir(d.dataRoot, sessionID))
		}
		return nil, rollback, fmt.Errorf("prepare workspace context: %w", err)
	}
	if created {
		rollback = func() { _ = os.RemoveAll(workspaceContextCheckoutDir(d.dataRoot, sessionID)) }
	}
	return &pluginLaunchInstructions{
		Kind: pluginInstructionKindWorkspace,
		Content: hooks.Launch{
			WorkspaceContextPath: result.Path,
			InjectWorkflow:       parseBooleanSetting(d.store.GetSetting(SettingWorkflowsEnabled)),
			Garden:               d.gardenPrimeForLaunch(sessionID),
			Crew:                 d.crewPrimeForLaunch(sessionID),
		}.Instructions(),
		WorkspaceID:     workspaceID,
		ContextPath:     result.Path,
		ContextRevision: result.CanonicalRevision,
	}, rollback, nil
}

// gardenPrimeForLaunch is what a launching agent is primed with, or nil when
// this daemon has no garden to prime from — an outpost, where every seed
// command refuses, must not hand its agents a loop they cannot run.
func (d *Daemon) gardenPrimeForLaunch(sessionID string) *hooks.GardenPrime {
	prime, err := d.gardenPrime(sessionID)
	if err != nil {
		return nil
	}
	return prime
}

// crewPrimeForLaunch is the crew block for a plugin-driver launch, the same one
// a built-in driver's wrapper fetches over `crew_prime`. Empty for a session
// that is nobody.
func (d *Daemon) crewPrimeForLaunch(sessionID string) string {
	_, block, _, err := d.crewPrimeForSession(sessionID)
	if err != nil {
		d.logf("crew: refusing launch priming for session %s: %v", sessionID, err)
		return ""
	}
	return block
}
