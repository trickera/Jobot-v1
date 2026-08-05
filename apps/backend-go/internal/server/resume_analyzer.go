package server

import (
	"math"
	"net/http"
	"regexp"
	"strings"
	"unicode"
)

// resumeMetricPattern matches numeric/percentage/currency/scale signals used
// to detect bullets with quantifiable impact (Appendix F "impact"):
// percentages, currency (R$/$/€), explicit numbers and scale suffixes
// (k/mi/mil/million/bn/billion).
var resumeMetricPattern = regexp.MustCompile(`(?i)\d+([.,]\d+)?\s*(%|k|mi|mil|million|bn|billion)?|[$€]\s*\d|R\$\s*\d`)

// resumeImpactVerbs (EN+PT) are variation verbs Appendix F lists as impact
// signals when followed by a number (already covered by resumeMetricPattern
// whenever a digit is present, kept here for the "increased/reduced/..."
// naming fidelity to the spec).
var resumeImpactVerbs = []string{
	"increased", "reduced", "grew", "cut", "saved", "improved",
	"aumentou", "reduziu",
}

// resumeActionVerbs (EN+PT — Appendix G.4) are used to detect whether a
// bullet opens with a strong action verb (writing-rule #1 from the
// brainstorm §7.5, reused by the "generic_bullets" diagnosis issue and,
// later, the HR score's "skills"/"impact" components).
var resumeActionVerbs = map[string]bool{
	// EN
	"lead": true, "build": true, "design": true, "implement": true,
	"launch": true, "improve": true, "reduce": true, "increase": true,
	"manage": true, "deliver": true, "automate": true, "migrate": true,
	"optimize": true, "develop": true, "create": true, "drive": true,
	"achieve": true, "architect": true, "engineer": true, "deploy": true,
	"streamline": true, "spearhead": true, "coordinate": true, "mentor": true,
	"introduce": true, "cut": true,
	"led": true, "built": true, "designed": true, "implemented": true,
	"launched": true, "improved": true, "reduced": true, "increased": true,
	"managed": true, "delivered": true, "automated": true, "migrated": true,
	"optimized": true, "developed": true, "created": true, "drove": true,
	"achieved": true, "architected": true, "engineered": true, "deployed": true,
	"streamlined": true, "spearheaded": true, "coordinated": true, "mentored": true,
	// PT
	"liderar": true, "lidera": true, "criar": true, "cria": true,
	"desenvolver": true, "desenvolve": true, "implementar": true, "implementa": true,
	"lancar": true, "lanca": true, "melhorar": true, "melhora": true,
	"reduzir": true, "reduz": true, "aumentar": true, "aumenta": true,
	"gerir": true, "gere": true, "entregar": true, "entrega": true,
	"automatizar": true, "automatiza": true, "migrar": true, "migra": true,
	"otimizar": true, "otimiza": true, "arquitetar": true, "arquiteta": true,
	"coordenar": true, "coordena": true, "projetar": true, "projeta": true,
	"liderou": true, "criou": true, "desenvolveu": true, "implementou": true,
	"lancou": true, "melhorou": true, "reduziu": true, "aumentou": true,
	"geriu": true, "entregou": true, "automatizou": true, "migrou": true,
	"otimizou": true, "arquitetou": true, "coordenou": true, "projetou": true,
}

var irregularActionVerbs = map[string]string{
	"led": "lead", "built": "build", "drove": "drive", "cut": "cut",
}

func isResumeActionVerb(word string) bool {
	word = strings.Trim(strings.TrimSpace(normalizeText(word)), ".,;:!?()[]{}\"'")
	if resumeActionVerbs[word] {
		return true
	}
	if base := irregularActionVerbs[word]; resumeActionVerbs[base] {
		return true
	}

	var candidates []string
	switch {
	case strings.HasSuffix(word, "ied") && len(word) > 3:
		candidates = append(candidates, strings.TrimSuffix(word, "ied")+"y")
	case strings.HasSuffix(word, "ing") && len(word) > 4:
		root := strings.TrimSuffix(word, "ing")
		candidates = append(candidates, root, root+"e", trimDoubledFinalRune(root))
	case strings.HasSuffix(word, "ed") && len(word) > 3:
		root := strings.TrimSuffix(word, "ed")
		candidates = append(candidates, root, root+"e", trimDoubledFinalRune(root))
	case strings.HasSuffix(word, "es") && len(word) > 3:
		candidates = append(candidates, strings.TrimSuffix(word, "es"), strings.TrimSuffix(word, "s"))
	case strings.HasSuffix(word, "s") && len(word) > 2:
		candidates = append(candidates, strings.TrimSuffix(word, "s"))
	}
	for _, candidate := range candidates {
		if resumeActionVerbs[candidate] {
			return true
		}
	}
	return false
}

func trimDoubledFinalRune(value string) string {
	runes := []rune(value)
	if len(runes) >= 2 && runes[len(runes)-1] == runes[len(runes)-2] {
		return string(runes[:len(runes)-1])
	}
	return value
}

func bulletHasMetric(bullet string) bool {
	if resumeMetricPattern.MatchString(bullet) {
		return true
	}
	normalized := normalizeText(bullet)
	hasDigit := strings.ContainsAny(bullet, "0123456789")
	if !hasDigit {
		return false
	}
	for _, verb := range resumeImpactVerbs {
		if containsTerm(normalized, verb) {
			return true
		}
	}
	return false
}

func bulletStartsWithActionVerb(bullet string) bool {
	normalized := strings.TrimLeftFunc(normalizeText(bullet), func(r rune) bool {
		return unicode.IsSpace(r) || unicode.IsPunct(r) || unicode.IsSymbol(r)
	})
	fields := strings.Fields(normalized)
	if len(fields) == 0 {
		return false
	}
	return isResumeActionVerb(fields[0])
}

// resumeBullets collects every bullet the resume has (experience + project
// entries) — the population used by the impact/generic-bullets checks.
func resumeBullets(r CanonicalResume) []string {
	var bullets []string
	for _, exp := range r.Experience {
		bullets = append(bullets, exp.Bullets...)
	}
	for _, proj := range r.Projects {
		bullets = append(bullets, proj.Bullets...)
	}
	return bullets
}

// bulletImpactFraction returns the share of bullets that carry a
// quantifiable impact signal (Appendix F "impact"/G.1 "Impact").
func bulletImpactFraction(r CanonicalResume) (fraction float64, total int) {
	bullets := resumeBullets(r)
	total = len(bullets)
	if total == 0 {
		return 0, 0
	}
	matched := 0
	for _, b := range bullets {
		if bulletHasMetric(b) {
			matched++
		}
	}
	return float64(matched) / float64(total), total
}

// impactSubScore turns the bullet-impact fraction into the 0-100 score
// shared by the diagnosis "Impact" sub-score (G.1) and the HR score's
// "impact" component (Appendix F) — they are defined as the same formula.
func impactSubScore(r CanonicalResume) int {
	fraction, total := bulletImpactFraction(r)
	if total == 0 {
		return 0
	}
	return boundedScore(int(math.Round(math.Min(100, fraction/0.4*100))))
}

// genericBulletsDetected flags a resume where most bullets do not open with
// a strong action verb (G.2 "generic_bullets").
func genericBulletsDetected(r CanonicalResume) bool {
	bullets := resumeBullets(r)
	if len(bullets) == 0 {
		return false
	}
	weak := 0
	for _, b := range bullets {
		if !bulletStartsWithActionVerb(b) {
			weak++
		}
	}
	return float64(weak)/float64(len(bullets)) >= 0.5
}

// resumeSearchableText concatenates the resume's free-text content
// (summary + skills + experience/education/project text) into a single
// string for keyword extraction and, later, ATS phrase/keyword matching
// (Appendix F): "texto concatenado do currículo (summary + bullets + skills)".
func resumeSearchableText(r CanonicalResume) string {
	var parts []string
	parts = append(parts, r.Summary)
	parts = append(parts, r.Basics.Headline)
	parts = append(parts, r.Target.JobTitle, r.Target.Category, r.Target.Seniority)
	parts = append(parts, r.Skills.Hard...)
	parts = append(parts, r.Skills.Soft...)
	parts = append(parts, r.Skills.Tools...)
	for _, exp := range r.Experience {
		parts = append(parts, exp.Company, exp.Role)
		parts = append(parts, exp.Bullets...)
	}
	for _, edu := range r.Education {
		parts = append(parts, edu.Institution, edu.Degree, edu.Area)
	}
	for _, proj := range r.Projects {
		parts = append(parts, proj.Name, proj.Description)
		parts = append(parts, proj.Bullets...)
	}
	for _, license := range r.Licenses {
		parts = append(parts, license.Name, license.Issuer, license.Jurisdiction, license.Number, license.Expires)
	}
	for _, cert := range r.Certifications {
		parts = append(parts, cert.Name, cert.Issuer)
	}
	for _, lang := range r.Languages {
		parts = append(parts, lang.Language, lang.Fluency)
	}
	return strings.Join(parts, " ")
}

// hasMultiColumnRisk detects raw-text signals of a multi-column/table
// layout, the leading cause of ATS parsing failures (G.2 "multi_column_risk").
func hasMultiColumnRisk(rawText string) bool {
	if strings.TrimSpace(rawText) == "" {
		return false
	}
	if strings.Contains(rawText, "\t\t") {
		return true
	}
	if strings.Count(rawText, " | ") > 3 {
		return true
	}
	return false
}

// nonASCIIRatio is the fraction of runes outside ASCII, used to flag
// icon-heavy resumes (G.1 "Readability").
func nonASCIIRatio(text string) float64 {
	total := 0
	nonASCII := 0
	for _, r := range text {
		total++
		if r > unicode.MaxASCII {
			nonASCII++
		}
	}
	if total == 0 {
		return 0
	}
	return float64(nonASCII) / float64(total)
}

// diagnoseResume is the offline (no AI key required) ATS diagnostic:
// four 0-100 sub-scores plus a list of actionable, stable-coded issues
// (Appendix G). It is deterministic — same input always yields the same
// output.
func diagnoseResume(r CanonicalResume, rawText string) (atsScores, []atsIssue) {
	var issues []atsIssue

	readability := 100
	if hasMultiColumnRisk(rawText) {
		readability -= 20
		issues = append(issues, atsIssue{Code: "multi_column_risk", Severity: "high", Message: "Multi-column/table layout may break ATS parsing."})
	}
	if strings.TrimSpace(rawText) != "" && nonASCIIRatio(rawText) > 0.05 {
		readability -= 20
	}
	hasParsableDate := false
	for _, exp := range r.Experience {
		if _, ok := parseResumeDate(exp.Start); ok {
			hasParsableDate = true
			break
		}
	}
	if len(r.Experience) > 0 && !hasParsableDate {
		readability -= 15
		issues = append(issues, atsIssue{Code: "no_dates", Severity: "medium", Message: "Experience dates are missing or unreadable."})
	}
	hasContact := strings.TrimSpace(r.Basics.Email) != "" || strings.TrimSpace(r.Basics.Phone) != ""
	if !hasContact {
		readability -= 10
		issues = append(issues, atsIssue{Code: "no_contact", Severity: "medium", Message: "Missing email or phone in the header."})
	}
	readability = boundedScore(readability)

	content := 100
	if strings.TrimSpace(r.Summary) == "" {
		content -= 20
		issues = append(issues, atsIssue{Code: "no_summary", Severity: "medium", Message: "Resume has no summary/objective."})
	}
	if len(r.Experience) == 0 {
		content -= 20
		issues = append(issues, atsIssue{Code: "no_experience", Severity: "high", Message: "Resume has no experience section."})
	}
	if len(r.Education) == 0 {
		content -= 20
		issues = append(issues, atsIssue{Code: "no_education", Severity: "medium", Message: "Resume has no education section."})
	}
	if len(r.Skills.Hard)+len(r.Skills.Soft)+len(r.Skills.Tools) < 3 {
		content -= 20
		issues = append(issues, atsIssue{Code: "few_skills", Severity: "medium", Message: "Resume lists fewer than three skills."})
	}
	content = boundedScore(content)

	fraction, totalBullets := bulletImpactFraction(r)
	impact := impactSubScore(r)
	if totalBullets > 0 && fraction < 0.4 {
		issues = append(issues, atsIssue{Code: "no_metrics", Severity: "high", Message: "Few bullets with impact metrics."})
	}

	if genericBulletsDetected(r) {
		issues = append(issues, atsIssue{Code: "generic_bullets", Severity: "medium", Message: "Generic bullets; start with action verbs."})
	}

	text := rawText
	if strings.TrimSpace(text) == "" {
		text = resumeSearchableText(r)
	}
	detectedKeywords := extractResumeKeywords(text)
	if len(r.Skills.Hard) == 0 && len(detectedKeywords) > 0 {
		issues = append(issues, atsIssue{Code: "skills_unorganized", Severity: "low", Message: "Skills are not well organized."})
	}
	keywords := boundedScore(int(math.Round(math.Min(100, float64(len(detectedKeywords))/10*100))))

	return atsScores{
		Readability: readability,
		Content:     content,
		Impact:      impact,
		Keywords:    keywords,
		// Same condition the no_metrics warning uses: with no bullets there is
		// nothing to judge, and saying so beats reporting the zero we default to.
		ImpactMeasured: totalBullets > 0,
	}, issues
}

func (a *api) resumeDiagnose(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload diagnoseRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}

	canonical := normalizeCanonical(payload.Canonical)
	heuristic := false
	if isEmptyCanonical(canonical) && strings.TrimSpace(payload.RawText) != "" {
		// BUG-04: no AI parse has happened (or none was provided) - build a
		// best-effort structure directly from the raw text so the offline
		// diagnostic still has real section signal (dates/contact/summary/
		// experience/education/skills) instead of scoring an all-empty
		// resume. This route never required an AI key to begin with; the
		// gap was that the frontend only ever called it after the AI-gated
		// parse step succeeded.
		canonical = buildHeuristicCanonical(payload.RawText)
		heuristic = true
	}
	scores, issues := diagnoseResume(canonical, payload.RawText)
	if issues == nil {
		issues = []atsIssue{}
	}

	if strings.TrimSpace(payload.DocumentID) != "" {
		if err := a.configStore.saveResumeAnalysis(resumeAnalysis{
			SubjectKind: "document",
			SubjectID:   payload.DocumentID,
			Scores:      scores,
			Issues:      issues,
		}); err != nil {
			a.logger.Printf("save resume analysis: %v", err)
		}
	}

	writeJSON(w, http.StatusOK, diagnoseResponse{Scores: scores, Issues: issues, Canonical: canonical, Heuristic: heuristic})
}
