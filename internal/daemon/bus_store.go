package daemon

import (
	"encoding/json"
	"time"

	"github.com/victorarias/attn/internal/bus"
	"github.com/victorarias/attn/internal/store"
)

// sqlBusStore satisfies the bus.Store seam.
var _ bus.Store = (*sqlBusStore)(nil)

// sqlBusStore adapts the profile SQLite store to the bus.Store seam. It lives in
// the daemon — which imports both internal/bus and internal/store — so neither of
// those packages depends on the other, the same arrangement sqlTaskStore uses for
// internal/jobs.
//
// The translation is mechanical: internal/store speaks BusEvent/BusConsumer rows,
// internal/bus speaks Event/Consumer values. Payloads cross as JSON text one way
// and json.RawMessage the other.
type sqlBusStore struct{ store *store.Store }

func (d *Daemon) newSQLBusStore() *sqlBusStore { return &sqlBusStore{store: d.store} }

// NewBusStore adapts a store the caller opened itself. `attn bus trim` uses it
// to run the same retention pass the daemon's tick runs, against the same
// policy, without a daemon: the pass is a database operation, and an operator
// must be able to run one whether or not the daemon is listening — the reason
// the whole `attn bus` surface is database-direct.
func NewBusStore(s *store.Store) bus.Store { return &sqlBusStore{store: s} }

func (a *sqlBusStore) Append(e bus.Event, now time.Time) (int64, error) {
	return a.store.AppendBusEvent(store.BusEvent{
		Name:    e.Name,
		Subject: e.Subject,
		Payload: string(e.Payload),
		Source:  e.Source,
	}, now)
}

func (a *sqlBusStore) Since(cursor int64, limit int) ([]bus.Event, error) {
	rows, err := a.store.BusEventsSince(cursor, limit)
	if err != nil {
		return nil, err
	}
	out := make([]bus.Event, 0, len(rows))
	for _, r := range rows {
		out = append(out, bus.Event{
			Seq:       r.Seq,
			Name:      r.Name,
			Subject:   r.Subject,
			Payload:   json.RawMessage(r.Payload),
			Source:    r.Source,
			CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}

func (a *sqlBusStore) Bounds() (int64, int64, error) { return a.store.BusBounds() }

func (a *sqlBusStore) GetConsumer(name string) (bus.Consumer, bool, error) {
	rec, ok, err := a.store.GetBusConsumer(name)
	if err != nil || !ok {
		return bus.Consumer{}, ok, err
	}
	return busConsumerFromRow(rec), true, nil
}

func (a *sqlBusStore) SaveConsumer(c bus.Consumer, now time.Time) error {
	return a.store.SaveBusConsumer(store.BusConsumer{
		Name:    c.Name,
		Cursor:  c.Cursor,
		Filter:  c.Filter,
		Enabled: c.Enabled,
	}, now)
}

func (a *sqlBusStore) DeleteConsumer(name string) error {
	return a.store.DeleteBusConsumer(name)
}

func (a *sqlBusStore) SetCursor(name string, cursor int64, now time.Time) error {
	return a.store.SetBusConsumerCursor(name, cursor, now)
}

func (a *sqlBusStore) ListConsumers() ([]bus.Consumer, error) {
	rows, err := a.store.ListBusConsumers()
	if err != nil {
		return nil, err
	}
	out := make([]bus.Consumer, 0, len(rows))
	for _, r := range rows {
		out = append(out, busConsumerFromRow(r))
	}
	return out, nil
}

func (a *sqlBusStore) Trim(cutoff time.Time) (int, error) { return a.store.TrimBusEvents(cutoff) }

func (a *sqlBusStore) Compact(names []string, floor int64) (int, error) {
	return a.store.CompactBusEvents(names, floor)
}

func (a *sqlBusStore) Producers(cutoffs []time.Time) ([]bus.ProducerRow, error) {
	rows, err := a.store.BusProducers(cutoffs)
	if err != nil {
		return nil, err
	}
	out := make([]bus.ProducerRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, bus.ProducerRow{
			Name:     r.Name,
			Events:   r.Events,
			Bytes:    r.Bytes,
			Subjects: r.Subjects,
			Recent:   r.Recent,
		})
	}
	return out, nil
}

func (a *sqlBusStore) EventTimeAt(seq int64) (time.Time, bool, error) {
	return a.store.BusEventTimeAt(seq)
}

func busConsumerFromRow(r store.BusConsumer) bus.Consumer {
	return bus.Consumer{
		Name:      r.Name,
		Cursor:    r.Cursor,
		Filter:    r.Filter,
		Enabled:   r.Enabled,
		UpdatedAt: r.UpdatedAt,
	}
}
