package server

import (
	"net/http"
	"strings"
)

// searchPlanResponse is the answer to "what is this search actually going to
// do?", shown before the user presses Search.
//
// It exists because the 2026-07-13 run produced a search whose every visible
// setting said "Registered Nurse, on-site, Chicago" and whose every actual
// request said "United States, remote-only, scored on AWS and Terraform". The
// user had no way to see the difference. Nothing here is new information — it is
// the same effectiveSearchConfig the scraper runs on, rendered honestly.
type searchPlanResponse struct {
	Roles       []string `json:"roles"`
	RolesSource string   `json:"rolesSource"` // "profiles" | "role"
	// IgnoredRoles is non-empty exactly when the advanced profiles have overridden
	// the simple target role. The UI must show a persistent warning and offer both
	// "use the simple role" and "clear the advanced profiles" (UX-001).
	IgnoredRoles []string `json:"ignoredRoles,omitempty"`

	Levels         []string `json:"levels"`
	ExcludedLevels []string `json:"excludedLevels,omitempty"`

	ScoringTerms  []string `json:"scoringTerms"`
	ScoringSource string   `json:"scoringSource"` // "keywords" | "resume" | "none"
	// StaleKeywords is set when the keywords were written for a role that shares
	// no word with the roles about to be searched. The UI asks whether to keep or
	// reset them; the app never decides on its own (UX-014).
	StaleKeywords    bool     `json:"staleKeywords"`
	KeywordsForRoles []string `json:"keywordsForRoles,omitempty"`

	WorkMode string `json:"workMode"` // "remote" | "hybrid" | "onsite"
	// Locations are the places actually queried, in order, each with the modality
	// it is queried under. A remote-only entry here for an on-site search is the
	// UX-015 bug, made visible.
	Locations []searchPlanLocation `json:"locations"`

	Sources []string `json:"sources"`
	Summary string   `json:"summary"`
}

type searchPlanLocation struct {
	Location string `json:"location"`
	Remote   bool   `json:"remote"`
}

func buildSearchPlan(config appConfig) searchPlanResponse {
	eff := effectiveSearchConfig(config)

	plan := searchPlanResponse{
		// Required array fields must never inherit a nil slice. encoding/json
		// serializes nil as null, while the desktop contract (and normal array
		// operations such as join) requires an array even on first run.
		Roles:            append([]string{}, eff.Roles...),
		RolesSource:      eff.RolesSource,
		IgnoredRoles:     append([]string{}, eff.IgnoredRoles...),
		Levels:           append([]string{}, splitCSV(coalesce(config.Form.Levels, config.Form.Seniority))...),
		ExcludedLevels:   append([]string{}, splitCSV(config.Form.ExcludedLevels)...),
		ScoringTerms:     append([]string{}, eff.ScoringTerms...),
		ScoringSource:    eff.ScoringSource,
		StaleKeywords:    eff.StaleKeywords,
		KeywordsForRoles: append([]string{}, eff.KeywordsForRoles...),
		WorkMode:         eff.WorkMode,
		Locations:        make([]searchPlanLocation, 0),
		Sources:          make([]string, 0),
		Summary:          eff.summary(),
	}

	for _, pipeline := range modalityPipelines(config) {
		plan.Locations = append(plan.Locations, searchPlanLocation{
			Location: pipeline.location,
			Remote:   pipeline.remote,
		})
	}

	for toggle, name := range map[string]string{
		"useLinkedin":       "LinkedIn",
		"useIndeed":         "Indeed",
		"useGupy":           "Gupy",
		"useRemotive":       "Remotive",
		"useRemoteok":       "RemoteOK",
		"useJobicy":         "Jobicy",
		"useArbeitnow":      "Arbeitnow",
		"useWeworkremotely": "We Work Remotely",
	} {
		if config.Toggles[toggle] {
			plan.Sources = append(plan.Sources, name)
		}
	}
	sortStrings(plan.Sources)

	return plan
}

func (a *api) searchPlan(w http.ResponseWriter, _ *http.Request) {
	config, err := a.configStore.load()
	if err != nil {
		a.logger.Printf("search plan: load config: %v", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"message": "Não foi possível ler a configuração."})
		return
	}
	writeJSON(w, http.StatusOK, buildSearchPlan(config))
}

func sortStrings(in []string) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && strings.ToLower(in[j]) < strings.ToLower(in[j-1]); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}
