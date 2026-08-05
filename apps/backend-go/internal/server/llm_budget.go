package server

import (
	"errors"
	"fmt"
	"time"
)

// Nothing counted requests per day, so the app only learned the daily quota was
// gone by getting a 429 in the middle of something the user was waiting on. The
// per-minute limiter cannot help here: a free key's daily cap is spent slowly,
// over hours, well inside every per-minute budget.
//
// So we count. The budget is deliberately set below the provider's real cap: the
// point is to run out on our own terms — degrading to the offline scorer with an
// honest warning — rather than discovering the wall by hitting it.

// llmDefaultRequestsPerDay leaves headroom under Google AI Studio's free-tier
// daily allowance, which is what the app ships pointed at. It is a budget, not a
// measurement of the provider's limit: the provider's own 429 remains the
// backstop, and errQuotaBudgetSpent is what we aim to hit first.
const llmDefaultRequestsPerDay = 200

// llmBudgetWarnRatio is where we start saying so in the logs, while there is
// still enough left for the user to decide what to spend it on.
const llmBudgetWarnRatio = 0.8

// errQuotaBudgetSpent is classified exactly like a provider-reported exhausted
// quota, so every path that already degrades honestly — offline job scoring, the
// search banner, the Resume Studio error — handles it with no further wiring.
var errQuotaBudgetSpent = errors.New("daily AI request budget spent")

func llmRequestsPerDay(config appConfig) int {
	if configured := config.Form.LLMRequestsPerDay; configured > 0 {
		return configured
	}
	if isLocalProvider(config.Form.Provider) {
		// A local model has no daily allowance to protect.
		return 0
	}
	return llmDefaultRequestsPerDay
}

// spendDailyRequest books one provider call against today's budget. It returns
// errQuotaBudgetSpent when the budget is already gone, before any request is
// made — which is the whole point: the call that would have been refused is
// never sent.
func (a *api) spendDailyRequest(config appConfig, now time.Time) error {
	budget := llmRequestsPerDay(config)
	if budget <= 0 {
		return nil
	}

	if spent := a.configStore.llmRequestsToday(now); spent >= budget {
		// The warn-at-80% line only fires while we are still spending. Once the
		// budget refuses a call, that line is in the past and the user needs to be
		// told again — otherwise the app just gets quietly worse at its job.
		a.logs.add("warning", fmt.Sprintf(
			"[ LLM ] orçamento diário do app esgotado (%d/%d). Aumente llmRequestsPerDay nas configurações ou aguarde amanhã.",
			spent, budget))
		return fmt.Errorf("%w: %d/%d requests today", errQuotaBudgetSpent, spent, budget)
	}

	spent := a.configStore.recordLLMRequest(now)
	if spent == int(float64(budget)*llmBudgetWarnRatio) {
		a.logs.add("warning", fmt.Sprintf(
			"[ LLM ] %d/%d requests de IA usados hoje. Ao esgotar, a busca passa a pontuar offline.",
			spent, budget))
	}
	if spent >= budget {
		a.logs.add("warning", fmt.Sprintf(
			"[ LLM ] orçamento diário de IA esgotado (%d/%d). Pontuação offline até amanhã.",
			spent, budget))
	}
	return nil
}
