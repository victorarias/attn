package daemon

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/victorarias/attn/internal/git"
	"github.com/victorarias/attn/internal/protocol"
)

func expandPickerPath(input string) (resolved string, homePath string, err error) {
	homePath, err = os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	trimmed := strings.TrimSpace(input)
	switch {
	case trimmed == "":
		return "", homePath, nil
	case trimmed == "~":
		return homePath, homePath, nil
	case strings.HasPrefix(trimmed, "~/"):
		return filepath.Join(homePath, trimmed[2:]), homePath, nil
	case strings.HasPrefix(trimmed, "~"):
		return filepath.Join(homePath, trimmed[1:]), homePath, nil
	default:
		return trimmed, homePath, nil
	}
}

func parseBrowseInput(input string) (directory string, prefix string, homePath string, err error) {
	trimmed := strings.TrimSpace(input)
	explicitDirectory := strings.HasSuffix(trimmed, string(os.PathSeparator))

	expanded, homePath, err := expandPickerPath(input)
	if err != nil || expanded == "" {
		return "", "", homePath, err
	}

	if explicitDirectory {
		return filepath.Clean(expanded), "", homePath, nil
	}

	lastSlash := strings.LastIndex(expanded, string(os.PathSeparator))
	if lastSlash == -1 {
		return "", "", homePath, nil
	}

	directory = expanded[:lastSlash+1]
	if directory == "" {
		directory = string(os.PathSeparator)
	}
	return filepath.Clean(directory), strings.ToLower(expanded[lastSlash+1:]), homePath, nil
}

// listDirectoryEntries lists one directory. Directories always come back; a
// non-empty extensions filter (dotless, lowercased — see normalizeExtensions)
// adds the regular files carrying those extensions, which is what turns the
// session picker's directory browser into the markdown opener's path mode.
// Symlinks and other irregular entries are dropped for files, matching
// fs_index: only a regular file is openable.
//
// .git is never listed, the same rule fs_index applies (skippedDirName), so a
// dot-directory holding real documents stays reachable both ways while git's
// object database is noise in neither.
//
// Directories sort before files so a listing reads as "where you can go, then
// what you can open"; within each group the existing rule holds — entries whose
// name starts with the typed prefix first, then alphabetical.
func listDirectoryEntries(dirToQuery string, prefix string, extensions []string) ([]protocol.DirectoryEntry, error) {
	entries, err := os.ReadDir(dirToQuery)
	if err != nil {
		return nil, err
	}

	prefix = strings.ToLower(prefix)
	wanted := normalizeExtensions(extensions)
	var listed []protocol.DirectoryEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		name := entry.Name()
		isDir := info.IsDir()
		switch {
		case isDir:
			if skippedDirName(name) {
				continue
			}
		case len(wanted) == 0 || !info.Mode().IsRegular() || !matchesExtension(name, wanted):
			continue
		}
		if prefix != "" && !strings.Contains(strings.ToLower(name), prefix) {
			continue
		}
		listed = append(listed, protocol.DirectoryEntry{
			Name:  name,
			Path:  filepath.Join(dirToQuery, name),
			IsDir: isDir,
		})
	}

	sort.Slice(listed, func(i, j int) bool {
		if listed[i].IsDir != listed[j].IsDir {
			return listed[i].IsDir
		}
		left := strings.ToLower(listed[i].Name)
		right := strings.ToLower(listed[j].Name)
		if prefix != "" {
			leftStarts := strings.HasPrefix(left, prefix)
			rightStarts := strings.HasPrefix(right, prefix)
			if leftStarts != rightStarts {
				return leftStarts
			}
		}
		return left < right
	})

	return listed, nil
}

func inspectPickerPath(input string) (*protocol.PathInspection, error) {
	resolved, homePath, err := expandPickerPath(input)
	if err != nil {
		return nil, err
	}
	resolved = git.CanonicalizePath(resolved)

	inspection := &protocol.PathInspection{
		InputPath:    input,
		ResolvedPath: resolved,
		HomePath:     protocol.Ptr(homePath),
		Exists:       false,
		IsDirectory:  false,
	}

	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return inspection, nil
		}
		return nil, err
	}

	inspection.Exists = true
	inspection.IsDirectory = info.IsDir()
	if !inspection.IsDirectory {
		return inspection, nil
	}

	repoRoot, isRepoTarget, err := git.ResolvePickerRepoTarget(resolved)
	if err != nil || !isRepoTarget || repoRoot == "" {
		return inspection, nil
	}
	inspection.RepoRoot = protocol.Ptr(repoRoot)
	return inspection, nil
}

func (d *Daemon) handleBrowseDirectoryWS(client *wsClient, msg *protocol.BrowseDirectoryMessage) {
	// Directory names leak only the shape of the tree, which this command has
	// always been willing to hand any local client. File names are the user's
	// documents, so the moment a request asks for them it needs the same gate
	// every fs_* command applies to an arbitrary root (see resolveFsRoot).
	if len(msg.Extensions) > 0 && !client.isTrustedAppClient() {
		d.sendToClient(client, &protocol.BrowseDirectoryResultMessage{
			Event:      protocol.EventBrowseDirectoryResult,
			InputPath:  msg.InputPath,
			EndpointID: msg.EndpointID,
			RequestID:  msg.RequestID,
			Success:    false,
			Error:      protocol.Ptr("listing files requires the authenticated attn app client"),
		})
		return
	}
	go func() {
		dirToQuery, prefix, homePath, err := parseBrowseInput(msg.InputPath)
		if err != nil {
			d.sendToClient(client, &protocol.BrowseDirectoryResultMessage{
				Event:      protocol.EventBrowseDirectoryResult,
				InputPath:  msg.InputPath,
				EndpointID: msg.EndpointID,
				RequestID:  msg.RequestID,
				Success:    false,
				Error:      protocol.Ptr(err.Error()),
			})
			return
		}

		if dirToQuery == "" {
			d.sendToClient(client, &protocol.BrowseDirectoryResultMessage{
				Event:      protocol.EventBrowseDirectoryResult,
				InputPath:  msg.InputPath,
				Directory:  "",
				Entries:    []protocol.DirectoryEntry{},
				EndpointID: msg.EndpointID,
				RequestID:  msg.RequestID,
				HomePath:   protocol.Ptr(homePath),
				Success:    true,
			})
			return
		}

		entries, err := listDirectoryEntries(dirToQuery, prefix, msg.Extensions)
		if err != nil {
			d.sendToClient(client, &protocol.BrowseDirectoryResultMessage{
				Event:      protocol.EventBrowseDirectoryResult,
				InputPath:  msg.InputPath,
				Directory:  dirToQuery,
				EndpointID: msg.EndpointID,
				RequestID:  msg.RequestID,
				HomePath:   protocol.Ptr(homePath),
				Success:    false,
				Error:      protocol.Ptr(err.Error()),
			})
			return
		}

		d.sendToClient(client, &protocol.BrowseDirectoryResultMessage{
			Event:      protocol.EventBrowseDirectoryResult,
			InputPath:  msg.InputPath,
			Directory:  dirToQuery,
			Entries:    entries,
			EndpointID: msg.EndpointID,
			RequestID:  msg.RequestID,
			HomePath:   protocol.Ptr(homePath),
			Success:    true,
		})
	}()
}

func (d *Daemon) handleInspectPathWS(client *wsClient, msg *protocol.InspectPathMessage) {
	go func() {
		inspection, err := inspectPickerPath(msg.Path)
		if err != nil {
			d.sendToClient(client, &protocol.InspectPathResultMessage{
				Event:      protocol.EventInspectPathResult,
				Inspection: &protocol.PathInspection{InputPath: msg.Path},
				EndpointID: msg.EndpointID,
				RequestID:  msg.RequestID,
				Success:    false,
				Error:      protocol.Ptr(err.Error()),
			})
			return
		}

		d.sendToClient(client, &protocol.InspectPathResultMessage{
			Event:      protocol.EventInspectPathResult,
			Inspection: inspection,
			EndpointID: msg.EndpointID,
			RequestID:  msg.RequestID,
			Success:    true,
		})
	}()
}
