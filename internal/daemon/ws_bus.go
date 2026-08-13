package daemon

import (
	"strings"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/protocol"
)

// The app's window onto the event bus, answering with the same bus.Status that
// `attn bus status` renders. The daemon adds nothing of its own: one
// computation, two presentations, so the CLI and the app can never disagree
// about whether a consumer is behind or a producer is loud.
//
// Unlike the CLI's, this snapshot comes from the process that owns the delivery
// loops, so it can also report which consumers are actually running and what
// their handlers are failing on.

// handleBusStatusGet answers on the asking client alone: it is one window's
// diagnostic question, and the answer changes nothing any other window shows.
//
// The aggregate walks the whole log (209ms at 945k rows, measured), so it runs
// on its own goroutine rather than blocking the socket reader.
func (d *Daemon) handleBusStatusGet(client *wsClient, msg *protocol.BusStatusGetMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdBusStatusGet, "bus_status_get is missing a request id")
		return
	}
	go func() {
		result := protocol.BusStatusResultMessage{
			Event:     protocol.EventBusStatusResult,
			RequestID: requestID,
		}
		status, err := d.BusStatus()
		if err != nil {
			result.Error = protocol.Ptr(err.Error())
			d.sendToClient(client, result)
			return
		}
		fillBusStatusResult(&result, status)
		result.Success = true
		d.sendToClient(client, result)
	}()
}

// handleBusSetConsumerEnabled flips the kill switch the CLI writes.
//
// It writes the database row directly rather than asking the running bus,
// because that bit is database-only BY DESIGN — delivery re-reads it every
// cycle, so the switch works whether or not the daemon is healthy. Going
// through the daemon here would be a second mechanism for the same bit.
func (d *Daemon) handleBusSetConsumerEnabled(client *wsClient, msg *protocol.BusSetConsumerEnabledMessage) {
	requestID := strings.TrimSpace(msg.RequestID)
	name := strings.TrimSpace(msg.Consumer)
	result := protocol.BusSetConsumerEnabledResultMessage{
		Event:     protocol.EventBusSetConsumerEnabledResult,
		RequestID: requestID,
		Consumer:  name,
	}
	if requestID == "" {
		d.sendCommandError(client, protocol.CmdBusSetConsumerEnabled, "bus_set_consumer_enabled is missing a request id")
		return
	}
	if name == "" {
		result.Error = protocol.Ptr("bus_set_consumer_enabled: consumer is required")
		d.sendToClient(client, result)
		return
	}
	if d.store == nil {
		result.Error = protocol.Ptr("bus_set_consumer_enabled: no store")
		d.sendToClient(client, result)
		return
	}
	if _, ok, err := d.store.GetBusConsumer(name); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	} else if !ok {
		result.Error = protocol.Ptr("no consumer named " + name)
		d.sendToClient(client, result)
		return
	}
	if _, err := d.store.SetBusConsumerEnabled(name, msg.Enabled, time.Now()); err != nil {
		result.Error = protocol.Ptr(err.Error())
		d.sendToClient(client, result)
		return
	}
	verb := "disabled"
	if msg.Enabled {
		verb = "enabled"
	}
	d.logf("bus: consumer %s %s from the app", name, verb)
	result.Success = true
	d.sendToClient(client, result)
}

// fillBusStatusResult copies the snapshot onto the wire message. Every derived
// number is already computed; nothing here decides anything.
func fillBusStatusResult(out *protocol.BusStatusResultMessage, s bus.Status) {
	out.Earliest = int(s.Earliest)
	out.Head = int(s.Head)
	out.Rows = int(s.Rows)
	out.Bytes = int(s.Bytes)
	out.OldestAt = formatBusStamp(s.OldestAt)
	out.NewestAt = formatBusStamp(s.NewestAt)
	out.Delivering = s.Delivering
	out.RetentionSeconds = s.RetentionWindow.Seconds()
	out.RecentWindowSeconds = bus.RecentWindow.Seconds()
	out.BaselineWindowSeconds = bus.BaselineWindow.Seconds()
	out.SurgeRatePerHour = bus.SurgeRatePerHour
	out.PinAlarmSeconds = s.PinAlarmAge.Seconds()

	out.Producers = make([]protocol.BusProducerStatus, 0, len(s.Producers))
	for _, p := range s.Producers {
		out.Producers = append(out.Producers, protocol.BusProducerStatus{
			Name:               p.Name,
			Events:             int(p.Events),
			Bytes:              int(p.Bytes),
			Subjects:           int(p.Subjects),
			Share:              p.Share,
			RecentPerHour:      p.RecentPerHour,
			BaselinePerHour:    p.BaselinePerHour,
			SustainedPerHour:   p.SustainedPerHour,
			Surging:            p.Surging,
			SurgeWindowSeconds: p.SurgeWindow.Seconds(),
			SurgePerHour:       p.SurgePerHour,
		})
	}
	out.Consumers = make([]protocol.BusConsumerStatus, 0, len(s.Consumers))
	for _, c := range s.Consumers {
		out.Consumers = append(out.Consumers, protocol.BusConsumerStatus{
			Name:                c.Name,
			Cursor:              int(c.Cursor),
			Lag:                 int(c.Lag),
			Filter:              c.Filter,
			Enabled:             c.Enabled,
			UpdatedAt:           formatBusStamp(c.UpdatedAt),
			Live:                c.Live,
			Stalled:             c.Stalled,
			OldestUnreadAt:      formatBusStamp(c.OldestUnreadAt),
			HoldsRetentionFloor: c.HoldsRetentionFloor,
			PinAlarm:            c.PinAlarm,
			PinnedBytes:         int(c.PinnedBytes),
		})
	}
	out.Health = make([]protocol.BusHealthEntry, 0, len(s.Health))
	for _, h := range s.Health {
		out.Health = append(out.Health, protocol.BusHealthEntry{
			Level:   h.Level,
			Kind:    h.Kind,
			Subject: h.Subject,
			Message: h.Message,
		})
	}
}

func formatBusStamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
