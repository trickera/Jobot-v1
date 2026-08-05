package server

import (
	"regexp"
	"sort"
	"strings"
)

type seniorityRule struct {
	canonical string
	terms     []string
}

// seniorityRules is ordered longest-first so "tech lead" wins over "lead".
var seniorityRules = []seniorityRule{
	{canonical: "lead", terms: []string{"tech lead", "team lead", "líder", "lider", "lead"}},
	{canonical: "principal", terms: []string{"principal"}},
	{canonical: "staff", terms: []string{"staff"}},
	{canonical: "manager", terms: []string{"manager", "gerente", "coordenador", "coordenadora"}},
	{canonical: "especialista", terms: []string{"especialista", "specialist"}},
	{canonical: "senior", terms: []string{"sênior", "senior", "ssr", "sr"}},
	{canonical: "pleno", terms: []string{"mid-level", "mid level", "midlevel", "pleno", "plena"}},
	{canonical: "junior", terms: []string{"entry level", "entry-level", "júnior", "junior", "jr"}},
	{canonical: "intern", terms: []string{"estagiario", "estagiária", "estagio", "estágio", "intern", "trainee"}},
}

func allowedSeniorityLevels(config appConfig) map[string]bool {
	allowed := map[string]bool{}
	for _, raw := range splitCSV(coalesce(config.Form.Levels, config.Form.Seniority)) {
		for _, canonical := range canonicalSeniorityTerms(raw) {
			allowed[canonical] = true
		}
	}
	return allowed
}

func canonicalSeniorityTerms(raw string) []string {
	raw = normalizeText(strings.TrimSpace(raw))
	if raw == "" {
		return nil
	}
	for _, rule := range seniorityRules {
		for _, term := range rule.terms {
			if raw == normalizeText(term) {
				return []string{rule.canonical}
			}
		}
	}
	switch {
	case strings.Contains(raw, "jun"):
		return []string{"junior"}
	case strings.Contains(raw, "plen"):
		return []string{"pleno"}
	case raw == "sr" || strings.Contains(raw, "sen"):
		return []string{"senior"}
	case strings.Contains(raw, "espec"):
		return []string{"especialista"}
	case strings.Contains(raw, "lead") || strings.Contains(raw, "lider"):
		return []string{"lead"}
	case strings.Contains(raw, "staff"):
		return []string{"staff"}
	case strings.Contains(raw, "principal"):
		return []string{"principal"}
	case strings.Contains(raw, "ger") || strings.Contains(raw, "coord") || strings.Contains(raw, "manager"):
		return []string{"manager"}
	case strings.Contains(raw, "estag") || strings.Contains(raw, "intern") || strings.Contains(raw, "trainee"):
		return []string{"intern"}
	default:
		return []string{raw}
	}
}

var principalICTitle = regexp.MustCompile(`(?i)(principal\s+(engineer|engenheiro|developer|desenvolvedor|software|architect|arquiteto|consultant|consultor|scientist|cientista|designer|analyst|analista))|((staff|senior|sr\.?)\s+principal\b)|(principal\s+(ic|staff|engineer))`)

func detectSeniorityLevels(text string) []string {
	text = normalizeText(text)
	if strings.TrimSpace(text) == "" {
		return nil
	}
	found := map[string]bool{}
	for _, rule := range seniorityRules {
		for _, term := range rule.terms {
			if containsSeniorityTerm(text, term) {
				found[rule.canonical] = true
				break
			}
		}
	}
	if len(found) == 0 {
		return nil
	}
	out := make([]string, 0, len(found))
	for level := range found {
		out = append(out, level)
	}
	sort.Strings(out)
	return out
}

func containsSeniorityTerm(normalizedHaystack, term string) bool {
	term = normalizeText(strings.TrimSpace(term))
	if term == "principal" {
		return principalLevelSignal(normalizedHaystack)
	}
	return containsTerm(normalizedHaystack, term)
}

func containsExcludedSeniorityTerm(normalizedHaystack, term string) bool {
	term = normalizeText(strings.TrimSpace(term))
	switch term {
	case "principal":
		return principalLevelSignal(normalizedHaystack)
	default:
		return containsTerm(normalizedHaystack, term)
	}
}

func principalLevelSignal(text string) bool {
	text = normalizeText(text)
	if principalEngineeringTitle(text) {
		return true
	}
	if hasPrincipalBoilerplate(text) {
		return false
	}
	return containsTerm(text, "principal")
}

func principalEngineeringTitle(text string) bool {
	return principalICTitle.FindStringIndex(text) != nil
}

func hasPrincipalBoilerplate(text string) bool {
	phrases := []string{
		"principais atividades", "atividades principais", "principais responsabilidades",
		"responsabilidades principais", "principalmente", "unidade principal",
		"atividade principal", "funcao principal", "objetivo principal",
		"missao principal", "atribuicoes principais", "cargo principal",
	}
	text = normalizeText(text)
	for _, phrase := range phrases {
		if strings.Contains(text, phrase) {
			return true
		}
	}
	return false
}
