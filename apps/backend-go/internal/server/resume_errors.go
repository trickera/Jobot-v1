package server

import (
	"context"
	"errors"
	"net"
	"net/http"
)

// Sentinel errors wrapped by lower layers (schema validation, AI JSON decode)
// so handlers can classify failures with errors.Is instead of string matching.
var (
	errMissingName       = errors.New("missing_name")
	errInvalidAIJSON     = errors.New("invalid_ai_json")
	errInvalidFile       = errors.New("invalid_file")
	errUnsupportedFormat = errors.New("unsupported_format")
	errFileTooLarge      = errors.New("file_too_large")
)

// resumeError is the user-facing error contract of every POST /api/v1/resume/*
// route: a stable machine-readable code plus an actionable English message.
// The Resume Studio UI is English-only; the rest of the app remains PT and
// does NOT use this type.
type resumeError struct {
	Status  int
	Code    string
	Message string
}

func resumeErrorFor(status int, code, message string) resumeError {
	return resumeError{Status: status, Code: code, Message: message}
}

func writeResumeError(w http.ResponseWriter, e resumeError) {
	writeJSON(w, e.Status, map[string]string{"code": e.Code, "message": e.Message})
}

// classifyResumeError maps an error returned by an AI-backed resume operation
// (parse/analyze-job/gap/optimize) to the typed user-facing contract above.
func classifyResumeError(err error) resumeError {
	switch {
	case errors.Is(err, errMissingName):
		return resumeErrorFor(http.StatusUnprocessableEntity, "missing_name",
			"We couldn't find your name on the resume. Make sure your full name appears at the top, or paste plain text. If you uploaded a PDF, try a simpler layout or check the extracted text below.")
	case errors.Is(err, errInvalidAIJSON):
		return resumeErrorFor(http.StatusBadGateway, "invalid_ai_json",
			"The AI returned an unreadable answer. Try again - if it keeps happening, switch the model in Settings.")
	case errors.Is(err, context.DeadlineExceeded), isNetTimeout(err):
		return resumeErrorFor(http.StatusGatewayTimeout, "ai_timeout",
			"The AI call timed out. Try again - large resumes can take longer.")
	}
	if errors.Is(err, errAIDataConsentRequired) {
		return resumeErrorFor(http.StatusConflict, "ai_consent_required",
			"Allow privacy-safe AI processing in Settings > AI before sharing resume data. Direct identifiers are redacted before every provider call.")
	}
	if errors.Is(err, errLLMOperationBudgetSpent) {
		return resumeErrorFor(http.StatusBadGateway, "ai_operation_budget_spent",
			"This AI operation reached its Free Tier request budget. Retry once, or switch to Free quality mode in Settings > AI for a larger operation budget.")
	}
	// Our own budget running out is not the provider refusing us: it is a number
	// the user chose and can raise. Saying "come back tomorrow" would cost them a
	// day over a setting they could change in one edit.
	if errors.Is(err, errQuotaBudgetSpent) {
		return resumeErrorFor(http.StatusBadGateway, "ai_budget_spent",
			"This app's daily AI request budget is used up. Raise the daily budget in Settings > AI to keep going, or wait for it to reset tomorrow.")
	}
	// Checked before the status switch: a spent daily allowance arrives as a 429
	// like any other, but telling the user to "wait a minute and try again" would
	// be a lie — it does not come back today.
	if isDailyQuotaError(err) {
		return resumeErrorFor(http.StatusBadGateway, "ai_quota_exhausted",
			"This API key's daily AI quota is spent. It resets tomorrow. To keep going now, add a different provider under Settings > AI.")
	}
	switch llmHTTPStatus(err) {
	case 404:
		// Reaching the user means the cascade already tried the other models on
		// this key and none of them answered either, so "pick another model" is
		// the only advice left — and it is advice they can act on, unlike the
		// generic failure this used to fall through to.
		return resumeErrorFor(http.StatusBadGateway, "ai_model_unavailable",
			"The AI model set in Settings no longer exists for your API key. Providers retire model names over time. Pick a current model in Settings > AI.")
	case 429:
		return resumeErrorFor(http.StatusBadGateway, "ai_rate_limited",
			"The AI provider is rate-limiting requests (quota). Wait a minute and try again, or switch provider/model in Settings.")
	case 401, 403:
		return resumeErrorFor(http.StatusBadGateway, "ai_key_invalid",
			"The AI provider rejected your key. Check the key in Settings > AI.")
	case 503:
		return resumeErrorFor(http.StatusBadGateway, "ai_unavailable",
			"The AI provider is unavailable right now. Try again in a few minutes.")
	}
	return resumeErrorFor(http.StatusBadGateway, "ai_unavailable",
		"The AI call failed. Try again - check the app logs if it keeps happening.")
}

func isNetTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}
