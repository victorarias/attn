package daemon

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// kittyImageBackend serves one image, or refuses in the way the caller picked.
type kittyImageBackend struct {
	*fakeSpawnBackend
	image pty.KittyImage
	err   error
}

func (b *kittyImageBackend) KittyImage(_ context.Context, _ string, imageID uint32) (pty.KittyImage, error) {
	if b.err != nil {
		return pty.KittyImage{}, b.err
	}
	if imageID != b.image.ID {
		return pty.KittyImage{}, fmt.Errorf("%w: image %d", pty.ErrKittyImageNotFound, imageID)
	}
	return b.image, nil
}

func kittyTestImage() pty.KittyImage {
	return pty.KittyImage{
		ID:         77,
		Width:      2,
		Height:     2,
		Format:     ghosttyvt.KittyImageRGB,
		Generation: 5,
		Data:       []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12},
	}
}

func kittyCapableClient() *wsClient {
	client := spawnTestClient()
	client.setIdentity("test", "v", []string{
		protocol.CapabilityWorkspaceSessions,
		protocol.CapabilityKittyImages,
	})
	return client
}

func kittyPlainClient() *wsClient {
	client := spawnTestClient()
	client.setIdentity("test", "v", []string{protocol.CapabilityWorkspaceSessions})
	return client
}

func readKittyImageResult(t *testing.T, client *wsClient) protocol.KittyImageResultMessage {
	t.Helper()
	select {
	case message := <-client.send:
		if message.kind != messageKindText {
			t.Fatalf("kitty_image_result arrived as kind %v, want text", message.kind)
		}
		var result protocol.KittyImageResultMessage
		if err := json.Unmarshal(message.payload, &result); err != nil {
			t.Fatalf("decode kitty_image_result: %v", err)
		}
		return result
	case <-time.After(time.Second):
		t.Fatal("no kitty_image_result was sent")
	}
	return protocol.KittyImageResultMessage{}
}

// The event family is the only thing that tells a client an image exists, and a
// client that cannot draw one has nothing to do with it. Sending anyway spams
// every automation client and relay with traffic they parse and drop; not
// sending to a capable client leaves the app blind to every image.
func TestKittyPlacementsReachOnlyClientsThatAskedForThem(t *testing.T) {
	capable := kittyCapableClient()
	plain := kittyPlainClient()

	for _, tc := range []struct {
		name      string
		client    *wsClient
		wantEvent bool
	}{
		{"capable client", capable, true},
		{"client without the capability", plain, false},
	} {
		d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
		t.Cleanup(func() { _ = d.store.Close() })
		stream := newFakeOutputStream()
		stream.events <- ptybackend.OutputEvent{
			Kind: ptybackend.OutputEventKindPlacements,
			Seq:  9,
			Placements: []pty.KittyPlacement{{
				ImageID:     77,
				PlacementID: 1,
				ViewportRow: 3,
				PixelWidth:  64,
				PixelHeight: 48,
			}},
		}
		_ = stream.Close()
		d.forwardPTYStreamEvents(tc.client, "sess-1", stream)

		select {
		case message := <-tc.client.send:
			if !tc.wantEvent {
				t.Fatalf("%s: received %q, want no placement traffic", tc.name, message.payload)
			}
			var event protocol.KittyPlacementsMessage
			if err := json.Unmarshal(message.payload, &event); err != nil {
				t.Fatalf("%s: decode kitty_placements: %v", tc.name, err)
			}
			if event.Event != protocol.EventKittyPlacements || event.ID != "sess-1" || event.Seq != 9 {
				t.Fatalf("%s: envelope = %+v", tc.name, event)
			}
			if len(event.Placements) != 1 || event.Placements[0].ImageID != 77 ||
				event.Placements[0].ViewportRow != 3 || event.Placements[0].PixelWidth != 64 {
				t.Fatalf("%s: placements = %+v", tc.name, event.Placements)
			}
		default:
			if tc.wantEvent {
				t.Fatalf("%s: no placement event was sent", tc.name)
			}
		}
	}
}

// The empty set is the one message that says "stop drawing". It has to survive
// serialization as a present, empty array: dropped by an omitempty, or turned
// into null by a nil slice, and the client keeps painting an image the session
// no longer has — forever, because nothing else is coming.
func TestKittyPlacementsEventCarriesTheEmptySet(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	client := kittyCapableClient()
	stream := newFakeOutputStream()
	stream.events <- ptybackend.OutputEvent{Kind: ptybackend.OutputEventKindPlacements, Seq: 12}
	_ = stream.Close()

	d.forwardPTYStreamEvents(client, "sess-1", stream)

	select {
	case message := <-client.send:
		if !strings.Contains(string(message.payload), `"placements":[]`) {
			t.Fatalf("payload = %s, want a present empty placements array", message.payload)
		}
		var event protocol.KittyPlacementsMessage
		if err := json.Unmarshal(message.payload, &event); err != nil {
			t.Fatalf("decode kitty_placements: %v", err)
		}
		if event.Placements == nil {
			t.Fatal("placements decoded to nil, want an empty array")
		}
		if event.Seq != 12 {
			t.Fatalf("seq = %d, want 12", event.Seq)
		}
	default:
		t.Fatal("the empty set was not sent")
	}
}

// Capable clients take the pixels as a binary frame — a measured real image is
// megabytes, and base64-in-JSON adds a third of that plus a parse stall on the
// UI thread.
func TestHandleGetKittyImageAnswersCapableClientsWithABinaryFrame(t *testing.T) {
	image := kittyTestImage()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &kittyImageBackend{fakeSpawnBackend: &fakeSpawnBackend{}, image: image}
	client := kittyCapableClient()

	d.handleGetKittyImage(client, &protocol.GetKittyImageMessage{ID: "sess-1", ImageID: 77})

	message := <-client.send
	if message.kind != messageKindBinary {
		t.Fatalf("message kind = %v, want binary", message.kind)
	}
	frame, err := protocol.DecodeKittyImageFrame(message.payload)
	if err != nil {
		t.Fatalf("decode kitty image frame: %v", err)
	}
	if frame.SessionID != "sess-1" || frame.ImageID != 77 || frame.Generation != 5 {
		t.Fatalf("frame identity = %+v", frame)
	}
	if frame.Width != 2 || frame.Height != 2 || frame.Format != protocol.KittyImageFormatCodeRGB {
		t.Fatalf("frame geometry = %+v", frame)
	}
	if string(frame.Pixels) != string(image.Data) {
		t.Fatalf("pixels = %x, want %x", frame.Pixels, image.Data)
	}
}

// Automation clients and relays never take a binary frame, so the same image
// has to be assertable as JSON — otherwise nothing outside the app can prove an
// image reached a session.
func TestHandleGetKittyImageAnswersPlainClientsWithBase64(t *testing.T) {
	image := kittyTestImage()
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &kittyImageBackend{fakeSpawnBackend: &fakeSpawnBackend{}, image: image}
	client := kittyPlainClient()

	d.handleGetKittyImage(client, &protocol.GetKittyImageMessage{ID: "sess-1", ImageID: 77})

	result := readKittyImageResult(t, client)
	if !result.Success {
		t.Fatalf("result failed: %v", protocol.Deref(result.Error))
	}
	if result.ID != "sess-1" || result.ImageID != 77 {
		t.Fatalf("result identity = %+v", result)
	}
	if protocol.Deref(result.Generation) != 5 || protocol.Deref(result.Width) != 2 ||
		protocol.Deref(result.Height) != 2 || protocol.Deref(result.Format) != "rgb" {
		t.Fatalf("result geometry = %+v", result)
	}
	pixels, err := base64.StdEncoding.DecodeString(protocol.Deref(result.DataB64))
	if err != nil {
		t.Fatalf("decode data_b64: %v", err)
	}
	if string(pixels) != string(image.Data) {
		t.Fatalf("pixels = %x, want %x", pixels, image.Data)
	}
}

// An evicted or unknown id is an ordinary answer, not a broken session — and
// the client correlates by id, so an error that does not name the id cannot be
// matched to the placement it kills.
func TestHandleGetKittyImageReportsAMissingImageByID(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &kittyImageBackend{fakeSpawnBackend: &fakeSpawnBackend{}, image: kittyTestImage()}

	for _, client := range []*wsClient{kittyCapableClient(), kittyPlainClient()} {
		d.handleGetKittyImage(client, &protocol.GetKittyImageMessage{ID: "sess-1", ImageID: 404})

		result := readKittyImageResult(t, client)
		if result.Success {
			t.Fatal("a missing image reported success")
		}
		if result.ImageID != 404 {
			t.Fatalf("result image id = %d, want 404", result.ImageID)
		}
		if !strings.Contains(protocol.Deref(result.Error), "404") {
			t.Fatalf("error = %q, want it to name image 404", protocol.Deref(result.Error))
		}
	}
}

// KittyImageProvider is optional, like SnapshotProvider: the embedded backend
// and a worker built before the method existed both have to answer something.
// Asserting the interface without checking is a nil-interface panic that takes
// the daemon down over a missing image.
func TestHandleGetKittyImageAnswersWhenTheBackendServesNoImages(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &fakeSpawnBackend{}
	client := kittyCapableClient()

	d.handleGetKittyImage(client, &protocol.GetKittyImageMessage{ID: "sess-1", ImageID: 77})

	result := readKittyImageResult(t, client)
	if result.Success {
		t.Fatal("a backend without the provider reported success")
	}
	if !strings.Contains(protocol.Deref(result.Error), "77") {
		t.Fatalf("error = %q, want it to name image 77", protocol.Deref(result.Error))
	}
}

// A backend failure that is not "no such image" still has to come back as an
// answer rather than silence, or the client waits on a pull that never lands.
func TestHandleGetKittyImageReportsBackendFailures(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &kittyImageBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		err:              errors.New("worker unreachable"),
	}
	client := kittyCapableClient()

	d.handleGetKittyImage(client, &protocol.GetKittyImageMessage{ID: "sess-1", ImageID: 77})

	result := readKittyImageResult(t, client)
	if result.Success {
		t.Fatal("an unreachable worker reported success")
	}
	if !strings.Contains(protocol.Deref(result.Error), "worker unreachable") {
		t.Fatalf("error = %q, want the backend's reason", protocol.Deref(result.Error))
	}
}

type placementAttachBackend struct {
	*fakeSpawnBackend
	info ptybackend.AttachInfo
}

func (b *placementAttachBackend) Attach(context.Context, string, string) (ptybackend.AttachInfo, ptybackend.Stream, error) {
	return b.info, newFakeOutputStream(), nil
}

// The VT dump has no images in it — the APC bytes were stripped from the stream
// long before it was serialized — so without the placements riding beside it, a
// detach/reattach silently loses every image the session was showing.
func TestAttachResultCarriesTheSnapshotPlacements(t *testing.T) {
	d := NewForTesting(filepath.Join(t.TempDir(), "test.sock"))
	t.Cleanup(func() { _ = d.store.Close() })
	d.ptyBackend = &placementAttachBackend{
		fakeSpawnBackend: &fakeSpawnBackend{},
		info: ptybackend.AttachInfo{
			Running:         true,
			Cols:            80,
			Rows:            24,
			GhosttySnapshot: []byte("vt-dump"),
			GhosttyPlacements: []pty.KittyPlacement{{
				ImageID:         77,
				PlacementID:     2,
				ImageGeneration: 5,
				ViewportRow:     7,
				ViewportCol:     1,
				PixelWidth:      640,
				PixelHeight:     480,
			}},
		},
	}
	client := kittyCapableClient()

	d.handleAttachSession(client, &protocol.AttachSessionMessage{ID: "sess-1"})

	var result protocol.AttachResultMessage
	readNotebookWSEvent(t, client.send, &result)
	if result.Snapshot == nil {
		t.Fatal("attach result carried no snapshot")
	}
	if len(result.Snapshot.Placements) != 1 {
		t.Fatalf("snapshot placements = %+v, want one", result.Snapshot.Placements)
	}
	got := result.Snapshot.Placements[0]
	if got.ImageID != 77 || got.PlacementID != 2 || got.ImageGeneration != 5 ||
		got.ViewportRow != 7 || got.ViewportCol != 1 ||
		got.PixelWidth != 640 || got.PixelHeight != 480 {
		t.Fatalf("snapshot placement = %+v", got)
	}
}

// A fresh placement carries kitty's natural size — ghostty resolves a cell
// footprint only on reflow — so the zeros have to reach the client as zeros. A
// daemon that "helpfully" derived cell counts would be running a second, wrong
// model of the grid beside the worker's.
func TestPlacementsToProtocolKeepsNaturalSizeZeros(t *testing.T) {
	out := placementsToProtocol([]pty.KittyPlacement{{
		ImageID:     77,
		PixelWidth:  640,
		PixelHeight: 480,
	}})
	if len(out) != 1 {
		t.Fatalf("converted %d placements, want 1", len(out))
	}
	if out[0].GridCols != 0 || out[0].GridRows != 0 {
		t.Fatalf("grid size = %dx%d, want the natural-size zeros", out[0].GridCols, out[0].GridRows)
	}
}
