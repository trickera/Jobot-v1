package server

import (
	"net/http"
	"time"
)

type aiUsageResponse struct {
	Day              string                 `json:"day"`
	Mode             string                 `json:"mode"`
	Consent          bool                   `json:"consent"`
	Requests         int                    `json:"requests"`
	CacheHits        int                    `json:"cacheHits"`
	Budget           int                    `json:"budget"`
	Remaining        int                    `json:"remaining"`
	OperationBudgets map[string]int         `json:"operationBudgets"`
	Breakdown        []llmUsageBreakdown    `json:"breakdown"`
	ModelValidation  *modelValidationStatus `json:"modelValidation,omitempty"`
}

func (a *api) aiUsage(w http.ResponseWriter, _ *http.Request) {
	config, err := a.configStore.load()
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível carregar o uso de IA."})
		return
	}

	now := time.Now()
	breakdown := a.configStore.llmUsageToday(now)
	cacheHits := 0
	for _, item := range breakdown {
		cacheHits += item.CacheHits
	}
	requests := a.configStore.llmRequestsToday(now)
	budget := llmRequestsPerDay(config)
	remaining := budget - requests
	if budget <= 0 {
		remaining = 0
	} else if remaining < 0 {
		remaining = 0
	}
	if breakdown == nil {
		breakdown = []llmUsageBreakdown{}
	}

	writeJSON(w, http.StatusOK, aiUsageResponse{
		Day:       usageDay(now),
		Mode:      normalizeAIMode(config.Form.AIMode),
		Consent:   config.Form.AIDataConsent,
		Requests:  requests,
		CacheHits: cacheHits,
		Budget:    budget,
		Remaining: remaining,
		OperationBudgets: map[string]int{
			"job_score":       llmOperationRequestBudget(config.Form.AIMode, "job_score"),
			"resume_parse":    llmOperationRequestBudget(config.Form.AIMode, "resume_parse"),
			"resume_gap":      llmOperationRequestBudget(config.Form.AIMode, "resume_gap"),
			"resume_optimize": llmOperationRequestBudget(config.Form.AIMode, "resume_optimize"),
			"cover_letter":    llmOperationRequestBudget(config.Form.AIMode, "cover_letter"),
		},
		Breakdown:       breakdown,
		ModelValidation: config.ModelValidation,
	})
}
