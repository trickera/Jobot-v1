package server

import (
	"sort"
	"strings"
)

// A version-pinned model name is a dated fuse, and this one went off.
//
// Google retires a model for projects that have NOT used it yet while leaving it
// working for projects that have. gemini-2.5-flash — the model this app shipped
// as its default — still answered on the developer's own key and answered every
// key created after the cutoff with:
//
//	404 "This model models/gemini-2.5-flash is no longer available to new users."
//
// So the app worked for whoever built it and was dead on arrival for everyone
// who installed it, which is the hardest kind of breakage to see from the inside:
// no test fails, no log looks wrong, and the one machine that would have caught
// it is the one machine that cannot.
//
// The product specification pins the preferred Flash-Lite generation, but the
// pin is never trusted blindly: the first real use validates it against
// ListModels and visibly migrates to the curated Flash-Lite alias when absent.
// This keeps the requested deterministic default without recreating the 404
// failure that a previous, unvalidated pin caused.
//
// The LITE family is the default, and that is not a quality compromise made
// lightly — it is the only one a free key can actually afford.
// gemini-flash-latest currently resolves to gemini-3.5-flash, whose free tier is
// twenty requests a day:
//
//	"Quota exceeded ... limit: 20, model: gemini-3.5-flash"
//
// Twenty is less than a day's work. One pass through Resume Studio is four or five
// calls and one job search is three to five, so the app would stop answering after
// roughly four uses — while gemini-flash-lite-latest went on answering through an
// entire day of testing that exhausted the other two. A model that refuses is worth
// less than a lighter model that replies.
const (
	geminiPinnedFreeModel = "gemini-3.1-flash-lite"
	geminiLiteAlias       = "gemini-flash-lite-latest"
	geminiQualityAlias    = "gemini-flash-latest"
	geminiFreeModel       = geminiPinnedFreeModel
)

// retiredModels is deliberately evidence-based: each entry was observed to
// return 404 for a newly created key. Compatible names not present here remain
// untouched; prefix matching alone is not proof that a model was retired.
var retiredModels = map[string]string{
	"gemini-2.5-flash":      geminiFreeModel,
	"gemini-2.5-flash-lite": geminiFreeModel,
}

// geminiModelFallbacks are the other Gemini models to try when the configured one
// will not answer. Measured against a newly created AI Studio key on 2026-07-11,
// asking each model for one line of JSON:
//
//	gemini-flash-latest        ok
//	gemini-flash-lite-latest   ok
//	gemini-3-flash-preview     ok
//	gemini-2.5-flash           404 — retired for new keys
//	gemini-2.5-flash-lite      404 — retired for new keys
//	gemini-2.0-flash           429 — on a key with nothing spent
//	gemini-2.5-pro             429 — on a key with nothing spent
//	gemini-pro-latest          429 — on a key with nothing spent
//
// The 429s are the surprising ones: a brand-new key with zero usage is refused,
// which means those models' free-tier allowance is not spent but zero. Retrying
// them would burn the whole rate-limit ladder against a wall, so they are not
// here. Only models observed to actually answer are.
//
// The ORDER is by what a free key can afford, not by what answers best, because a
// model that refuses answers worst of all. A day of real use on one key settled it:
// gemini-flash-latest (which resolves to gemini-3.5-flash) ran out after twenty
// requests — "limit: 20, model: gemini-3.5-flash" — while the two below it were
// still answering. So the biggest allowance leads and the sharpest model is kept in
// reserve, for when the cheaper ones have nothing left.
var geminiFreeTierAllowlist = []string{
	geminiPinnedFreeModel,
	geminiLiteAlias,
	geminiQualityAlias,
}

// Preview/experimental models are deliberately absent even when ListModels
// reports them. Only the stable curated siblings can enter the runtime cascade.
var geminiModelFallbacks = []string{
	geminiLiteAlias,
	geminiQualityAlias,
}

func replacementForRetiredModel(provider, model string) (string, bool) {
	if !isGeminiProvider(provider) {
		return model, false
	}
	replacement, ok := retiredModels[strings.ToLower(strings.TrimSpace(model))]
	return replacement, ok
}

// rankGeminiModels keeps the live ListModels response authoritative while
// presenting the measured free-tier choices first. Unknown stable models are
// preserved after the curated entries; Settings decides how to present preview
// and experimental names.
func rankGeminiModels(models []string) []string {
	rank := make(map[string]int, len(geminiFreeTierAllowlist))
	for index, model := range geminiFreeTierAllowlist {
		rank[strings.ToLower(model)] = index
	}
	sort.SliceStable(models, func(i, j int) bool {
		left, leftKnown := rank[strings.ToLower(models[i])]
		right, rightKnown := rank[strings.ToLower(models[j])]
		if leftKnown != rightKnown {
			return leftKnown
		}
		if leftKnown {
			return left < right
		}
		return strings.ToLower(models[i]) < strings.ToLower(models[j])
	})
	return models
}

// modelFallbacksFor lists sibling models worth trying before giving up on a
// provider. Two failures make this worth the extra attempts, and both are free:
// the sibling needs no new API key, and on Gemini's free tier it has its own
// quota.
//
//   - The configured model was retired out from under a stored config (the 404
//     above). The user has no way to know which name is current, and no reason to.
//   - The configured model's daily allowance is gone. On the free tier the quota
//     is PER MODEL: measured on one key in one second, gemini-2.0-flash answered
//     429 while gemini-flash-lite-latest answered normally. So one model running
//     out says nothing about the next, and moving on multiplies what a free key
//     can actually do in a day.
//
// Only Gemini is listed. The other providers meter by account rather than by
// model, so a sibling model there buys nothing a retry would not.
func modelFallbacksFor(provider string) []string {
	if isGeminiProvider(provider) {
		return geminiModelFallbacks
	}
	return nil
}

func isGeminiProvider(provider string) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	return strings.Contains(provider, "gemini") || strings.Contains(provider, "google")
}
