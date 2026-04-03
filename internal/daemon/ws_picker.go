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

func listDirectoryEntries(dirToQuery string, prefix string) ([]protocol.DirectoryEntry, error) {
	entries, err := os.ReadDir(dirToQuery)
	if err != nil {
		return nil, err
	}

	prefix = strings.ToLower(prefix)
	var directories []protocol.DirectoryEntry
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil || !info.IsDir() {
			continue
		}
		name := entry.Name()
		if prefix != "" && !strings.Contains(strings.ToLower(name), prefix) {
			continue
		}
		directories = append(directories, protocol.DirectoryEntry{
			Name: name,
			Path: filepath.Join(dirToQuery, name),
		})
	}

	sort.Slice(directories, func(i, j int) bool {
		left := strings.ToLower(directories[i].Name)
		right := strings.ToLower(directories[j].Name)
		if prefix != "" {
			leftStarts := strings.HasPrefix(left, prefix)
			rightStarts := strings.HasPrefix(right, prefix)
			if leftStarts != rightStarts {
				return leftStarts
			}
		}
		return left < right
	})

	return directories, nil
}

func inspectPickerPath(input string) (*protocol.PathInspection, error) {
	resolved, homePath, err := expandPickerPath(input)
	if err != nil {
		return nil, err
	}
	resolved = filepath.Clean(resolved)

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

	branchInfo, err := git.GetBranchInfo(resolved)
	if err != nil || branchInfo == nil || branchInfo.Branch == "" {
		return inspection, nil
	}
	repoRoot := git.ResolveMainRepoPath(resolved)
	if repoRoot != "" {
		inspection.RepoRoot = protocol.Ptr(repoRoot)
	}
	return inspection, nil
}

func (d *Daemon) handleBrowseDirectoryWS(client *wsClient, msg *protocol.BrowseDirectoryMessage) {
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

		entries, err := listDirectoryEntries(dirToQuery, prefix)
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
