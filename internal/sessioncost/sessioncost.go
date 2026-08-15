// Package sessioncost owns provider-neutral token accounting and USD pricing.
package sessioncost

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strings"
)

// SessionCostPricePrefix prefixes an exact-model price override in settings.
// The suffix is the transcript's model id, without alias expansion.
const SessionCostPricePrefix = "session_cost.price."

// Usage is one model's billable token traffic. InputTokens excludes cache
// reads: adapters for providers whose input count includes cached tokens must
// subtract that subset before adding the usage to a Ledger.
type Usage struct {
	InputTokens                  int64 `json:"input_tokens"`
	OutputTokens                 int64 `json:"output_tokens"`
	CacheReadInputTokens         int64 `json:"cache_read_input_tokens"`
	CacheWrite5mInputTokens      int64 `json:"cache_write_5m_input_tokens"`
	CacheWrite1hInputTokens      int64 `json:"cache_write_1h_input_tokens"`
	UnclassifiedCacheWriteTokens int64 `json:"unclassified_cache_write_tokens"`
}

// HasUsage reports whether the value is valid and contains any token traffic.
func (u Usage) HasUsage() bool {
	return u.valid() && u.hasUsage()
}

// Add returns the component-wise sum. Invalid input or overflow produces an
// invalid value so Price reports unknown instead of silently undercharging.
func (u Usage) Add(other Usage) Usage {
	if !u.valid() || !other.valid() {
		return invalidUsage()
	}
	next, ok := addUsage(u, other)
	if !ok {
		return invalidUsage()
	}
	return next
}

// Subtract returns the component-wise difference. Invalid input or underflow
// produces an invalid value so Price reports unknown instead of silently
// undercharging.
func (u Usage) Subtract(other Usage) Usage {
	if !u.valid() || !other.valid() ||
		other.InputTokens > u.InputTokens ||
		other.OutputTokens > u.OutputTokens ||
		other.CacheReadInputTokens > u.CacheReadInputTokens ||
		other.CacheWrite5mInputTokens > u.CacheWrite5mInputTokens ||
		other.CacheWrite1hInputTokens > u.CacheWrite1hInputTokens ||
		other.UnclassifiedCacheWriteTokens > u.UnclassifiedCacheWriteTokens {
		return invalidUsage()
	}
	return Usage{
		InputTokens:                  u.InputTokens - other.InputTokens,
		OutputTokens:                 u.OutputTokens - other.OutputTokens,
		CacheReadInputTokens:         u.CacheReadInputTokens - other.CacheReadInputTokens,
		CacheWrite5mInputTokens:      u.CacheWrite5mInputTokens - other.CacheWrite5mInputTokens,
		CacheWrite1hInputTokens:      u.CacheWrite1hInputTokens - other.CacheWrite1hInputTokens,
		UnclassifiedCacheWriteTokens: u.UnclassifiedCacheWriteTokens - other.UnclassifiedCacheWriteTokens,
	}
}

// Ledger preserves usage by exact model id so a settings change can reprice a
// session and a model switch does not apply the new model's rate retroactively.
type Ledger map[string]Usage

// Add accumulates non-negative usage without partially applying an overflow.
// It returns false for empty, negative, overflowing, or nil-ledger input.
func (l Ledger) Add(model string, usage Usage) bool {
	if l == nil || !usage.hasUsage() || !usage.valid() {
		return false
	}
	model = strings.TrimSpace(model)
	current := l[model]
	if !current.valid() {
		return false
	}
	next, ok := addUsage(current, usage)
	if !ok {
		return false
	}
	l[model] = next
	return true
}

// RateCard prices each token category in USD per million tokens. A complete
// settings override supplies every field, including zero-priced categories.
type RateCard struct {
	InputUSDPerMTok        float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok       float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok    float64 `json:"cache_read_usd_per_mtok"`
	CacheWrite5mUSDPerMTok float64 `json:"cache_write_5m_usd_per_mtok"`
	CacheWrite1hUSDPerMTok float64 `json:"cache_write_1h_usd_per_mtok"`
}

// ParseOverrides decodes all non-blank exact-model overrides. It rejects an
// incomplete object rather than filling omitted categories with a free rate.
func ParseOverrides(settings map[string]string) (map[string]RateCard, error) {
	keys := make([]string, 0)
	for key := range settings {
		if strings.HasPrefix(key, SessionCostPricePrefix) {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)

	overrides := make(map[string]RateCard, len(keys))
	for _, key := range keys {
		model := strings.TrimSpace(strings.TrimPrefix(key, SessionCostPricePrefix))
		if model == "" {
			return nil, fmt.Errorf("session cost price setting %q has no model id", key)
		}
		raw := strings.TrimSpace(settings[key])
		if raw == "" {
			continue
		}
		card, err := parseRateCard(raw)
		if err != nil {
			return nil, fmt.Errorf("session cost price for %s: %w", model, err)
		}
		overrides[model] = card
	}
	return overrides, nil
}

// Price returns the ledger's standard USD list-price estimate. hasUsage is
// false only before any token usage exists. known is false when any used model
// has no rate or when persisted usage/settings are invalid; partial totals are
// never returned as if they were the whole session cost.
func Price(ledger Ledger, settings map[string]string) (usd float64, known bool, hasUsage bool) {
	for _, usage := range ledger {
		if usage.hasAnyValue() {
			hasUsage = true
			break
		}
	}
	if !hasUsage {
		return 0, false, false
	}

	overrides, err := ParseOverrides(settings)
	if err != nil {
		return 0, false, true
	}
	for model, usage := range ledger {
		if !usage.hasAnyValue() {
			continue
		}
		if !usage.valid() {
			return 0, false, true
		}
		// Claude transcripts that report only aggregate cache creation do not say
		// whether those writes used the 5-minute or 1-hour rate. Guessing would
		// violate the session-cost contract, so the entire total stays unknown.
		if usage.UnclassifiedCacheWriteTokens > 0 {
			return 0, false, true
		}
		card, ok := overrides[model]
		if !ok {
			card, ok = builtInRateCards[model]
		}
		if !ok {
			return 0, false, true
		}
		priced := priceUsage(usage, card)
		if math.IsNaN(priced) || math.IsInf(priced, 0) {
			return 0, false, true
		}
		usd += priced
		if math.IsNaN(usd) || math.IsInf(usd, 0) {
			return 0, false, true
		}
	}
	return usd, true, true
}

func (u Usage) hasUsage() bool {
	return u.InputTokens > 0 ||
		u.OutputTokens > 0 ||
		u.CacheReadInputTokens > 0 ||
		u.CacheWrite5mInputTokens > 0 ||
		u.CacheWrite1hInputTokens > 0 ||
		u.UnclassifiedCacheWriteTokens > 0
}

func (u Usage) hasAnyValue() bool {
	return u.InputTokens != 0 ||
		u.OutputTokens != 0 ||
		u.CacheReadInputTokens != 0 ||
		u.CacheWrite5mInputTokens != 0 ||
		u.CacheWrite1hInputTokens != 0 ||
		u.UnclassifiedCacheWriteTokens != 0
}

func (u Usage) valid() bool {
	return u.InputTokens >= 0 &&
		u.OutputTokens >= 0 &&
		u.CacheReadInputTokens >= 0 &&
		u.CacheWrite5mInputTokens >= 0 &&
		u.CacheWrite1hInputTokens >= 0 &&
		u.UnclassifiedCacheWriteTokens >= 0
}

func addUsage(a, b Usage) (Usage, bool) {
	values := [6][2]int64{
		{a.InputTokens, b.InputTokens},
		{a.OutputTokens, b.OutputTokens},
		{a.CacheReadInputTokens, b.CacheReadInputTokens},
		{a.CacheWrite5mInputTokens, b.CacheWrite5mInputTokens},
		{a.CacheWrite1hInputTokens, b.CacheWrite1hInputTokens},
		{a.UnclassifiedCacheWriteTokens, b.UnclassifiedCacheWriteTokens},
	}
	for _, pair := range values {
		if pair[1] > math.MaxInt64-pair[0] {
			return Usage{}, false
		}
	}
	return Usage{
		InputTokens:                  a.InputTokens + b.InputTokens,
		OutputTokens:                 a.OutputTokens + b.OutputTokens,
		CacheReadInputTokens:         a.CacheReadInputTokens + b.CacheReadInputTokens,
		CacheWrite5mInputTokens:      a.CacheWrite5mInputTokens + b.CacheWrite5mInputTokens,
		CacheWrite1hInputTokens:      a.CacheWrite1hInputTokens + b.CacheWrite1hInputTokens,
		UnclassifiedCacheWriteTokens: a.UnclassifiedCacheWriteTokens + b.UnclassifiedCacheWriteTokens,
	}, true
}

func invalidUsage() Usage {
	return Usage{InputTokens: -1}
}

type requiredRateCard struct {
	InputUSDPerMTok        *float64 `json:"input_usd_per_mtok"`
	OutputUSDPerMTok       *float64 `json:"output_usd_per_mtok"`
	CacheReadUSDPerMTok    *float64 `json:"cache_read_usd_per_mtok"`
	CacheWrite5mUSDPerMTok *float64 `json:"cache_write_5m_usd_per_mtok"`
	CacheWrite1hUSDPerMTok *float64 `json:"cache_write_1h_usd_per_mtok"`
}

func parseRateCard(raw string) (RateCard, error) {
	var decoded requiredRateCard
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&decoded); err != nil {
		return RateCard{}, err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return RateCard{}, err
	}
	if decoded.InputUSDPerMTok == nil || decoded.OutputUSDPerMTok == nil ||
		decoded.CacheReadUSDPerMTok == nil || decoded.CacheWrite5mUSDPerMTok == nil ||
		decoded.CacheWrite1hUSDPerMTok == nil {
		return RateCard{}, errors.New("override requires input, output, cache-read, 5-minute cache-write, and 1-hour cache-write rates")
	}
	card := RateCard{
		InputUSDPerMTok:        *decoded.InputUSDPerMTok,
		OutputUSDPerMTok:       *decoded.OutputUSDPerMTok,
		CacheReadUSDPerMTok:    *decoded.CacheReadUSDPerMTok,
		CacheWrite5mUSDPerMTok: *decoded.CacheWrite5mUSDPerMTok,
		CacheWrite1hUSDPerMTok: *decoded.CacheWrite1hUSDPerMTok,
	}
	for _, rate := range []float64{
		card.InputUSDPerMTok,
		card.OutputUSDPerMTok,
		card.CacheReadUSDPerMTok,
		card.CacheWrite5mUSDPerMTok,
		card.CacheWrite1hUSDPerMTok,
	} {
		if rate < 0 || math.IsNaN(rate) || math.IsInf(rate, 0) {
			return RateCard{}, errors.New("rates must be finite and non-negative")
		}
	}
	return card, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected data after override")
		}
		return err
	}
	return nil
}

func priceUsage(usage Usage, card RateCard) float64 {
	return (float64(usage.InputTokens)*card.InputUSDPerMTok +
		float64(usage.OutputTokens)*card.OutputUSDPerMTok +
		float64(usage.CacheReadInputTokens)*card.CacheReadUSDPerMTok +
		float64(usage.CacheWrite5mInputTokens)*card.CacheWrite5mUSDPerMTok +
		float64(usage.CacheWrite1hInputTokens)*card.CacheWrite1hUSDPerMTok) / 1_000_000
}
