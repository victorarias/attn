package present

import (
	"fmt"
	"os"
	"strings"

	"github.com/victorarias/attn/internal/git"
)

// Pin resolves the manifest's frame.base and frame.head git refs to full
// 40-char commit SHAs, so a presentation is pinned to what it actually
// reviewed regardless of later changes to those refs.
func Pin(m *Manifest) (baseSHA, headSHA string, err error) {
	if _, statErr := os.Stat(m.Frame.Repo); statErr != nil {
		return "", "", fmt.Errorf("present: frame.repo %q does not exist: %w", m.Frame.Repo, statErr)
	}

	baseSHA, err = resolveSHA(m.Frame.Repo, m.Frame.Base)
	if err != nil {
		return "", "", fmt.Errorf("present: resolve frame.base %q: %w", m.Frame.Base, err)
	}

	headSHA, err = resolveSHA(m.Frame.Repo, m.Frame.Head)
	if err != nil {
		return "", "", fmt.Errorf("present: resolve frame.head %q: %w", m.Frame.Head, err)
	}

	return baseSHA, headSHA, nil
}

func resolveSHA(repoDir, ref string) (string, error) {
	out, err := git.Output(git.OpMetadata, repoDir, "rev-parse", "--verify", ref+"^{commit}")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}
