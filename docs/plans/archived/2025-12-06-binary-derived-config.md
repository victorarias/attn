# Binary-Derived Configuration

**Date:** 2025-12-06
**Status:** Design approved

## Goal

Enable running two isolated versions of the CLI simultaneously:
- `cm` - stable version for daily use
- `attn` - experimental version for deeper tmux integration work

This also rebrands the experimental version to reflect the tool's evolution from "Claude manager" to "attention queue" (things waiting for user input: Claude sessions, PRs, etc.).

## Design

### Core Concept

The binary name becomes the namespace. At startup, the program detects its own executable name and derives all paths from it:

| Binary | Socket | State | Logs |
|--------|--------|-------|------|
| `cm` | `~/.cm.sock` | `~/.cm-state.json` | `~/.cm/daemon.log` |
| `attn` | `~/.attn.sock` | `~/.attn-state.json` | `~/.attn/daemon.log` |

Same codebase, different binary name = completely isolated runtime.

### New `internal/config` Package

```go
package config

import (
    "os"
    "path/filepath"
)

var binaryName string

func init() {
    binaryName = filepath.Base(os.Args[0])
}

// BinaryName returns the name of the running binary (e.g., "cm", "attn")
func BinaryName() string {
    return binaryName
}

// SetBinaryName overrides the binary name (for testing)
func SetBinaryName(name string) {
    binaryName = name
}

// SocketPath returns the unix socket path
func SocketPath() string {
    home, err := os.UserHomeDir()
    if err != nil {
        return "/tmp/." + binaryName + ".sock"
    }
    return filepath.Join(home, "."+binaryName+".sock")
}

// StatePath returns the state file path
func StatePath() string {
    home, err := os.UserHomeDir()
    if err != nil {
        return "/tmp/." + binaryName + "-state.json"
    }
    return filepath.Join(home, "."+binaryName+"-state.json")
}

// LogPath returns the log file path
func LogPath() string {
    home, err := os.UserHomeDir()
    if err != nil {
        return "/tmp/" + binaryName + ".log"
    }
    return filepath.Join(home, "."+binaryName, "daemon.log")
}

// DebugLevel returns the debug level from DEBUG env var
func DebugLevel() int {
    switch os.Getenv("DEBUG") {
    case "trace":
        return 4 // LogTrace
    case "debug":
        return 3 // LogDebug
    case "info":
        return 2 // LogInfo
    case "warn":
        return 1 // LogWarn
    case "1", "true":
        return 3 // LogDebug
    default:
        return 0 // LogError
    }
}
```

### Migration of Existing Code

**Files to update:**

1. `internal/client/client.go`
   - Remove `DefaultSocketPath()` function
   - Import `config` package
   - Replace `DefaultSocketPath()` calls with `config.SocketPath()`

2. `internal/store/store.go`
   - Remove `DefaultStatePath()` function
   - Import `config` package
   - Replace `DefaultStatePath()` calls with `config.StatePath()`

3. `internal/logging/logging.go`
   - Remove `DefaultLogPath()` function
   - Import `config` package
   - Replace `DefaultLogPath()` calls with `config.LogPath()`

4. `cmd/cm/main.go`
   - Remove `CM_DEBUG` env var handling
   - Import `config` package
   - Use `config.DebugLevel()` for log level initialization

### Makefile Changes

```makefile
# Existing targets (unchanged behavior, new paths)
build:
	go build -o cm ./cmd/cm

install:
	go build -o ~/.local/bin/cm ./cmd/cm

# New targets for experimental version
build-attn:
	go build -o attn ./cmd/cm

install-attn:
	go build -o ~/.local/bin/attn ./cmd/cm
```

### Environment Variable

Single `DEBUG` env var works for both binaries:

```bash
DEBUG=debug cm -d      # debug cm
DEBUG=trace attn -d    # trace attn
```

## Testing

- Unit tests for `config` package verify path derivation from binary name
- Existing tests use `config.SetBinaryName("test")` to isolate test runs
- Integration tests can verify isolation by running both binaries

## Backwards Compatibility

- `cm` behavior unchanged (paths just move from `~/.claude-manager.*` to `~/.cm.*`)
- One-time migration: users may need to copy/rename existing state file
- Or: detect old paths and auto-migrate on first run (optional enhancement)

## Implementation Order

1. Create `internal/config` package with path functions
2. Update `client`, `store`, `logging` to use config package
3. Update `cmd/cm/main.go` to use `DEBUG` env var
4. Add Makefile targets for `attn`
5. Update tests to use `config.SetBinaryName()`
6. Test both binaries run in isolation

## Future Considerations

- The `attn` name sets up for broader "attention management" features
- Same architecture supports adding more binaries if needed
- Config package could later support explicit overrides via flags if needed
