package server

import (
	"math"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// weighted rounds a 0-100 component score down to its already-weighted
// point contribution (Appendix F: "score final é a soma ponderada").
func weighted(component, weightPercent int) int {
	return int(math.Round(float64(component) * float64(weightPercent) / 100))
}

func sumBreakdown(b scoreBreakdown) int {
	total := 0
	for _, v := range b {
		total += v
	}
	return total
}

// requirementFillerWords are the words a job posting wraps a requirement in
// ("5+ years of strong hands-on experience with Figma") that carry no keyword
// of their own. Scoring them as if they were skills is what made a
// natural-language requirement unmatchable.
var requirementFillerWords = map[string]bool{
	"year": true, "years": true, "ano": true, "anos": true,
	"experience": true, "experiencia": true, "background": true,
	"strong": true, "solid": true, "proven": true, "deep": true, "hands": true,
	"knowledge": true, "understanding": true, "ability": true, "skills": true,
	"plus": true, "nice": true, "must": true, "have": true, "having": true,
	"working": true, "work": true, "using": true, "use": true, "including": true,
	"such": true, "etc": true, "solida": true, "forte": true, "conhecimento": true,
}

// requirementTokens reduces a requirement to the tokens that actually carry
// meaning: stopwords, filler and bare numbers dropped.
func requirementTokens(term string) []string {
	tokens := make([]string, 0, 4)
	for _, token := range significantTerms(term) {
		if requirementFillerWords[token] || isAllDigits(token) {
			continue
		}
		tokens = append(tokens, token)
	}
	return tokens
}

func isAllDigits(token string) bool {
	for _, r := range token {
		if r < '0' || r > '9' {
			return false
		}
	}
	return len(token) > 0
}

// containsRequirementToken matches a requirement token against the resume,
// tolerating the inflections a real ATS keyword scanner also folds together
// ("design" in "designer", "research" in "researchers"). Only tokens long
// enough to be a stem get the suffix tolerance, so short tokens keep the
// strict word-boundary match.
func containsRequirementToken(normalizedResumeText, token string) bool {
	if containsTerm(normalizedResumeText, token) {
		return true
	}
	if len(token) < 5 {
		return false
	}
	re := regexp.MustCompile(`(^|[^a-z0-9])` + regexp.QuoteMeta(token) + `[a-z]{0,4}([^a-z0-9]|$)`)
	return re.FindStringIndex(normalizedResumeText) != nil
}

// requirementCoverage is how much of one requirement the resume actually
// covers, in [0,1]. A verbatim phrase (or an atomic keyword like "CI/CD")
// scores 1; a natural-language requirement scores the fraction of its
// meaningful tokens the resume can show. Partial credit is the point: the old
// all-or-nothing phrase match scored an aligned resume at zero because "5+
// years product design" never appears verbatim in anyone's resume.
func requirementCoverage(normalizedResumeText, term string) float64 {
	if containsTerm(normalizedResumeText, term) {
		return 1
	}
	tokens := requirementTokens(term)
	if len(tokens) == 0 {
		return 0
	}
	matched := 0
	for _, token := range tokens {
		if containsRequirementToken(normalizedResumeText, token) {
			matched++
		}
	}
	return float64(matched) / float64(len(tokens))
}

// phraseMatchComponent is shared by the ATS "phrase" and "keyword"
// components: both are "how much of each term the resume's own text covers",
// just fed different term lists (hardRequirements vs atsKeywords).
func phraseMatchComponent(normalizedResumeText string, terms []string) int {
	if len(terms) == 0 {
		return 100
	}
	var covered float64
	for _, term := range terms {
		covered += requirementCoverage(normalizedResumeText, term)
	}
	return int(math.Round(covered / float64(len(terms)) * 100))
}

// titleMatchComponent checks whether the job title (or a significant word
// of it) appears in the resume's header/summary.
func titleMatchComponent(r CanonicalResume, req jobRequirements) int {
	target := strings.TrimSpace(req.JobTitle)
	if target == "" {
		return 100
	}
	haystack := normalizeText(r.Basics.Headline + " " + r.Target.JobTitle + " " + r.Summary)
	if containsTerm(haystack, target) {
		return 100
	}
	for _, word := range strings.Fields(target) {
		if len(word) < 4 {
			continue
		}
		if containsTerm(haystack, word) {
			return 100
		}
	}
	return 0
}

// skillsInContextComponent is the fraction of hard skills that are also
// demonstrated somewhere in the experience/project bullets.
func skillsInContextComponent(r CanonicalResume) int {
	if len(r.Skills.Hard) == 0 {
		return 100
	}
	bulletsText := normalizeText(strings.Join(resumeBullets(r), " "))
	matched := 0
	for _, skill := range r.Skills.Hard {
		if containsTerm(bulletsText, skill) {
			matched++
		}
	}
	return int(math.Round(float64(matched) / float64(len(r.Skills.Hard)) * 100))
}

// recencyComponent rewards requirement/keyword matches that show up in the
// most recent role — Appendix F "skill recency".
func recencyComponent(r CanonicalResume, req jobRequirements) int {
	mostRecent := mostRecentExperience(r)
	if mostRecent == nil {
		return 0
	}
	text := normalizeText(mostRecent.Role + " " + strings.Join(mostRecent.Bullets, " "))
	for _, hardReq := range req.HardRequirements {
		if containsTerm(text, hardReq) {
			return 100
		}
	}
	for _, keyword := range req.ATSKeywords {
		if containsTerm(text, keyword) {
			return 60
		}
	}
	return 30
}

// atsScore implements the Appendix F ATS rubric: phrase 25% / keyword 25% /
// title 15% / structure 15% / skills-in-context 10% / recency 10%.
func atsScore(r CanonicalResume, req jobRequirements) (int, scoreBreakdown) {
	return atsScoreWithRawText(r, req, "")
}

func atsScoreWithRawText(r CanonicalResume, req jobRequirements, rawText string) (int, scoreBreakdown) {
	text := normalizeText(resumeSearchableText(r))
	structureScores, _ := diagnoseResume(r, rawText)

	breakdown := scoreBreakdown{
		"phrase":        weighted(phraseMatchComponent(text, req.HardRequirements), 25),
		"keyword":       weighted(phraseMatchComponent(text, req.ATSKeywords), 25),
		"title":         weighted(titleMatchComponent(r, req), 15),
		"structure":     weighted(structureScores.Readability, 15),
		"skillsContext": weighted(skillsInContextComponent(r), 10),
		"recency":       weighted(recencyComponent(r, req), 10),
	}
	return boundedScore(sumBreakdown(breakdown)), breakdown
}

// experienceRecencyKey returns a comparable time for ordering experience
// entries most-recent-first: "present" sorts as now; otherwise the end
// date, falling back to the start date, when parseable.
func experienceRecencyKey(e ResumeExperience) (time.Time, bool) {
	if strings.EqualFold(strings.TrimSpace(e.End), "present") {
		return time.Now(), true
	}
	if t, ok := parseResumeDate(e.End); ok {
		return t, true
	}
	if t, ok := parseResumeDate(e.Start); ok {
		return t, true
	}
	return time.Time{}, false
}

// experienceRecencyOrder returns a copy of the experience list ordered
// most-recent-first. Entries with parseable dates are ordered by date;
// entries without parseable dates keep their original relative order
// (index 0 = most recent), matching resume-writing convention.
func experienceRecencyOrder(r CanonicalResume) []ResumeExperience {
	ordered := make([]ResumeExperience, len(r.Experience))
	copy(ordered, r.Experience)
	sort.SliceStable(ordered, func(i, j int) bool {
		ki, oki := experienceRecencyKey(ordered[i])
		kj, okj := experienceRecencyKey(ordered[j])
		if oki && okj {
			return ki.After(kj)
		}
		if oki != okj {
			return oki
		}
		return false
	})
	return ordered
}

func mostRecentExperience(r CanonicalResume) *ResumeExperience {
	ordered := experienceRecencyOrder(r)
	if len(ordered) == 0 {
		return nil
	}
	return &ordered[0]
}

// totalExperienceYears sums the durations of every experience entry with a
// parseable start date; "present" counts as now. Entries with an
// unparseable/inconsistent range are skipped rather than guessed.
func totalExperienceYears(r CanonicalResume) float64 {
	var total float64
	for _, e := range r.Experience {
		start, ok := parseResumeDate(e.Start)
		if !ok {
			continue
		}
		end := time.Now()
		if !strings.EqualFold(strings.TrimSpace(e.End), "present") {
			t, ok := parseResumeDate(e.End)
			if !ok {
				continue
			}
			end = t
		}
		if end.Before(start) {
			continue
		}
		total += end.Sub(start).Hours() / (24 * 365.25)
	}
	return total
}

// requiredYearsPattern looks for an explicit years-of-experience requirement
// (e.g. "3+ years", "2 a 4 anos") in the job requirements' own text fields.
var requiredYearsPattern = regexp.MustCompile(`(?i)(\d+)\s*\+?\s*(years|year|anos|ano)\b`)

// extractRequiredYears scans jobRequirements' text fields for an explicit
// years requirement. optimizeRequest/scoreReq only carry the already-
// extracted jobRequirements (not the raw job description), so detection
// runs over hardRequirements/niceToHave/atsKeywords/jobTitle/category —
// good enough when the job analyzer captured "3+ years" as one of those
// items, degrading gracefully to the seniority-band signal otherwise.
func extractRequiredYears(req jobRequirements) (float64, bool) {
	haystacks := make([]string, 0, 2+len(req.HardRequirements)+len(req.NiceToHave)+len(req.ATSKeywords))
	haystacks = append(haystacks, req.JobTitle, req.Category)
	haystacks = append(haystacks, req.HardRequirements...)
	haystacks = append(haystacks, req.NiceToHave...)
	haystacks = append(haystacks, req.ATSKeywords...)
	for _, h := range haystacks {
		if m := requiredYearsPattern.FindStringSubmatch(h); m != nil {
			if n, err := strconv.Atoi(m[1]); err == nil {
				return float64(n), true
			}
		}
	}
	return 0, false
}

// senioritySignalRange is a weak, wide band per seniority label — NOT a
// hard rule (Global Constraint: seniority does not use a fixed year range).
func senioritySignalRange(seniority string) (lo, hi float64, ok bool) {
	switch strings.ToLower(strings.TrimSpace(seniority)) {
	case "junior", "júnior", "jr", "estagio", "estágio", "estagiário", "estagiaria":
		return 0, 2, true
	case "pleno", "mid", "mid-level":
		return 1, 5, true
	case "senior", "sênior", "sr":
		return 4, 10, true
	case "especialista", "specialist", "lideranca", "liderança", "leadership", "lead":
		return 7, 99, true
	default:
		return 0, 0, false
	}
}

// experienceFitComponent implements the Appendix F "Goldilocks" experience
// fit: prefer an explicit years requirement from the job text; otherwise
// fall back to a low-penalty seniority-band signal (never below 75 via
// that path, since years alone don't determine seniority).
func experienceFitComponent(r CanonicalResume, req jobRequirements) int {
	totalYears := totalExperienceYears(r)
	if requiredYears, ok := extractRequiredYears(req); ok {
		switch {
		case totalYears >= requiredYears:
			return 100
		case totalYears >= requiredYears-1:
			return 75
		default:
			return 50
		}
	}
	if len(r.Experience) == 0 {
		return 40
	}
	lo, hi, ok := senioritySignalRange(req.Seniority)
	if !ok {
		return 100
	}
	if totalYears >= lo-1 && totalYears <= hi+2 {
		return 100
	}
	return 75
}

// bulletContainsActionVerb checks every word (not just the first) so it can
// be used to gate whether a skill mention counts as "demonstrated".
func bulletContainsActionVerb(bullet string) bool {
	for _, word := range strings.Fields(normalizeText(bullet)) {
		if isResumeActionVerb(word) {
			return true
		}
	}
	return false
}

func skillDemonstratedInActionBullet(r CanonicalResume, skill string) bool {
	for _, bullet := range resumeBullets(r) {
		if bulletContainsActionVerb(bullet) && containsTerm(normalizeText(bullet), skill) {
			return true
		}
	}
	return false
}

// skillsDemonstratedComponent is the fraction of skills (any category) that
// appear in a bullet alongside an action verb, per Appendix F "skills".
func skillsDemonstratedComponent(r CanonicalResume) int {
	all := make([]string, 0, len(r.Skills.Hard)+len(r.Skills.Soft)+len(r.Skills.Tools))
	all = append(all, r.Skills.Hard...)
	all = append(all, r.Skills.Soft...)
	all = append(all, r.Skills.Tools...)
	if len(all) == 0 {
		return 100
	}
	demonstrated := 0
	for _, skill := range all {
		if skillDemonstratedInActionBullet(r, skill) {
			demonstrated++
		}
	}
	return int(math.Round(float64(demonstrated) / float64(len(all)) * 100))
}

var seniorTierWords = []string{
	"senior", "sênior", "lead", "principal", "staff", "head", "manager", "director",
	"coordenador", "gerente", "especialista",
}
var juniorTierWords = []string{
	"junior", "júnior", "jr", "intern", "trainee", "estagio", "estágio", "estagiario", "estagiário",
}

func seniorityWordRank(role string) int {
	normalized := normalizeText(role)
	for _, w := range seniorTierWords {
		if containsTerm(normalized, w) {
			return 2
		}
	}
	for _, w := range juniorTierWords {
		if containsTerm(normalized, w) {
			return 0
		}
	}
	return 1
}

// trajectoryComponent compares the most recent role's seniority word tier
// against the previous role's, per Appendix F "career trajectory".
func trajectoryComponent(r CanonicalResume) int {
	ordered := experienceRecencyOrder(r)
	if len(ordered) < 2 {
		return 0
	}
	current := seniorityWordRank(ordered[0].Role)
	previous := seniorityWordRank(ordered[1].Role)
	switch {
	case current > previous:
		return 100
	case current == previous:
		return 60
	default:
		return 30
	}
}

// clarityComponent scores 100 when the average bullet length sits in
// [8,30] words, penalizing linearly outside that range down to a floor of
// 40 (Appendix F "clareza/legibilidade").
func clarityComponent(r CanonicalResume) int {
	bullets := resumeBullets(r)
	if len(bullets) == 0 {
		return 100
	}
	totalWords := 0
	for _, b := range bullets {
		totalWords += len(strings.Fields(b))
	}
	avg := float64(totalWords) / float64(len(bullets))
	switch {
	case avg >= 8 && avg <= 30:
		return 100
	case avg < 8:
		return boundedScore(int(math.Round(math.Max(40, 40+(avg/8)*60))))
	default:
		return boundedScore(int(math.Round(math.Max(40, 100-((avg-30)/30)*60))))
	}
}

// hrScore implements the Appendix F HR rubric: experience fit 30% / impact
// 25% / skills 20% / trajectory 15% / clarity 10%. Job-hopping/gap
// penalties are deferred to v2 (brainstorm §7.6/§10).
func hrScore(r CanonicalResume, req jobRequirements) (int, scoreBreakdown) {
	breakdown := scoreBreakdown{
		"experienceFit": weighted(experienceFitComponent(r, req), 30),
		"impact":        weighted(impactSubScore(r), 25),
		"skills":        weighted(skillsDemonstratedComponent(r), 20),
		"trajectory":    weighted(trajectoryComponent(r), 15),
		"clarity":       weighted(clarityComponent(r), 10),
	}
	return boundedScore(sumBreakdown(breakdown)), breakdown
}

func (a *api) resumeScore(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload scoreReq
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid request body."))
		return
	}

	canonical := normalizeCanonical(payload.Canonical)
	ats, atsBreakdown := atsScoreWithRawText(canonical, payload.Requirements, payload.RawText)
	hr, hrBreakdown := hrScore(canonical, payload.Requirements)

	writeJSON(w, http.StatusOK, scoreResult{
		ATS:          ats,
		HR:           hr,
		ATSBreakdown: atsBreakdown,
		HRBreakdown:  hrBreakdown,
	})
}
