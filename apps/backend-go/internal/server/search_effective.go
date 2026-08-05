package server

import (
	"fmt"
	"strings"
)

// Nothing in this app knew what search it was actually running.
//
// The 2026-07-13 usability run made that concrete. A nurse configured
// "Registered Nurse", hybrid/on-site, Chicago. The search then queried all of
// the United States with LinkedIn's remote-only filter, returned seventeen jobs
// in Florida, Missouri and Texas, and scored every one of them offline — while
// the provider test said the AI key was fine. Three separate bugs, one cause:
// five components each had their own private answer to "what is this search
// looking for?", and none of them agreed.
//
//	parseSearchProfiles      profiles win; Form.Roles is dead code (UX-001)
//	searchRolesConfigured    REQUIRES Form.Roles — which is what kept the stale
//	                         DevOps value alive across a change of profession
//	titleRelevant            reads Form.Roles, so it prefiltered the nursing jobs
//	                         the *profile* had just fetched, and every one of them
//	                         silently fell through to the offline heuristic (UX-016)
//	candidateScoringTerms    prefers Form.Keywords, wired to neither, so AWS and
//	                         Terraform were still scoring a nurse (UX-014)
//	modalityPipelines        string-compared a work-mode token the UI emits and
//	                         Go has never understood (UX-015)
//
// effectiveSearchConfig resolves all of it once. Every consumer — the URL
// builder, the prefilter, the scorer, the log, and the plan the user sees before
// pressing Search — reads the same struct. If they disagree now, they disagree
// loudly, in one place, with a test on it.

// The canonical work modes. These three, and only these three, reach the
// pipeline. Everything else — a legacy persisted value, a UI token nobody
// mapped — is normalized into one of them at the door.
const (
	workModeRemote = "remote"
	workModeHybrid = "hybrid"
	workModeOnsite = "onsite"
)

// canonicalWorkMode maps every token the UI has emitted, and every value a
// config might have persisted, onto the three the pipeline understands.
//
// "hybrid_onsite" is the one that mattered: the <select> has always emitted it
// (SettingsView.tsx) and modalityPipelines has never recognised it. It matched
// neither "hybrid" nor "onsite" nor "remote", so both the remote and the local
// pass were skipped and the empty-pipelines fallback quietly reinstated a remote
// search — pinned to RemoteCountry, with f_WT=2. The user's Chicago never had a
// chance to reach the query.
func canonicalWorkMode(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "onsite", "on-site", "presencial", "on_site":
		return workModeOnsite
	case "hybrid", "hibrido", "híbrido", "hybrid_onsite", "hybrid-onsite", "hibrido_presencial":
		return workModeHybrid
	case "remote", "remoto", "":
		return workModeRemote
	default:
		// An unknown token must never silently become "remote" again. Anything
		// naming a place is on-site; anything else is remote, which is the
		// documented default.
		return workModeRemote
	}
}

// wantsRemotePass / wantsLocalPass are the only two questions the scraper asks
// of a work mode. Hybrid means both: a hybrid role is advertised as remote by
// some boards and as on-site by others.
func wantsRemotePass(mode string) bool {
	return mode == workModeRemote || mode == workModeHybrid
}

func wantsLocalPass(mode string) bool {
	return mode == workModeOnsite || mode == workModeHybrid
}

// effectiveSearch is what the search is actually going to do, as opposed to what
// any single config field claims. It is computed once and shown to the user
// before the search starts, because "I cannot tell which setting controls this"
// was itself one of the reported bugs.
type effectiveSearch struct {
	Profiles []searchProfile

	// Roles is the union of every profile's roles — the set the search really
	// looks for, and therefore the set the AI prefilter must test against.
	Roles []string
	// RolesSource is "profiles" when the advanced profiles supplied the roles and
	// the simple target-role field was ignored, else "role".
	RolesSource string
	// IgnoredRoles is the simple target role that the advanced profiles overrode.
	// Non-empty here is exactly the UX-001 condition, and the UI must say so.
	IgnoredRoles []string

	// ScoringTerms are the keywords a job is scored against, and ScoringSource
	// says where they came from ("keywords" | "resume" | "none").
	ScoringTerms  []string
	ScoringSource string
	// StaleKeywords is set when the manual keywords were last edited for a role
	// that shares nothing with the roles this search is about to run — a nurse
	// still carrying AWS, Terraform and Kubernetes from a backend profile. It is
	// never acted on automatically: a silent reset is as bad as a silent
	// inheritance. The UI asks.
	StaleKeywords bool
	// KeywordsForRoles is the role set the keywords were written for.
	KeywordsForRoles []string

	WorkMode string
	// RemoteLocation and OnsiteLocation are the two places a search can target.
	// Which of them is used follows from WorkMode, not from a separate toggle.
	RemoteLocation string
	OnsiteLocation string
}

// effectiveSearchConfig resolves the whole config into the one description of
// the search that every consumer must agree on.
func effectiveSearchConfig(config appConfig) effectiveSearch {
	profiles := parseSearchProfiles(config)

	eff := effectiveSearch{
		Profiles:       profiles,
		WorkMode:       canonicalWorkMode(config.Form.WorkMode),
		RemoteLocation: coalesce(strings.TrimSpace(config.Form.RemoteCountry), "Brazil"),
		OnsiteLocation: coalesce(strings.TrimSpace(config.Form.OnsiteLocation), strings.TrimSpace(config.Form.Location)),
	}

	seen := map[string]bool{}
	for _, p := range profiles {
		for _, role := range p.Roles {
			key := strings.ToLower(normalizeText(strings.TrimSpace(role)))
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			eff.Roles = append(eff.Roles, role)
		}
	}

	simple := splitCSV(coalesce(config.Form.Roles, config.Form.Role))
	eff.RolesSource = "role"
	if len(profiles) > 0 && profiles[0].explicit {
		eff.RolesSource = "profiles"
		// Only report the simple role as ignored when it is genuinely different
		// from what the profiles will search for. Repeating the same role back at
		// the user as a conflict is noise.
		for _, role := range simple {
			if !seen[strings.ToLower(normalizeText(strings.TrimSpace(role)))] {
				eff.IgnoredRoles = append(eff.IgnoredRoles, role)
			}
		}
	}

	eff.ScoringTerms = candidateScoringTerms(config)
	switch {
	case len(splitCSV(config.Form.Keywords)) > 0:
		eff.ScoringSource = "keywords"
	case len(eff.ScoringTerms) > 0:
		eff.ScoringSource = "resume"
	default:
		eff.ScoringSource = "none"
	}

	eff.KeywordsForRoles = splitCSV(config.Form.KeywordsForRoles)
	if eff.ScoringSource == "keywords" && len(eff.KeywordsForRoles) > 0 && len(eff.Roles) > 0 {
		eff.StaleKeywords = !roleSetsOverlap(eff.KeywordsForRoles, eff.Roles)
	}

	return eff
}

// roleSetsOverlap reports whether two role sets share any meaningful word. It is
// how a "material change of profession" is detected without the code ever
// knowing what a profession is: Backend Developer and Registered Nurse share no
// token, so the keywords written for one cannot be assumed to fit the other.
// Words shorter than four characters are ignored so that "of", "and", "sr" do
// not make every role look related to every other.
func roleSetsOverlap(a []string, b []string) bool {
	words := map[string]bool{}
	for _, role := range a {
		for _, word := range strings.Fields(strings.ToLower(normalizeText(role))) {
			if len(word) >= 4 {
				words[word] = true
			}
		}
	}
	for _, role := range b {
		for _, word := range strings.Fields(strings.ToLower(normalizeText(role))) {
			if len(word) >= 4 && words[word] {
				return true
			}
		}
	}
	return false
}

// targetLocation is the place this search actually queries, given its mode.
func (e effectiveSearch) targetLocation(remotePass bool) string {
	if remotePass {
		return e.RemoteLocation
	}
	return e.OnsiteLocation
}

// configured reports whether the search has anything to look for. It accepts a
// profiles-only config, which the old searchRolesConfigured did not — and that
// refusal is what forced the stale simple-Roles field to stay populated,
// which is what kept feeding the wrong roles to the AI prefilter.
func (e effectiveSearch) configured() bool {
	return len(e.Roles) > 0
}

// summary is the plain-language description of the effective search, shown to
// the user before the run and written to the log. "I could not tell which
// configuration controlled the search" was a reported bug in its own right.
func (e effectiveSearch) summary() string {
	var b strings.Builder
	fmt.Fprintf(&b, "cargos: %s", strings.Join(e.Roles, ", "))
	if e.RolesSource == "profiles" {
		b.WriteString(" (perfis avancados)")
	}
	if len(e.IgnoredRoles) > 0 {
		fmt.Fprintf(&b, " — cargo simples IGNORADO: %s", strings.Join(e.IgnoredRoles, ", "))
	}
	switch e.WorkMode {
	case workModeRemote:
		fmt.Fprintf(&b, " | modalidade: remoto em %s", e.RemoteLocation)
	case workModeOnsite:
		fmt.Fprintf(&b, " | modalidade: presencial em %s", e.OnsiteLocation)
	case workModeHybrid:
		fmt.Fprintf(&b, " | modalidade: hibrido (remoto em %s + presencial em %s)", e.RemoteLocation, e.OnsiteLocation)
	}
	terms := "(nenhuma)"
	if len(e.ScoringTerms) > 0 {
		terms = strings.Join(e.ScoringTerms, ", ")
	}
	fmt.Fprintf(&b, " | keywords (%s): %s", e.ScoringSource, terms)
	if e.StaleKeywords {
		fmt.Fprintf(&b, " | ATENCAO: keywords escritas para %s", strings.Join(e.KeywordsForRoles, ", "))
	}
	return b.String()
}
