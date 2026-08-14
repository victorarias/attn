package daemon

import (
	"strings"

	"github.com/victorarias/attn/internal/protocol"
	"github.com/victorarias/attn/internal/sessioncost"
)

func (d *Daemon) decorateSessionWithCost(session *protocol.Session) {
	if session == nil || d.store == nil {
		return
	}
	state, err := d.store.SessionCost(session.ID)
	if err != nil {
		return
	}
	if state.UsageUnavailable {
		session.CostUnknown = protocol.Ptr(true)
		return
	}
	usd, known, hasUsage := sessioncost.Price(state.Ledger, d.store.GetAllSettings())
	if !hasUsage {
		return
	}
	if !known {
		session.CostUnknown = protocol.Ptr(true)
		return
	}
	session.CostUsd = protocol.Ptr(usd)
}

func isSessionCostPriceSetting(key string) bool {
	return strings.HasPrefix(key, sessioncost.SessionCostPricePrefix)
}

func (d *Daemon) publishSessionCostReprices() {
	for _, session := range d.store.List("") {
		state, err := d.store.SessionCost(session.ID)
		if err != nil {
			continue
		}
		_, _, hasUsage := sessioncost.Price(state.Ledger, nil)
		if hasUsage || state.UsageUnavailable {
			d.publishFact(FactSessionCostChanged, session.ID, nil)
		}
	}
}
