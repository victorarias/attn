package daemon

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/victorarias/attn/internal/garden"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/workspacelayout"
)

func readSeedDocumentResult(t *testing.T, client *wsClient) protocol.SeedDocumentGetResultMessage {
	t.Helper()
	var result protocol.SeedDocumentGetResultMessage
	message := <-client.send
	if err := json.Unmarshal(message.payload, &result); err != nil {
		t.Fatalf("decode seed document result: %v", err)
	}
	return result
}

func TestSeedDocumentGetReturnsBodyImmediateChildrenAndLog(t *testing.T) {
	d := newGardenDaemon(t)
	body := "# Crown\n\nRead this."
	crown := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Crown", Body: &body})
	child := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Child", PartOf: &crown.ID})
	plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Grandchild", PartOf: &child.ID})
	note(t, d, "sess-a", crown.ID, "reader log entry", "trellis")

	client := newWorkspaceProtocolTestClient()
	d.handleSeedDocumentGet(client, &protocol.SeedDocumentGetMessage{
		Cmd: protocol.CmdSeedDocumentGet, SeedID: crown.ID, RequestID: "seed-doc-1",
	})
	result := readSeedDocumentResult(t, client)
	if !result.Success || result.RequestID != "seed-doc-1" || result.Document == nil {
		t.Fatalf("result = %+v, want successful correlated document", result)
	}
	if result.Document.Seed.Body != body {
		t.Fatalf("body = %q, want %q", result.Document.Seed.Body, body)
	}
	if len(result.Document.Children) != 1 || result.Document.Children[0].ID != child.ID {
		t.Fatalf("children = %+v, want only immediate child %s", result.Document.Children, child.ID)
	}
	if result.Document.NotesTotal != 1 || len(result.Document.Notes) != 1 || result.Document.Notes[0].Body != "reader log entry" {
		t.Fatalf("notes = %+v total=%d", result.Document.Notes, result.Document.NotesTotal)
	}
}

func TestSeedDocumentGetNamesUnknownID(t *testing.T) {
	d := newGardenDaemon(t)
	client := newWorkspaceProtocolTestClient()
	d.handleSeedDocumentGet(client, &protocol.SeedDocumentGetMessage{
		Cmd: protocol.CmdSeedDocumentGet, SeedID: "s-ffffff", RequestID: "missing",
	})
	result := readSeedDocumentResult(t, client)
	if result.Success || result.Error == nil || !strings.Contains(*result.Error, "s-ffffff") {
		t.Fatalf("result = %+v, want loud error naming s-ffffff", result)
	}
}

func TestSeedDocumentGetReportsWhetherTheStoredTenderStillHolds(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{Title: "Held only while live"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")

	read := func(requestID string) protocol.SeedDocumentGetResultMessage {
		client := newWorkspaceProtocolTestClient()
		d.handleSeedDocumentGet(client, &protocol.SeedDocumentGetMessage{
			Cmd: protocol.CmdSeedDocumentGet, SeedID: seed.ID, RequestID: requestID,
		})
		return readSeedDocumentResult(t, client)
	}
	if result := read("live"); !result.Success || result.Document == nil || !result.Document.TenderHolds {
		t.Fatalf("live tender document = %+v, want tender_holds", result)
	}

	d.store.Remove("sess-a")
	result := read("gone")
	if !result.Success || result.Document == nil || result.Document.TenderHolds {
		t.Fatalf("ended tender document = %+v, want stored identity without a live hold", result)
	}
	if result.Document.Seed.TenderSession != "sess-a" {
		t.Fatalf("read model erased stored tender identity: %+v", result.Document.Seed)
	}
}

func TestOpenSeedUsesPlacementPaneAndTenderBinding(t *testing.T) {
	d := newGardenDaemon(t)
	_, _, workspaceID := setupMarkdownWorkspaceOn(t, d)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Read me"})
	move(t, d, "sess-a", seed.ID, garden.VerbTend, "", "trellis")

	gotWorkspace, tileID, err := d.openSeedTile(seed.ID, "session-1")
	if err != nil {
		t.Fatalf("openSeedTile: %v", err)
	}
	if gotWorkspace != workspaceID || tileID != seedTileIDForID(seed.ID) {
		t.Fatalf("open = (%q, %q), want (%q, %q)", gotWorkspace, tileID, workspaceID, seedTileIDForID(seed.ID))
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	leaves := workspacelayout.TileLeaves(snapshot.Layout)
	if len(leaves) != 1 || leaves[0].TileKind != string(workspacelayout.TileKindSeed) || leaves[0].TileParams != seed.ID || leaves[0].TileSessionID != "sess-a" {
		t.Fatalf("seed tile = %+v, want seed params and tender binding", leaves)
	}
	move(t, d, "sess-a", seed.ID, garden.VerbPark, "", "trellis")
	if _, reopenedTileID, err := d.openSeedTile(seed.ID, "session-1"); err != nil || reopenedTileID != tileID {
		t.Fatalf("reopen = (%q, %v), want existing %q", reopenedTileID, err, tileID)
	}
	snapshot = d.store.GetWorkspaceLayout(workspaceID)
	leaves = workspacelayout.TileLeaves(snapshot.Layout)
	if len(leaves) != 1 || leaves[0].TileSessionID != "session-1" {
		t.Fatalf("reopened seed tile = %+v, want refreshed fallback binding", leaves)
	}
}

func TestOpenSeedNamesUnknownID(t *testing.T) {
	d := newGardenDaemon(t)
	setupMarkdownWorkspaceOn(t, d)
	_, _, err := d.openSeedTile("s-ffffff", "session-1")
	if err == nil || !strings.Contains(err.Error(), "s-ffffff") {
		t.Fatalf("error = %v, want unknown id named", err)
	}
}

func TestOpenSeedWSReturnsCorrelatedTile(t *testing.T) {
	d := newGardenDaemon(t)
	_, _, workspaceID := setupMarkdownWorkspaceOn(t, d)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Open from panel"})
	client := &wsClient{send: make(chan outboundMessage, 1)}
	d.handleOpenSeedWS(client, &protocol.OpenSeedMessage{
		Cmd: protocol.CmdOpenSeed, SeedID: seed.ID, SessionID: protocol.Ptr("session-1"), RequestID: protocol.Ptr("open-1"),
	})
	var result protocol.OpenSeedResultMessage
	message := <-client.send
	if err := json.Unmarshal(message.payload, &result); err != nil {
		t.Fatal(err)
	}
	if !result.Success || protocol.Deref(result.RequestID) != "open-1" || protocol.Deref(result.WorkspaceID) != workspaceID || protocol.Deref(result.TileID) != seedTileIDForID(seed.ID) {
		t.Fatalf("open_seed_result = %+v", result)
	}
}

func TestConcurrentMarkdownAndSeedOpenPreservesBothTiles(t *testing.T) {
	d := newGardenDaemon(t)
	_, _, workspaceID := setupMarkdownWorkspaceOn(t, d)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Concurrent seed"})
	path := filepath.Join(t.TempDir(), "concurrent.md")
	if err := os.WriteFile(path, []byte("# Concurrent"), 0o644); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _, err := d.openMarkdownTile(path, "session-1")
		errs <- err
	}()
	go func() {
		defer wg.Done()
		_, _, err := d.openSeedTile(seed.ID, "session-1")
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent open: %v", err)
		}
	}
	snapshot := d.store.GetWorkspaceLayout(workspaceID)
	leaves := workspacelayout.TileLeaves(snapshot.Layout)
	if len(leaves) != 2 || !workspacelayout.HasTile(snapshot.Layout, markdownTileIDForPath(path)) || !workspacelayout.HasTile(snapshot.Layout, seedTileIDForID(seed.ID)) {
		t.Fatalf("tiles after concurrent opens = %+v, want markdown and seed", leaves)
	}
}

func TestSeedAnnotationDraftUsesCanonicalSeedKey(t *testing.T) {
	d := newGardenDaemon(t)
	seed := plant(t, d, protocol.SeedPlantMessage{SourceSessionID: protocol.Ptr("sess-a"), Title: "Annotated seed"})
	client := newWorkspaceProtocolTestClient()
	d.handleMarkdownAnnotationsSave(client, &protocol.MarkdownAnnotationsSaveMessage{
		Cmd: protocol.CmdMarkdownAnnotationsSave, RequestID: "save-seed", Generation: 1,
		DocumentUri: seedDocumentURI(seed.ID), SourceKind: annotationSourceSeed, SeedID: &seed.ID,
	})
	var result protocol.MarkdownAnnotationsSaveResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if !result.Success || protocol.Deref(result.SeedID) != seed.ID {
		t.Fatalf("save result = %+v", result)
	}
	draft, err := d.store.GetMarkdownAnnotationDraft(seedDocumentURI(seed.ID))
	if err != nil || draft.Generation != 1 {
		t.Fatalf("seed draft = %+v, err=%v", draft, err)
	}
}

func TestAnnotationSourceRejectsMismatchedDocumentURI(t *testing.T) {
	d := newMarkdownAnnotationsDaemon(t)
	_, err := d.resolveAnnotationDocumentSource("attn://seed/s-wrong", annotationSourceFile,
		protocol.Ptr("workspace a"), protocol.Ptr("/tmp/a b.md"), nil)
	if err == nil || !strings.Contains(err.Error(), "does not match typed file source") {
		t.Fatalf("error = %v, want URI mismatch", err)
	}
	want := "attn://file/workspace%20a/%2Ftmp%2Fa%20b.md"
	if got := fileDocumentURI("workspace a", "/tmp/a b.md"); got != want {
		t.Fatalf("fileDocumentURI = %q, want %q", got, want)
	}
	want = "attn://file/work%2F%C3%A9/%2Ftmp%2Fcaf%C3%A9%20!(x).md"
	if got := fileDocumentURI("work/é", "/tmp/café !(x).md"); got != want {
		t.Fatalf("unicode fileDocumentURI = %q, want %q", got, want)
	}
}

func TestFormatMarkdownAnnotationPayloadNamesSeed(t *testing.T) {
	payload := formatMarkdownAnnotationPayload(annotationDocumentSource{
		kind: annotationSourceSeed, seedID: "s-abc123", seedTitle: "Reader",
	}, []protocol.MarkdownAnnotation{{ID: "g", Type: markdownAnnotationTypeGlobal, Text: protocol.Ptr("note")}}, nil)
	if !strings.Contains(payload, "Seed: s-abc123 — Reader") {
		t.Fatalf("payload does not identify seed:\n%s", payload)
	}
}
