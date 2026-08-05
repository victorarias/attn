package daemon

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// hubSendMethods are the ways the daemon puts something on the wire for more
// than one client. A call to any of them is wire traffic.
var hubSendMethods = map[string]bool{
	"Broadcast":                    true,
	"BroadcastValue":               true,
	"BroadcastRawText":             true,
	"SendRawTextToMatchingClients": true,
	"SendValueToMatchingClients":   true,
}

// daemonSendHelpers are the daemon's own wrappers around those methods. They
// are wire traffic too, from the caller's point of view.
var daemonSendHelpers = map[string]bool{
	"broadcastMessage":      true,
	"broadcastRawWSMessage": true,
}

// wireSenderExceptions is the enumerated list A2 closes on: the functions that
// may reach the hub without a fact behind them. Everything else must publish,
// and the projection does the sending.
//
// Adding an entry here is a design decision, not a formality — it says this
// traffic is not a state change any consumer could subscribe to. Read the
// reason before you add one.
var wireSenderExceptions = map[string]string{
	// The projection table. Its closures are projections; they just do not have
	// their own names.
	"buildWireProjections": "the projection table itself",

	// The plumbing itself. Projections call these.
	"broadcastMessage":      "the generic value sender every projection uses",
	"broadcastRawWSMessage": "the remote relay: the fact was already published on the remote daemon's bus, and re-publishing it locally would duplicate it",

	// Byte streams. High volume, no entity worth subscribing to, and the tile
	// and attach paths route by a per-client predicate that pub/sub cannot
	// express.
	"broadcastFsChanged":   "filesystem change bursts, coalesced per watcher rather than per file",
	"broadcastTileContent": "workspace tile content bytes, sent only to the clients subscribed to that tile",
	"handleHostEvent":      "a conversation host's envelope stream: render deltas the daemon never reads, on the same direct path as pty_output. The daemon's picture of the session is built from its own state, never from one of these",
}

// TestWireTrafficComesFromProjections pins the boundary A2 establishes: a state
// change reaches clients by publishing a fact, and only a projection turns a
// fact into bytes. A new broadcaster that skips the bus fails here.
func TestWireTrafficComesFromProjections(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("read package dir: %v", err)
	}

	fset := token.NewFileSet()
	var offenders []string

	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}

		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			caller := fn.Name.Name
			if strings.HasPrefix(caller, "project") || wireSenderExceptions[caller] != "" {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				sel, ok := call.Fun.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				switch {
				case isHubSend(sel), isDaemonSendHelper(sel):
					offenders = append(offenders, filepath.Base(fset.Position(call.Pos()).String())+"\t"+caller)
				}
				return true
			})
		}
	}

	if len(offenders) > 0 {
		sort.Strings(offenders)
		t.Errorf("these functions push to WebSocket clients without going through a fact:\n\t%s\n\n"+
			"Publish a fact naming the entity that changed and move the push into a projection "+
			"(see internal/daemon/bus.go). If the traffic genuinely is not a state change, add the "+
			"function to wireSenderExceptions with the reason.",
			strings.Join(offenders, "\n\t"))
	}
}

// isHubSend matches d.wsHub.<send>(...).
func isHubSend(sel *ast.SelectorExpr) bool {
	if !hubSendMethods[sel.Sel.Name] {
		return false
	}
	inner, ok := sel.X.(*ast.SelectorExpr)
	return ok && inner.Sel.Name == "wsHub"
}

// isDaemonSendHelper matches d.broadcastMessage(...) and friends.
func isDaemonSendHelper(sel *ast.SelectorExpr) bool {
	if !daemonSendHelpers[sel.Sel.Name] {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == "d"
}
