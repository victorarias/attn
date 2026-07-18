package main

import (
	"crypto/sha256"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/victorarias/attn/internal/client"
	attngit "github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

const (
	planAuthorityAuto       = "auto"
	planAuthorityRepository = "repository"
	planAuthorityNotebook   = "notebook"
)

type ticketAttachPlanArgs struct {
	File      string
	Scope     string
	Authority string
	Ticket    string
	State     string
	Comment   string
	Session   string
	JSON      bool
}

type planAttachment struct {
	Authority       string
	SourcePath      string
	RepoRoot        string
	ScopePath       string
	Scope           string
	RepoPath        string
	Remote          string
	Branch          string
	IntroducedIn    string
	ReferenceName   string
	ConventionFound bool
}

type ticketAttachPlanResult struct {
	Authority                 string                       `json:"authority"`
	CanonicalPath             string                       `json:"canonical_path"`
	SourceRetired             bool                         `json:"source_retired"`
	LegacyNotebookCopyRetired bool                         `json:"legacy_notebook_copy_retired"`
	RepositoryPath            string                       `json:"repository_path,omitempty"`
	Branch                    string                       `json:"branch,omitempty"`
	IntroducedIn              string                       `json:"introduced_in,omitempty"`
	Attachment                *protocol.TicketAttachResult `json:"attachment"`
}

func parseTicketAttachPlanArgs(args []string) (ticketAttachPlanArgs, error) {
	var result ticketAttachPlanArgs
	fs := flag.NewFlagSet("ticket attach-plan", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	file := fs.String("file", "", "path to one Markdown plan or design")
	scope := fs.String("scope", "", "affected repository or monorepo component scope")
	authority := fs.String("authority", planAuthorityAuto, "canonical authority: auto, repository, or notebook")
	ticketID := fs.String("ticket", "", "attach to this ticket instead of the session's bound ticket")
	state := fs.String("state", "", "optional resulting work state")
	comment := fs.String("comment", "", "optional context recorded with the attachment")
	session := fs.String("session", "", "session id (defaults to ATTN_SESSION_ID)")
	jsonOutput := fs.Bool("json", false, "print the result as JSON")
	if err := fs.Parse(args); err != nil {
		return result, err
	}
	if fs.NArg() != 0 {
		return result, fmt.Errorf("unexpected arguments: %v", fs.Args())
	}
	result.File = strings.TrimSpace(*file)
	if result.File == "" {
		return result, errors.New("--file is required")
	}
	result.Scope = strings.TrimSpace(*scope)
	result.Authority = strings.ToLower(strings.TrimSpace(*authority))
	switch result.Authority {
	case planAuthorityAuto, planAuthorityRepository, planAuthorityNotebook:
	default:
		return result, fmt.Errorf("unknown --authority %q (want auto, repository, or notebook)", result.Authority)
	}
	result.Ticket = strings.TrimSpace(*ticketID)
	result.State = strings.TrimSpace(*state)
	result.Comment = strings.TrimSpace(*comment)
	result.Session = *session
	result.JSON = *jsonOutput
	return result, nil
}

func inspectPlanAttachment(path, scope, requestedAuthority string) (planAttachment, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return planAttachment{}, err
	}
	absPath = attngit.CanonicalizePath(absPath)
	info, err := os.Lstat(absPath)
	if err != nil {
		return planAttachment{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return planAttachment{}, fmt.Errorf("%q is not a regular file", absPath)
	}
	if !strings.EqualFold(filepath.Ext(absPath), ".md") {
		return planAttachment{}, errors.New("attach-plan accepts one Markdown (.md) plan or design; use ticket attach for other artifacts")
	}

	decision := planAttachment{SourcePath: absPath}
	repoRoot, repoErr := attngit.GetRepoRoot(filepath.Dir(absPath))
	if repoErr == nil && repoRoot != "" {
		decision.RepoRoot = repoRoot
		decision.RepoPath, err = relativePathWithin(repoRoot, absPath)
		if err != nil {
			return planAttachment{}, err
		}
		decision.ScopePath, decision.Scope, err = resolvePlanScope(repoRoot, scope)
		if err != nil {
			return planAttachment{}, err
		}
		tracked := gitPathTracked(repoRoot, decision.RepoPath)
		decision.ConventionFound, err = repositoryHasDocumentationConvention(repoRoot, decision.Scope)
		if err != nil {
			return planAttachment{}, fmt.Errorf("inspect repository convention: %w", err)
		}
		decision.ConventionFound = decision.ConventionFound || tracked
	}

	switch requestedAuthority {
	case planAuthorityRepository:
		decision.Authority = planAuthorityRepository
	case planAuthorityNotebook:
		decision.Authority = planAuthorityNotebook
	case planAuthorityAuto:
		if decision.ConventionFound {
			decision.Authority = planAuthorityRepository
		} else {
			decision.Authority = planAuthorityNotebook
		}
	default:
		return planAttachment{}, fmt.Errorf("unknown authority %q", requestedAuthority)
	}

	if decision.Authority == planAuthorityRepository {
		if decision.RepoRoot == "" {
			return planAttachment{}, errors.New("repository authority requires the plan to be inside a Git worktree")
		}
		if !gitPathTracked(decision.RepoRoot, decision.RepoPath) {
			if decision.ConventionFound {
				return planAttachment{}, fmt.Errorf("repository documentation convention found at scope %q; commit %q before attaching its reference", decision.Scope, decision.RepoPath)
			}
			return planAttachment{}, fmt.Errorf("repository authority requires %q to be committed", decision.RepoPath)
		}
		if err := requireCleanGitPath(decision.RepoRoot, decision.RepoPath); err != nil {
			return planAttachment{}, err
		}
		branch, err := attngit.GetBranchInfo(decision.RepoRoot)
		if err != nil || strings.TrimSpace(branch.Branch) == "" {
			return planAttachment{}, errors.New("could not resolve the plan's Git branch")
		}
		decision.Branch = branch.Branch
		decision.IntroducedIn, err = introducingCommit(decision.RepoRoot, decision.RepoPath)
		if err != nil {
			return planAttachment{}, err
		}
		if remote, remoteErr := attngit.Output(attngit.OpMetadata, decision.RepoRoot, "config", "--get", "remote.origin.url"); remoteErr == nil {
			decision.Remote = strings.TrimSpace(string(remote))
		}
		stem := strings.TrimSuffix(filepath.Base(absPath), filepath.Ext(absPath))
		decision.ReferenceName = stem + ".reference.md"
		return decision, nil
	}

	if decision.RepoRoot != "" && gitPathTracked(decision.RepoRoot, decision.RepoPath) {
		return planAttachment{}, fmt.Errorf("refusing to promote tracked file %q to Notebook authority; remove it from Git explicitly first or use --authority repository", decision.RepoPath)
	}
	return decision, nil
}

func resolvePlanScope(repoRoot, requested string) (string, string, error) {
	scopePath := repoRoot
	if requested != "" {
		var err error
		scopePath, err = filepath.Abs(requested)
		if err != nil {
			return "", "", err
		}
		scopePath = attngit.CanonicalizePath(scopePath)
		info, statErr := os.Stat(scopePath)
		if statErr != nil {
			return "", "", fmt.Errorf("scope %q: %w", scopePath, statErr)
		}
		if !info.IsDir() {
			return "", "", fmt.Errorf("scope %q is not a directory", scopePath)
		}
	}
	rel, err := relativePathWithin(repoRoot, scopePath)
	if err != nil {
		return "", "", fmt.Errorf("scope: %w", err)
	}
	return scopePath, rel, nil
}

func relativePathWithin(root, path string) (string, error) {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return "", fmt.Errorf("%q is outside repository %q", path, root)
	}
	return filepath.ToSlash(rel), nil
}

func gitPathTracked(repoRoot, repoPath string) bool {
	_, err := attngit.Output(attngit.OpMetadata, repoRoot, "ls-files", "--error-unmatch", "--", repoPath)
	return err == nil
}

func requireCleanGitPath(repoRoot, repoPath string) error {
	out, err := attngit.Output(attngit.OpStatus, repoRoot, "status", "--porcelain=v1", "--", repoPath)
	if err != nil {
		return fmt.Errorf("inspect Git status for %q: %w", repoPath, err)
	}
	if strings.TrimSpace(string(out)) != "" {
		return fmt.Errorf("repository plan %q has uncommitted changes; commit it before attaching its reference", repoPath)
	}
	return nil
}

func repositoryHasDocumentationConvention(repoRoot, scope string) (bool, error) {
	pathspec := scope
	if pathspec == "." {
		pathspec = ":/"
	}
	out, err := attngit.Output(attngit.OpMetadata, repoRoot, "ls-files", "--", pathspec)
	if err != nil {
		return false, err
	}
	for _, trackedPath := range strings.Split(string(out), "\n") {
		trackedPath = filepath.ToSlash(strings.TrimSpace(trackedPath))
		if trackedPath == "" {
			continue
		}
		rel := trackedPath
		if scope != "." {
			prefix := strings.TrimSuffix(filepath.ToSlash(scope), "/") + "/"
			if !strings.HasPrefix(trackedPath, prefix) {
				continue
			}
			rel = strings.TrimPrefix(trackedPath, prefix)
		}
		if isDocumentationConventionPath(rel) {
			return true, nil
		}
	}
	return false, nil
}

func isDocumentationConventionPath(path string) bool {
	path = strings.ToLower(strings.TrimPrefix(filepath.ToSlash(path), "./"))
	for _, prefix := range []string{
		"docs/", "plans/", "design/", "designs/", "rfcs/", "rfc/",
		"architecture/", "adr/", "adrs/",
	} {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}

func introducingCommit(repoRoot, repoPath string) (string, error) {
	out, err := attngit.Output(attngit.OpMetadata, repoRoot, "log", "--follow", "--diff-filter=A", "--format=%H", "--", repoPath)
	if err != nil {
		return "", fmt.Errorf("find introducing commit for %q: %w", repoPath, err)
	}
	commits := strings.Fields(string(out))
	if len(commits) == 0 {
		return "", fmt.Errorf("could not find the commit that introduced %q", repoPath)
	}
	return commits[len(commits)-1], nil
}

func renderRepositoryPlanReference(plan planAttachment) []byte {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("attn_artifact: repository-reference\n")
	b.WriteString("authority: repository\n")
	if plan.Remote != "" {
		fmt.Fprintf(&b, "repository: %s\n", strconv.Quote(plan.Remote))
	}
	fmt.Fprintf(&b, "repository_root: %s\n", strconv.Quote(plan.RepoRoot))
	fmt.Fprintf(&b, "scope: %s\n", strconv.Quote(plan.Scope))
	fmt.Fprintf(&b, "path: %s\n", strconv.Quote(plan.RepoPath))
	fmt.Fprintf(&b, "branch: %s\n", strconv.Quote(plan.Branch))
	fmt.Fprintf(&b, "introduced_in: %s\n", strconv.Quote(plan.IntroducedIn))
	b.WriteString("---\n\n")
	fmt.Fprintf(&b, "# %s\n\n", strings.TrimSuffix(filepath.Base(plan.SourcePath), filepath.Ext(plan.SourcePath)))
	b.WriteString("The canonical plan is maintained in Git. Follow-on work should read and edit the repository file, not this reference.\n\n")
	fmt.Fprintf(&b, "- Path: `%s`\n", plan.RepoPath)
	fmt.Fprintf(&b, "- Local checkout: `%s`\n", plan.SourcePath)
	fmt.Fprintf(&b, "- Branch when attached: `%s`\n", plan.Branch)
	fmt.Fprintf(&b, "- Introduced in: `%s`\n", plan.IntroducedIn)
	if plan.Remote != "" {
		fmt.Fprintf(&b, "- Repository: `%s`\n", plan.Remote)
	}
	return []byte(b.String())
}

func attachPlan(sourceSession string, args ticketAttachPlanArgs, plan planAttachment) (*ticketAttachPlanResult, error) {
	attachmentFile := protocol.TicketAttachFile{SourcePath: plan.SourcePath, Filename: filepath.Base(plan.SourcePath)}
	cleanup := func() {}
	if plan.Authority == planAuthorityRepository {
		tempDir, err := os.MkdirTemp("", "attn-plan-reference-*")
		if err != nil {
			return nil, err
		}
		cleanup = func() { _ = os.RemoveAll(tempDir) }
		referencePath := filepath.Join(tempDir, plan.ReferenceName)
		if err := os.WriteFile(referencePath, renderRepositoryPlanReference(plan), 0o600); err != nil {
			cleanup()
			return nil, err
		}
		attachmentFile = protocol.TicketAttachFile{SourcePath: referencePath, Filename: plan.ReferenceName}
	}
	defer cleanup()

	result, err := client.New("").AttachTicket(sourceSession, []protocol.TicketAttachFile{attachmentFile}, args.Ticket, args.State, args.Comment)
	if err != nil {
		return nil, err
	}
	if len(result.Artifacts) != 1 {
		return nil, fmt.Errorf("attachment returned %d artifacts, want 1", len(result.Artifacts))
	}
	canonicalPath := result.Artifacts[0].Path
	output := &ticketAttachPlanResult{
		Authority:      plan.Authority,
		CanonicalPath:  canonicalPath,
		RepositoryPath: plan.RepoPath,
		Branch:         plan.Branch,
		IntroducedIn:   plan.IntroducedIn,
		Attachment:     result,
	}
	if plan.Authority == planAuthorityRepository {
		legacyPath, retired, retireErr := retireLegacyNotebookPlanCopy(plan.SourcePath, canonicalPath)
		if retireErr != nil {
			return nil, fmt.Errorf("repository reference was attached at %q, but legacy Notebook copy was preserved: %w", canonicalPath, retireErr)
		}
		output.LegacyNotebookCopyRetired = retired
		if retired {
			comment := fmt.Sprintf("Retired byte-identical legacy Notebook copy %s. Canonical plan: %s on branch %s (introduced in %s).", legacyPath, plan.RepoPath, plan.Branch, plan.IntroducedIn)
			if _, err := client.New("").CommentTicket(sourceSession, result.TicketID, comment); err != nil {
				return nil, fmt.Errorf("retired legacy Notebook copy %q, but could not record the migration on the ticket: %w", legacyPath, err)
			}
		}
		return output, nil
	}
	retired, err := retireNotebookPlanSource(plan, canonicalPath)
	if err != nil {
		return nil, fmt.Errorf("Notebook artifact is canonical at %q but staging source %q was not retired: %w", canonicalPath, plan.SourcePath, err)
	}
	output.SourceRetired = retired
	return output, nil
}

func retireLegacyNotebookPlanCopy(sourcePath, referencePath string) (string, bool, error) {
	legacyPath := filepath.Join(filepath.Dir(referencePath), filepath.Base(sourcePath))
	info, err := os.Lstat(legacyPath)
	if os.IsNotExist(err) {
		return legacyPath, false, nil
	}
	if err != nil {
		return legacyPath, false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return legacyPath, false, fmt.Errorf("%q is not a regular file", legacyPath)
	}
	sameFile, err := pathsReferToSameFile(sourcePath, legacyPath)
	if err != nil {
		return legacyPath, false, err
	}
	if sameFile {
		return legacyPath, false, errors.New("legacy Notebook path is the canonical repository source")
	}
	if err := verifySameFileContent(sourcePath, legacyPath); err != nil {
		return legacyPath, false, fmt.Errorf("%q differs from the committed repository plan; reconcile it explicitly", legacyPath)
	}
	if err := os.Remove(legacyPath); err != nil {
		return legacyPath, false, err
	}
	return legacyPath, true, nil
}

func retireNotebookPlanSource(plan planAttachment, canonicalPath string) (bool, error) {
	if err := verifySameFileContent(plan.SourcePath, canonicalPath); err != nil {
		return false, err
	}
	samePath, err := pathsReferToSameFile(plan.SourcePath, canonicalPath)
	if err != nil {
		return false, err
	}
	if samePath {
		return false, nil
	}
	if plan.RepoRoot != "" {
		if err := requireGitPathUntracked(plan.RepoRoot, plan.RepoPath); err != nil {
			return false, err
		}
	}
	if err := os.Remove(plan.SourcePath); err != nil {
		return false, err
	}
	return true, nil
}

// requireGitPathUntracked enforces the no-tracked-source deletion promise at
// the deletion edge. Exit code 1 is git ls-files' expected "not tracked"
// result; every other failure is ambiguous and therefore preserves the file.
func requireGitPathUntracked(repoRoot, repoPath string) error {
	_, err := attngit.Output(attngit.OpMetadata, repoRoot, "ls-files", "--error-unmatch", "--", repoPath)
	if err == nil {
		return fmt.Errorf("refusing to retire tracked file %q after Notebook promotion", repoPath)
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return nil
	}
	return fmt.Errorf("verify Git ownership for %q before retirement: %w", repoPath, err)
}

func verifySameFileContent(left, right string) error {
	leftData, err := os.ReadFile(left)
	if err != nil {
		return err
	}
	rightData, err := os.ReadFile(right)
	if err != nil {
		return err
	}
	leftHash := sha256.Sum256(leftData)
	rightHash := sha256.Sum256(rightData)
	if leftHash != rightHash {
		return errors.New("source and Notebook artifact differ")
	}
	return nil
}

func pathsReferToSameFile(left, right string) (bool, error) {
	leftInfo, err := os.Stat(left)
	if err != nil {
		return false, err
	}
	rightInfo, err := os.Stat(right)
	if err != nil {
		return false, err
	}
	return os.SameFile(leftInfo, rightInfo), nil
}

func runTicketAttachPlan(args []string) {
	parsed, err := parseTicketAttachPlanArgs(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach-plan: %v\n", err)
		writeTicketHelp(os.Stderr)
		os.Exit(2)
	}
	plan, err := inspectPlanAttachment(parsed.File, parsed.Scope, parsed.Authority)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach-plan: %v\n", err)
		os.Exit(1)
	}
	source, err := resolveDispatchSession(parsed.Session)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach-plan: %v\n", err)
		os.Exit(2)
	}
	result, err := attachPlan(source, parsed, plan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ticket attach-plan: %v\n", err)
		os.Exit(1)
	}
	if parsed.JSON {
		printJSON(result)
		return
	}
	if result.Authority == planAuthorityRepository {
		fmt.Printf("attached repository reference to ticket %s\n", result.Attachment.TicketID)
		fmt.Printf("  canonical Git plan: %s\n", plan.SourcePath)
		fmt.Printf("  branch: %s\n", result.Branch)
		fmt.Printf("  introduced in: %s\n", result.IntroducedIn)
		fmt.Printf("  Notebook reference: %s\n", result.CanonicalPath)
		if result.LegacyNotebookCopyRetired {
			fmt.Printf("  retired byte-identical legacy Notebook copy\n")
		}
		return
	}
	fmt.Printf("promoted plan to Notebook authority on ticket %s\n", result.Attachment.TicketID)
	fmt.Printf("  canonical Notebook plan: %s\n", result.CanonicalPath)
	if result.SourceRetired {
		fmt.Printf("  retired staging source: %s\n", plan.SourcePath)
	}
}
