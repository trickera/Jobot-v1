package server

import (
	"strconv"
	"strings"
)

// searchProfile is one independent role+seniority package. The user can define
// several (one per line) so each role group carries its own seniority window,
// e.g. "DevOps + Junior/Pleno" while "Infra + Pleno/Senior". Nothing is
// hard-coded: the user writes whatever roles and levels fit their need.
type searchProfile struct {
	Name           string
	Roles          []string
	Levels         []string
	ExcludedLevels []string
	MaxYears       int
	explicit       bool
}

// parseSearchProfiles reads the multi-line searchProfiles field. Each line:
//
//	roles | allowed levels | excluded levels | max years
//
// Only the roles column is mandatory; missing columns fall back to the global
// Levels / ExcludedLevels / MaxYears. When searchProfiles is empty, a single
// global profile is returned so legacy single-field setups keep working.
func parseSearchProfiles(config appConfig) []searchProfile {
	raw := strings.TrimSpace(config.Form.SearchProfiles)
	globalLevels := coalesce(config.Form.Levels, config.Form.Seniority)
	globalExcluded := config.Form.ExcludedLevels
	globalMaxYears := config.Form.MaxYears

	var profiles []searchProfile
	if raw != "" {
		for _, line := range strings.Split(raw, "\n") {
			line = strings.TrimSpace(strings.TrimRight(line, "\r"))
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.Split(line, "|")
			roles := splitCSV(profileColumn(parts, 0))
			if len(roles) == 0 {
				continue
			}
			levels := splitCSV(profileColumn(parts, 1))
			if len(levels) == 0 {
				levels = splitCSV(globalLevels)
			}
			excluded := splitCSV(profileColumn(parts, 2))
			if len(excluded) == 0 {
				excluded = splitCSV(globalExcluded)
			}
			maxYears := globalMaxYears
			if y := strings.TrimSpace(profileColumn(parts, 3)); y != "" {
				if parsed, err := strconv.Atoi(y); err == nil && parsed > 0 {
					maxYears = parsed
				}
			}
			profiles = append(profiles, searchProfile{
				Name:           roles[0],
				Roles:          roles,
				Levels:         levels,
				ExcludedLevels: excluded,
				MaxYears:       maxYears,
				explicit:       true,
			})
		}
	}

	if len(profiles) > 0 {
		return profiles
	}

	roles := splitCSV(coalesce(config.Form.Roles, config.Form.Role))
	name := "Global"
	if len(roles) > 0 {
		name = roles[0]
	}
	return []searchProfile{{
		Name:           name,
		Roles:          roles,
		Levels:         splitCSV(globalLevels),
		ExcludedLevels: splitCSV(globalExcluded),
		MaxYears:       globalMaxYears,
	}}
}

func profileColumn(parts []string, index int) string {
	if index < len(parts) {
		return parts[index]
	}
	return ""
}

// queryGroups splits the profile roles into boolean OR groups of at most
// maxPerGroup so a long list does not truncate the Indeed URL nor over-narrow
// LinkedIn. Levels are intentionally NOT part of the query: we search broad by
// role and enforce seniority after fetching, which greatly improves recall.
func (p searchProfile) queryGroups(maxPerGroup int) []string {
	if len(p.Roles) == 0 {
		return nil
	}
	if maxPerGroup < 1 {
		maxPerGroup = 5
	}
	var groups []string
	for i := 0; i < len(p.Roles); i += maxPerGroup {
		end := i + maxPerGroup
		if end > len(p.Roles) {
			end = len(p.Roles)
		}
		groups = append(groups, booleanGroup(p.Roles[i:end]))
	}
	return groups
}

// gupyQuery joins roles with spaces because Gupy has no boolean search.
func (p searchProfile) gupyQuery() string {
	return strings.Join(p.Roles, " ")
}

// seniorityRuleSet is the resolved seniority window used to accept/reject a job.
type seniorityRuleSet struct {
	allowed  map[string]bool
	excluded []string
	maxYears int
}

func (p searchProfile) seniorityRules() seniorityRuleSet {
	allowed := map[string]bool{}
	for _, raw := range p.Levels {
		for _, canonical := range canonicalSeniorityTerms(raw) {
			allowed[canonical] = true
		}
	}
	return seniorityRuleSet{
		allowed:  allowed,
		excluded: p.ExcludedLevels,
		maxYears: p.MaxYears,
	}
}

func globalSeniorityRuleSet(config appConfig) seniorityRuleSet {
	return seniorityRuleSet{
		allowed:  allowedSeniorityLevels(config),
		excluded: splitCSV(config.Form.ExcludedLevels),
		maxYears: config.Form.MaxYears,
	}
}

// outsideAllowedReason returns a non-empty reason when text carries a seniority
// signal that is not in the allowed set. Empty allowed set means "accept all".
func (r seniorityRuleSet) outsideAllowedReason(text string, roleLevels map[string]bool) string {
	if len(r.allowed) == 0 {
		return ""
	}
	for _, level := range detectSeniorityLevels(text) {
		if !r.allowed[level] && !roleLevels[level] {
			return "senioridade fora do alvo: " + level
		}
	}
	return ""
}
