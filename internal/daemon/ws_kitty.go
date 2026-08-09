package daemon

// Client-facing half of the kitty image feed; the worker observes and stores.
// Two capabilities, kept apart for the relay: CapabilityKittyImages gates the
// kitty_placements fan-out, CapabilityBinaryPtyOutput decides only how a blob
// travels (frame 0x02 vs base64 JSON) — the hub relay wants the first without
// the second. Nothing is correlated by request id: answers name (session,
// image id, generation), so a duplicate is an idempotent refill, not an orphan.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/victorarias/attn/internal/ghosttyvt"
	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/pty"
	"github.com/victorarias/attn/internal/ptybackend"
)

// placementsToProtocol converts an observed placement set to its wire form.
// Never nil: the empty set is the only message that says "stop drawing", and a
// nil slice would marshal to null. (attach_result.snapshot's omitempty dropping
// it is fine — a restore has no prior placements to clear.)
func placementsToProtocol(placements []pty.KittyPlacement) []protocol.KittyPlacement {
	out := make([]protocol.KittyPlacement, len(placements))
	for i, p := range placements {
		out[i] = protocol.KittyPlacement{
			ImageID:         int(p.ImageID),
			PlacementID:     int(p.PlacementID),
			ImageGeneration: int(p.ImageGeneration),
			Virtual:         p.Virtual,
			Z:               int(p.Z),
			ViewportRow:     int(p.ViewportRow),
			ViewportCol:     int(p.ViewportCol),
			ViewportVisible: p.ViewportVisible,
			GridCols:        int(p.GridCols),
			GridRows:        int(p.GridRows),
			PixelWidth:      int(p.PixelWidth),
			PixelHeight:     int(p.PixelHeight),
			SourceX:         int(p.SourceX),
			SourceY:         int(p.SourceY),
			SourceWidth:     int(p.SourceWidth),
			SourceHeight:    int(p.SourceHeight),
		}
	}
	return out
}

// encodeKittyPlacementsMessage builds the outbound event for one placement set.
func encodeKittyPlacementsMessage(sessionID string, event ptybackend.OutputEvent) (outboundMessage, error) {
	payload, err := json.Marshal(protocol.KittyPlacementsMessage{
		Event:      protocol.EventKittyPlacements,
		ID:         sessionID,
		Seq:        int(event.Seq),
		Placements: placementsToProtocol(event.Placements),
	})
	if err != nil {
		return outboundMessage{}, err
	}
	return outboundMessage{kind: messageKindText, payload: payload}, nil
}

// kittyImageFormatCode translates ghostty's pixel layout to the protocol code.
// Explicit, not a cast: a pin that reorders ghostty's values would otherwise
// silently re-label every client's pixels.
func kittyImageFormatCode(format ghosttyvt.KittyImageFormat) (byte, bool) {
	switch format {
	case ghosttyvt.KittyImageRGB:
		return protocol.KittyImageFormatCodeRGB, true
	case ghosttyvt.KittyImageRGBA:
		return protocol.KittyImageFormatCodeRGBA, true
	case ghosttyvt.KittyImageGrayAlpha:
		return protocol.KittyImageFormatCodeGrayAlpha, true
	case ghosttyvt.KittyImageGray:
		return protocol.KittyImageFormatCodeGray, true
	}
	return 0, false
}

// handleGetKittyImage answers a client's pull for the pixels behind a placement.
// Asking is enough — only the transport is a capability decision. Every failure
// is an ordinary success=false answer (the session is not broken; the client
// drops that placement's render), sent as JSON to both kinds of client.
func (d *Daemon) handleGetKittyImage(client *wsClient, msg *protocol.GetKittyImageMessage) {
	provider, ok := d.ptyBackend.(ptybackend.KittyImageProvider)
	if !ok {
		d.sendKittyImageFailure(client, msg.ID, msg.ImageID, "pty backend serves no kitty images")
		return
	}

	image, err := provider.KittyImage(context.Background(), msg.ID, uint32(msg.ImageID))
	if err != nil {
		d.sendKittyImageFailure(client, msg.ID, msg.ImageID, err.Error())
		return
	}
	format, ok := kittyImageFormatCode(image.Format)
	if !ok {
		d.sendKittyImageFailure(client, msg.ID, msg.ImageID, fmt.Sprintf("unknown pixel format %d", image.Format))
		return
	}
	d.logf(
		"kitty image: id=%s image=%d generation=%d %dx%d format=%d bytes=%d binary=%v",
		msg.ID, image.ID, image.Generation, image.Width, image.Height,
		format, len(image.Data), client.HasCapability(protocol.CapabilityBinaryPtyOutput),
	)

	if client.HasCapability(protocol.CapabilityBinaryPtyOutput) {
		frame, err := protocol.EncodeKittyImageFrame(
			msg.ID, image.ID, image.Generation, image.Width, image.Height, format, image.Data,
		)
		if err != nil {
			d.sendKittyImageFailure(client, msg.ID, msg.ImageID, err.Error())
			return
		}
		// Blocking, like PTY output: better a slow client waits than a placement
		// whose pixels never arrive.
		if !d.sendOutboundBlocking(client, outboundMessage{kind: messageKindBinary, payload: frame}, ptyOutputSendWait) {
			d.logf("kitty image send failed: id=%s image=%d bytes=%d", msg.ID, image.ID, len(frame))
		}
		return
	}

	name, _ := protocol.KittyImageFormatName(format)
	d.sendToClient(client, protocol.KittyImageResultMessage{
		Event:      protocol.EventKittyImageResult,
		ID:         msg.ID,
		ImageID:    int(image.ID),
		Success:    true,
		Generation: protocol.Ptr(int(image.Generation)),
		Width:      protocol.Ptr(int(image.Width)),
		Height:     protocol.Ptr(int(image.Height)),
		Format:     protocol.Ptr(name),
		DataB64:    protocol.Ptr(base64.StdEncoding.EncodeToString(image.Data)),
	})
}

// sendKittyImageFailure answers a pull that produced no pixels; it always names
// the image id, which is what the client correlates by.
func (d *Daemon) sendKittyImageFailure(client *wsClient, sessionID string, imageID int, reason string) {
	d.sendToClient(client, protocol.KittyImageResultMessage{
		Event:   protocol.EventKittyImageResult,
		ID:      sessionID,
		ImageID: imageID,
		Success: false,
		Error:   protocol.Ptr(fmt.Sprintf("kitty image %d: %s", imageID, reason)),
	})
}
