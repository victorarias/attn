package sessioncost

import (
	"math"
	"testing"
)

func TestLedgerAdd(t *testing.T) {
	ledger := Ledger{}
	if !ledger.Add(" gpt-5.5 ", Usage{InputTokens: 10, CacheReadInputTokens: 20}) {
		t.Fatal("first usage was not added")
	}
	if !ledger.Add("gpt-5.5", Usage{OutputTokens: 3, CacheWrite5mInputTokens: 4}) {
		t.Fatal("second usage was not added")
	}
	if got := ledger["gpt-5.5"]; got != (Usage{
		InputTokens: 10, OutputTokens: 3, CacheReadInputTokens: 20, CacheWrite5mInputTokens: 4,
	}) {
		t.Fatalf("usage = %+v", got)
	}
	if ledger.Add("gpt-5.5", Usage{}) {
		t.Fatal("empty usage reported a change")
	}
	if ledger.Add("gpt-5.5", Usage{InputTokens: -1}) {
		t.Fatal("negative usage was accepted")
	}
}

func TestLedgerAddRejectsOverflowAtomically(t *testing.T) {
	ledger := Ledger{"model": {InputTokens: math.MaxInt64, OutputTokens: 7}}
	if ledger.Add("model", Usage{InputTokens: 1, OutputTokens: 2}) {
		t.Fatal("overflowing usage was accepted")
	}
	if got := ledger["model"]; got.InputTokens != math.MaxInt64 || got.OutputTokens != 7 {
		t.Fatalf("overflow partially changed usage: %+v", got)
	}
}

func TestUsageValueArithmetic(t *testing.T) {
	base := Usage{
		InputTokens:             10,
		OutputTokens:            20,
		CacheReadInputTokens:    30,
		CacheWrite5mInputTokens: 40,
		CacheWrite1hInputTokens: 50,
	}
	delta := Usage{
		InputTokens:             1,
		OutputTokens:            2,
		CacheReadInputTokens:    3,
		CacheWrite5mInputTokens: 4,
		CacheWrite1hInputTokens: 5,
	}
	want := Usage{
		InputTokens:             11,
		OutputTokens:            22,
		CacheReadInputTokens:    33,
		CacheWrite5mInputTokens: 44,
		CacheWrite1hInputTokens: 55,
	}
	if got := base.Add(delta); got != want {
		t.Fatalf("Add() = %+v, want %+v", got, want)
	}
	if got := want.Subtract(delta); got != base {
		t.Fatalf("Subtract() = %+v, want %+v", got, base)
	}
	if !base.HasUsage() || (Usage{}).HasUsage() || (Usage{InputTokens: -1}).HasUsage() {
		t.Fatal("HasUsage did not distinguish valid traffic from empty or invalid usage")
	}
}

func TestUsageValueArithmeticMarksImpossibleResultsInvalid(t *testing.T) {
	tests := []struct {
		name string
		got  Usage
	}{
		{name: "overflow", got: (Usage{InputTokens: math.MaxInt64}).Add(Usage{InputTokens: 1})},
		{name: "underflow", got: (Usage{OutputTokens: 1}).Subtract(Usage{OutputTokens: 2})},
		{name: "negative operand", got: (Usage{}).Add(Usage{InputTokens: -1})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usd, known, hasUsage := Price(Ledger{"model": tt.got}, map[string]string{
				SessionCostPricePrefix + "model": `{"input_usd_per_mtok":1,"output_usd_per_mtok":1,"cache_read_usd_per_mtok":1,"cache_write_5m_usd_per_mtok":1,"cache_write_1h_usd_per_mtok":1}`,
			})
			if usd != 0 || known || !hasUsage {
				t.Fatalf("Price() = %v, %v, %v; want 0, false, true", usd, known, hasUsage)
			}
		})
	}
}

func TestPriceUsesEveryRateAndSeparatesCacheWrites(t *testing.T) {
	ledger := Ledger{
		"custom-model": {
			InputTokens:             1_000_000,
			OutputTokens:            1_000_000,
			CacheReadInputTokens:    1_000_000,
			CacheWrite5mInputTokens: 1_000_000,
			CacheWrite1hInputTokens: 1_000_000,
		},
	}
	settings := map[string]string{
		SessionCostPricePrefix + "custom-model": `{
			"input_usd_per_mtok":1,
			"output_usd_per_mtok":2,
			"cache_read_usd_per_mtok":3,
			"cache_write_5m_usd_per_mtok":4,
			"cache_write_1h_usd_per_mtok":5
		}`,
	}
	usd, known, hasUsage := Price(ledger, settings)
	if !known || !hasUsage || usd != 15 {
		t.Fatalf("Price() = %v, %v, %v; want 15, true, true", usd, known, hasUsage)
	}
}

func TestPriceOverflowIsUnknown(t *testing.T) {
	settings := map[string]string{
		SessionCostPricePrefix + "huge-model": `{
			"input_usd_per_mtok":1e308,
			"output_usd_per_mtok":0,
			"cache_read_usd_per_mtok":0,
			"cache_write_5m_usd_per_mtok":0,
			"cache_write_1h_usd_per_mtok":0
		}`,
	}
	usd, known, hasUsage := Price(Ledger{"huge-model": {InputTokens: 2}}, settings)
	if usd != 0 || known || !hasUsage {
		t.Fatalf("Price() = %v, %v, %v; want 0, false, true", usd, known, hasUsage)
	}
}

func TestPriceBuiltInCacheRates(t *testing.T) {
	tests := []struct {
		name  string
		model string
		usage Usage
		want  float64
	}{
		{
			name:  "Claude five-minute and one-hour writes",
			model: "claude-opus-5",
			usage: Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadInputTokens: 1_000_000, CacheWrite5mInputTokens: 1_000_000, CacheWrite1hInputTokens: 1_000_000},
			want:  46.75,
		},
		{
			name:  "Codex cached input is not charged at input rate",
			model: "gpt-5.6-luna",
			usage: Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000, CacheReadInputTokens: 1_000_000},
			want:  1.42,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			usd, known, hasUsage := Price(Ledger{tt.model: tt.usage}, nil)
			if !known || !hasUsage || math.Abs(usd-tt.want) > 1e-12 {
				t.Fatalf("Price() = %.12f, %v, %v; want %.12f, true, true", usd, known, hasUsage, tt.want)
			}
		})
	}
}

func TestBuiltInCoverageForObservedModelIDs(t *testing.T) {
	priced := []string{
		"claude-fable-5",
		"claude-haiku-4-5-20251001",
		"claude-opus-4-6",
		"claude-opus-4-8",
		"claude-opus-5",
		"claude-sonnet-5",
		"gpt-5-codex",
		"gpt-5.4-mini",
		"gpt-5.5",
		"gpt-5.6-luna",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
	}
	for _, model := range priced {
		t.Run(model, func(t *testing.T) {
			_, known, hasUsage := Price(Ledger{model: {InputTokens: 1}}, nil)
			if !known || !hasUsage {
				t.Fatalf("Price(%q) known, hasUsage = %v, %v; want true, true", model, known, hasUsage)
			}
		})
	}

	for _, model := range []string{"<synthetic>", "codex-auto-review"} {
		t.Run(model+" remains unpriced", func(t *testing.T) {
			usd, known, hasUsage := Price(Ledger{model: {InputTokens: 1}}, nil)
			if usd != 0 || known || !hasUsage {
				t.Fatalf("Price(%q) = %v, %v, %v; want 0, false, true", model, usd, known, hasUsage)
			}
		})
	}
}

func TestPriceStatesAndOverrides(t *testing.T) {
	t.Run("no usage", func(t *testing.T) {
		usd, known, hasUsage := Price(nil, nil)
		if usd != 0 || known || hasUsage {
			t.Fatalf("Price() = %v, %v, %v", usd, known, hasUsage)
		}
	})

	t.Run("unknown model poisons partial total", func(t *testing.T) {
		ledger := Ledger{
			"gpt-5.5":      {InputTokens: 1_000_000},
			"future-model": {OutputTokens: 1},
		}
		usd, known, hasUsage := Price(ledger, nil)
		if usd != 0 || known || !hasUsage {
			t.Fatalf("Price() = %v, %v, %v", usd, known, hasUsage)
		}
	})

	t.Run("unclassified cache write stays unknown", func(t *testing.T) {
		usd, known, hasUsage := Price(Ledger{
			"claude-opus-5": {UnclassifiedCacheWriteTokens: 1},
		}, nil)
		if usd != 0 || known || !hasUsage {
			t.Fatalf("Price() = %v, %v, %v", usd, known, hasUsage)
		}
	})

	t.Run("override corrects built-in", func(t *testing.T) {
		settings := map[string]string{
			SessionCostPricePrefix + "gpt-5.5": `{"input_usd_per_mtok":1,"output_usd_per_mtok":0,"cache_read_usd_per_mtok":0,"cache_write_5m_usd_per_mtok":0,"cache_write_1h_usd_per_mtok":0}`,
		}
		usd, known, hasUsage := Price(Ledger{"gpt-5.5": {InputTokens: 1_000_000}}, settings)
		if usd != 1 || !known || !hasUsage {
			t.Fatalf("Price() = %v, %v, %v", usd, known, hasUsage)
		}
	})

	t.Run("override fills gap", func(t *testing.T) {
		settings := map[string]string{
			SessionCostPricePrefix + "codex-auto-review": `{"input_usd_per_mtok":0,"output_usd_per_mtok":0,"cache_read_usd_per_mtok":0,"cache_write_5m_usd_per_mtok":0,"cache_write_1h_usd_per_mtok":0}`,
		}
		usd, known, hasUsage := Price(Ledger{"codex-auto-review": {InputTokens: 1}}, settings)
		if usd != 0 || !known || !hasUsage {
			t.Fatalf("Price() = %v, %v, %v", usd, known, hasUsage)
		}
	})
}

func TestParseOverridesIsCompleteAndStrict(t *testing.T) {
	valid := `{"input_usd_per_mtok":1,"output_usd_per_mtok":2,"cache_read_usd_per_mtok":3,"cache_write_5m_usd_per_mtok":4,"cache_write_1h_usd_per_mtok":5}`
	overrides, err := ParseOverrides(map[string]string{
		SessionCostPricePrefix + "model":   valid,
		SessionCostPricePrefix + "cleared": " ",
		"unrelated":                        "value",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(overrides) != 1 || overrides["model"].CacheWrite1hUSDPerMTok != 5 {
		t.Fatalf("overrides = %#v", overrides)
	}

	invalid := map[string]string{
		"missing field": `{"input_usd_per_mtok":1,"output_usd_per_mtok":2,"cache_read_usd_per_mtok":3,"cache_write_5m_usd_per_mtok":4}`,
		"unknown field": `{"input_usd_per_mtok":1,"output_usd_per_mtok":2,"cache_read_usd_per_mtok":3,"cache_write_5m_usd_per_mtok":4,"cache_write_1h_usd_per_mtok":5,"currency":"USD"}`,
		"negative":      `{"input_usd_per_mtok":-1,"output_usd_per_mtok":2,"cache_read_usd_per_mtok":3,"cache_write_5m_usd_per_mtok":4,"cache_write_1h_usd_per_mtok":5}`,
		"trailing":      valid + `{}`,
	}
	for name, raw := range invalid {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseOverrides(map[string]string{SessionCostPricePrefix + "model": raw}); err == nil {
				t.Fatalf("ParseOverrides(%s) succeeded", raw)
			}
		})
	}
}
