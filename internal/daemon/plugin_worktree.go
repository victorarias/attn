package daemon

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/victorarias/attn/internal/git"
)

const (
	worktreeBeforeCreateSurface   = "worktree.before_create"
	worktreeCreateProviderSurface = "worktree.create"
	worktreeAfterCreateSurface    = "worktree.after_create"
	worktreeDeleteProviderSurface = "worktree.delete"
	worktreePluginCallTimeout     = 30 * time.Second

	providerStatusHandled = "handled"
	providerStatusDecline = "decline"
	providerStatusError   = "error"
)

type worktreeCreateProviderParams struct {
	MainRepo      string  `json:"main_repo"`
	Branch        string  `json:"branch"`
	StartingFrom  string  `json:"starting_from,omitempty"`
	RequestedPath *string `json:"requested_path"`
}

type worktreeCreateProviderResult struct {
	Status string `json:"status"`
	Path   string `json:"path,omitempty"`
	Branch string `json:"branch,omitempty"`
	Error  string `json:"error,omitempty"`
}

type worktreeDeleteProviderParams struct {
	MainRepo string `json:"main_repo"`
	Path     string `json:"path"`
	Branch   string `json:"branch,omitempty"`
	Force    bool   `json:"force"`
}

type worktreeDeleteProviderResult struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type worktreeAfterCreateHookParams struct {
	MainRepo string `json:"main_repo"`
	Path     string `json:"path"`
	Branch   string `json:"branch"`
}

func (d *Daemon) dispatchWorktreeBeforeCreateHooks(mainRepo, branch, startingFrom, requestedPath string) error {
	params := worktreeCreateProviderParams{
		MainRepo:     mainRepo,
		Branch:       branch,
		StartingFrom: startingFrom,
	}
	if requestedPath != "" {
		params.RequestedPath = &requestedPath
	}
	return d.dispatchWorktreeHooks(worktreeBeforeCreateSurface, params)
}

func (d *Daemon) dispatchWorktreeAfterCreateHooks(mainRepo, path, branch string) error {
	return d.dispatchWorktreeHooks(worktreeAfterCreateSurface, worktreeAfterCreateHookParams{
		MainRepo: mainRepo,
		Path:     path,
		Branch:   branch,
	})
}

func (d *Daemon) dispatchWorktreeHooks(surface string, params interface{}) error {
	handlers := d.ensurePluginRegistry().handlersForSurface(surface)
	for _, handler := range handlers {
		ctx, cancel := context.WithTimeout(context.Background(), worktreePluginCallTimeout)
		err := d.callPlugin(ctx, handler.PluginName, surface, params, nil)
		cancel()
		if err != nil {
			d.logf("worktree hook plugin=%s surface=%s status=call_failed error=%s", handler.PluginName, surface, providerLogValue(err.Error()))
			return fmt.Errorf("worktree hook %q %s call failed: %w", handler.PluginName, surface, err)
		}
		d.logf("worktree hook plugin=%s surface=%s status=completed", handler.PluginName, surface)
	}
	return nil
}

func (d *Daemon) dispatchWorktreeCreateProvider(mainRepo, branch, startingFrom, requestedPath string) (string, string, bool, error) {
	providers := d.ensurePluginRegistry().handlersForSurface(worktreeCreateProviderSurface)
	if len(providers) == 0 {
		return "", "", false, nil
	}
	preExisting, err := currentWorktreePathSet(mainRepo)
	if err != nil {
		return "", "", false, fmt.Errorf("list worktrees before provider create: %w", err)
	}

	params := worktreeCreateProviderParams{
		MainRepo:     mainRepo,
		Branch:       branch,
		StartingFrom: startingFrom,
	}
	if requestedPath != "" {
		params.RequestedPath = &requestedPath
	}

	for _, provider := range providers {
		var result worktreeCreateProviderResult
		ctx, cancel := context.WithTimeout(context.Background(), worktreePluginCallTimeout)
		err := d.callPlugin(ctx, provider.PluginName, worktreeCreateProviderSurface, params, &result)
		cancel()
		if err != nil {
			d.logf("worktree provider plugin=%s surface=%s status=call_failed main_repo=%s branch=%s starting_from=%s requested_path=%s error=%s", provider.PluginName, worktreeCreateProviderSurface, mainRepo, branch, startingFrom, requestedPath, providerLogValue(err.Error()))
			return "", "", false, fmt.Errorf("worktree provider %q create call failed: %w", provider.PluginName, err)
		}

		switch strings.TrimSpace(result.Status) {
		case providerStatusDecline:
			d.logf("worktree provider plugin=%s surface=%s status=decline main_repo=%s branch=%s starting_from=%s requested_path=%s", provider.PluginName, worktreeCreateProviderSurface, mainRepo, branch, startingFrom, requestedPath)
			continue
		case providerStatusError:
			d.logf("worktree provider plugin=%s surface=%s status=error main_repo=%s branch=%s starting_from=%s requested_path=%s error=%s", provider.PluginName, worktreeCreateProviderSurface, mainRepo, branch, startingFrom, requestedPath, providerLogValue(result.Error))
			return "", "", false, providerOperationError(provider.PluginName, worktreeCreateProviderSurface, result.Error)
		case providerStatusHandled:
			path, createdBranch, err := validateCreatedProviderWorktree(mainRepo, result, preExisting)
			if err != nil {
				d.logf("worktree provider plugin=%s surface=%s status=invalid_result main_repo=%s branch=%s starting_from=%s requested_path=%s error=%s", provider.PluginName, worktreeCreateProviderSurface, mainRepo, branch, startingFrom, requestedPath, providerLogValue(err.Error()))
				return "", "", false, fmt.Errorf("worktree provider %q returned invalid create result: %w", provider.PluginName, err)
			}
			d.logf("worktree provider plugin=%s surface=%s status=handled main_repo=%s branch=%s created_branch=%s starting_from=%s requested_path=%s path=%s", provider.PluginName, worktreeCreateProviderSurface, mainRepo, branch, createdBranch, startingFrom, requestedPath, path)
			return path, createdBranch, true, nil
		default:
			d.logf("worktree provider plugin=%s surface=%s status=unsupported main_repo=%s branch=%s starting_from=%s requested_path=%s provider_status=%s", provider.PluginName, worktreeCreateProviderSurface, mainRepo, branch, startingFrom, requestedPath, providerLogValue(result.Status))
			return "", "", false, fmt.Errorf("worktree provider %q returned unsupported create status %q", provider.PluginName, result.Status)
		}
	}

	d.logf("worktree provider surface=%s status=fallback main_repo=%s branch=%s starting_from=%s requested_path=%s", worktreeCreateProviderSurface, mainRepo, branch, startingFrom, requestedPath)
	return "", "", false, nil
}

func (d *Daemon) dispatchWorktreeDeleteProvider(mainRepo, path, branch string, force bool) (bool, error) {
	providers := d.ensurePluginRegistry().handlersForSurface(worktreeDeleteProviderSurface)
	if len(providers) == 0 {
		return false, nil
	}

	params := worktreeDeleteProviderParams{
		MainRepo: mainRepo,
		Path:     path,
		Branch:   branch,
		Force:    force,
	}
	for _, provider := range providers {
		var result worktreeDeleteProviderResult
		ctx, cancel := context.WithTimeout(context.Background(), worktreePluginCallTimeout)
		err := d.callPlugin(ctx, provider.PluginName, worktreeDeleteProviderSurface, params, &result)
		cancel()
		if err != nil {
			d.logf("worktree provider plugin=%s surface=%s status=call_failed main_repo=%s path=%s branch=%s error=%s", provider.PluginName, worktreeDeleteProviderSurface, mainRepo, path, branch, providerLogValue(err.Error()))
			return false, fmt.Errorf("worktree provider %q delete call failed: %w", provider.PluginName, err)
		}

		switch strings.TrimSpace(result.Status) {
		case providerStatusDecline:
			d.logf("worktree provider plugin=%s surface=%s status=decline main_repo=%s path=%s branch=%s", provider.PluginName, worktreeDeleteProviderSurface, mainRepo, path, branch)
			continue
		case providerStatusError:
			d.logf("worktree provider plugin=%s surface=%s status=error main_repo=%s path=%s branch=%s error=%s", provider.PluginName, worktreeDeleteProviderSurface, mainRepo, path, branch, providerLogValue(result.Error))
			return false, providerOperationError(provider.PluginName, worktreeDeleteProviderSurface, result.Error)
		case providerStatusHandled:
			if err := validateDeletedProviderWorktree(mainRepo, path); err != nil {
				d.logf("worktree provider plugin=%s surface=%s status=invalid_result main_repo=%s path=%s branch=%s error=%s", provider.PluginName, worktreeDeleteProviderSurface, mainRepo, path, branch, providerLogValue(err.Error()))
				return false, fmt.Errorf("worktree provider %q returned invalid delete result: %w", provider.PluginName, err)
			}
			d.logf("worktree provider plugin=%s surface=%s status=handled main_repo=%s path=%s branch=%s", provider.PluginName, worktreeDeleteProviderSurface, mainRepo, path, branch)
			return true, nil
		default:
			d.logf("worktree provider plugin=%s surface=%s status=unsupported main_repo=%s path=%s branch=%s provider_status=%s", provider.PluginName, worktreeDeleteProviderSurface, mainRepo, path, branch, providerLogValue(result.Status))
			return false, fmt.Errorf("worktree provider %q returned unsupported delete status %q", provider.PluginName, result.Status)
		}
	}

	d.logf("worktree provider surface=%s status=fallback main_repo=%s path=%s branch=%s", worktreeDeleteProviderSurface, mainRepo, path, branch)
	return false, nil
}

func providerOperationError(pluginName, surface, message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "provider reported an error"
	}
	return fmt.Errorf("worktree provider %q %s: %s", pluginName, surface, message)
}

func providerLogValue(value string) string {
	value = strings.TrimSpace(value)
	if len(value) <= 500 {
		return value
	}
	return value[:500] + "...(truncated)"
}

func validateCreatedProviderWorktree(mainRepo string, result worktreeCreateProviderResult, preExisting map[string]bool) (string, string, error) {
	path := git.CanonicalizePath(strings.TrimSpace(result.Path))
	if path == "" {
		return "", "", fmt.Errorf("handled create result is missing path")
	}
	if preExisting[path] {
		return "", "", fmt.Errorf("handled create result path %q already existed before provider create", path)
	}

	branch := strings.TrimSpace(result.Branch)
	if branch == "" {
		return "", "", fmt.Errorf("handled create result is missing branch")
	}

	worktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		return "", "", fmt.Errorf("list worktrees: %w", err)
	}
	for _, worktree := range worktrees {
		if git.CanonicalizePath(worktree.Path) != path {
			continue
		}
		if worktree.Branch != branch {
			return "", "", fmt.Errorf("created worktree branch is %q, provider reported %q", worktree.Branch, branch)
		}
		return path, branch, nil
	}

	return "", "", fmt.Errorf("created path %q is not a worktree of %q", path, mainRepo)
}

func currentWorktreePathSet(mainRepo string) (map[string]bool, error) {
	worktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		return nil, err
	}
	paths := make(map[string]bool, len(worktrees))
	for _, worktree := range worktrees {
		paths[git.CanonicalizePath(worktree.Path)] = true
	}
	return paths, nil
}

func validateDeletedProviderWorktree(mainRepo, path string) error {
	expectedPath := git.CanonicalizePath(path)
	worktrees, err := git.ListWorktrees(mainRepo)
	if err != nil {
		return fmt.Errorf("list worktrees: %w", err)
	}
	for _, worktree := range worktrees {
		if git.CanonicalizePath(worktree.Path) == expectedPath {
			return fmt.Errorf("deleted path %q is still a worktree of %q", expectedPath, mainRepo)
		}
	}
	return nil
}
