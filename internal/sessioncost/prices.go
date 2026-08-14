package sessioncost

// Built-in prices are standard global API list prices in USD per million
// tokens. Each receipt was checked 2026-08-15.
var builtInRateCards = map[string]RateCard{
	// Source: https://platform.claude.com/docs/en/about-claude/pricing
	// Claude's table distinguishes 5-minute and 1-hour cache writes.
	"claude-fable-5":            anthropicRates(10, 50),
	"claude-opus-5":             anthropicRates(5, 25),
	"claude-opus-4-8":           anthropicRates(5, 25),
	"claude-opus-4-6":           anthropicRates(5, 25),
	"claude-sonnet-5":           anthropicRates(2, 10),
	"claude-sonnet-4-6":         anthropicRates(3, 15),
	"claude-haiku-4-5":          anthropicRates(1, 5),
	"claude-haiku-4-5-20251001": anthropicRates(1, 5),

	// Sources, checked 2026-08-15:
	// https://developers.openai.com/api/docs/models/gpt-5-codex
	// https://developers.openai.com/api/docs/models/gpt-5.4-mini
	// https://developers.openai.com/api/docs/models/gpt-5.5
	// These models bill cache reads separately and list no cache-write charge.
	"gpt-5-codex":  openAIRates(1.25, 10, 0.125, 0),
	"gpt-5.4-mini": openAIRates(0.75, 4.5, 0.075, 0),
	"gpt-5.5":      openAIRates(5, 30, 0.5, 0),

	// Sources, checked 2026-08-15:
	// https://openai.com/index/advancing-the-price-performance-frontier-with-gpt-5-6/
	// https://developers.openai.com/api/docs/guides/latest-model
	// The July 30 prices supersede older model-page values for Terra and Luna;
	// GPT-5.6 cache reads cost 10% and explicit writes cost 125% of input.
	"gpt-5.6-sol":   openAIRates(5, 30, 0.5, 6.25),
	"gpt-5.6-terra": openAIRates(2, 12, 0.2, 2.5),
	"gpt-5.6-luna":  openAIRates(0.2, 1.2, 0.02, 0.25),
}

func anthropicRates(input, output float64) RateCard {
	return RateCard{
		InputUSDPerMTok:        input,
		OutputUSDPerMTok:       output,
		CacheReadUSDPerMTok:    input * 0.1,
		CacheWrite5mUSDPerMTok: input * 1.25,
		CacheWrite1hUSDPerMTok: input * 2,
	}
}

func openAIRates(input, output, cacheRead, cacheWrite float64) RateCard {
	return RateCard{
		InputUSDPerMTok:        input,
		OutputUSDPerMTok:       output,
		CacheReadUSDPerMTok:    cacheRead,
		CacheWrite5mUSDPerMTok: cacheWrite,
		CacheWrite1hUSDPerMTok: cacheWrite,
	}
}
