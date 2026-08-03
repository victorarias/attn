package daemon

// The client-facing half of the kitty image feed. The worker observes and
// stores; this translates its observations into the protocol clients speak, and
// answers their pulls for the pixels behind a placement.
//
// Two rules run through everything here. Placement traffic is gated on
// CapabilityKittyImages, because a client that cannot draw an image has nothing
// to do with the description and the events would be pure noise on its socket.
// And nothing is correlated by request id: an answer names (session, image id,
// generation), which is the key a client's blob cache uses, so a duplicate is an
// idempotent refill rather than an orphan.

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
//
// The result is never nil, unlike attachBlocksToProtocol's: kitty_placements
// declares the array required precisely so the empty set survives the trip, and
// a nil slice would marshal to null and hand the client something its type says
// cannot happen. The empty set is the only message that says "stop drawing".
//
// attach_result.snapshot uses the same converter and reaches the opposite
// result, correctly: that field is optional, so its omitempty drops the empty
// slice. A restore has no prior placements to clear — the client is resetting
// its model — so there absence and emptiness really are the same thing.
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

// kittyImageFormatCode translates ghostty's pixel layout to the protocol's own
// code. Explicit rather than a cast: ghostty's values are a declaration order,
// and a pin that reorders them would otherwise silently re-label every client's
// pixels as a different layout.
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

// handleGetKittyImage answers a client's pull for the pixels behind a
// placement. Capable clients get binary frame 0x02; everyone else gets the same
// image base64'd into kitty_image_result, so an automation client can assert on
// an image without implementing the frame.
//
// Every failure is an ordinary answer with success=false — an evicted or
// unknown id, a backend that cannot serve images at all — because none of them
// mean the session is broken, and the client's move in all cases is the same:
// drop that placement's render until something re-describes it.
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
		format, len(image.Data), client.HasCapability(protocol.CapabilityKittyImages),
	)

	if client.HasCapability(protocol.CapabilityKittyImages) {
		frame, err := protocol.EncodeKittyImageFrame(
			msg.ID, image.ID, image.Generation, image.Width, image.Height, format, image.Data,
		)
		if err != nil {
			d.sendKittyImageFailure(client, msg.ID, msg.ImageID, err.Error())
			return
		}
		// Blocking, like PTY output: a blob is megabytes and one message, so a
		// slow client is better made to wait than handed a placement whose
		// pixels never arrive.
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

// sendKittyImageFailure answers a pull that produced no pixels. The message
// always names the image id: the client asked by id and correlates by id, and
// an error that omits it cannot be matched to the placement it kills.
func (d *Daemon) sendKittyImageFailure(client *wsClient, sessionID string, imageID int, reason string) {
	d.sendToClient(client, protocol.KittyImageResultMessage{
		Event:   protocol.EventKittyImageResult,
		ID:      sessionID,
		ImageID: imageID,
		Success: false,
		Error:   protocol.Ptr(fmt.Sprintf("kitty image %d: %s", imageID, reason)),
	})
}
