package server

import (
	"regexp"
	"strings"
)

// resumeEmailPattern / resumePhonePattern are best-effort contact detectors
// used only by the offline heuristic parser below - they do not need to be
// as strict as a validation regex, only good enough to tell diagnoseResume
// "this resume has a way to reach the candidate".
var resumeEmailPattern = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
var resumePhonePattern = regexp.MustCompile(`(\+?\d[\d .()\-]{7,}\d)`)
var resumeYearPattern = regexp.MustCompile(`(19|20)\d{2}`)
var resumeRoleSeparator = regexp.MustCompile(`(?i)\s+([|—–-]|at|@)\s+`)

type resumeSearchSuggestion struct {
	Role      string
	Seniority string
	Levels    string
}

var resumeRoleIndicators = []string{
	"engineer", "engenheiro", "developer", "desenvolvedor", "analyst", "analista",
	"nurse", "enfermeiro", "enfermeira", "designer", "manager", "gerente",
	"coordinator", "coordenador", "specialist", "especialista", "technician", "tecnico",
	"architect", "arquiteto", "consultant", "consultor", "director", "diretor",
	"lead", "lider", "professor", "teacher", "docente", "pharmacist", "farmaceutico",
	"attorney", "advogado", "accountant", "contador", "marketing", "recruiter", "recrutador",
	"scientist", "cientista", "researcher", "pesquisador", "support", "suporte",
	"devops", "sre", "product", "produto", "administrator", "administrador",
	"sales", "vendas", "operations", "operacoes", "finance", "financeiro",
	"mechanic", "mecanico", "electrician", "eletricista", "physician", "medico",
	"therapist", "terapeuta", "chef", "editor", "writer", "redator", "auditor",
}

var resumeCompanyIndicators = []string{
	"inc", "llc", "ltda", "corp", "company", "group", "bank", "hospital",
	"university", "universidade", "investments", "investimentos", "technologies",
	"agency", "studio",
}

// detectResumeSearchProfile uses only the first experience heading in the
// uploaded resume. It never infers a profession or seniority from years or
// skills: the role and any level must be written in the resume itself.
func detectResumeSearchProfile(rawText string) resumeSearchSuggestion {
	inExperience := false
	seenLines := 0
	for _, line := range strings.Split(strings.ReplaceAll(rawText, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if section := classifyResumeSectionHeading(line); section != resumeSectionNone {
			if inExperience && section != resumeSectionExperience {
				break
			}
			inExperience = section == resumeSectionExperience
			continue
		}
		if !inExperience {
			continue
		}
		role, score := resumeRoleCandidate(line)
		if score >= 3 {
			return resumeSearchSuggestionForRole(role)
		}
		// A dated line marks the boundary between the position header and its
		// description. Do not promote later prose such as "operations" to a role.
		if resumeYearPattern.MatchString(line) {
			break
		}
		seenLines++
		if seenLines >= 6 {
			break
		}
	}
	return resumeSearchSuggestion{}
}

func resumeRoleCandidate(line string) (string, int) {
	if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "•") || strings.HasPrefix(line, "*") {
		return "", -1
	}
	parts := resumeRoleSeparator.Split(line, -1)
	best, bestScore := "", -1
	for _, part := range parts {
		candidate := strings.TrimSpace(strings.Trim(part, "|—–-•"))
		score := resumeRoleCandidateScore(candidate)
		if score > bestScore {
			best, bestScore = candidate, score
		}
	}
	return best, bestScore
}

func resumeRoleCandidateScore(candidate string) int {
	words := strings.Fields(candidate)
	if len(words) == 0 || len(words) > 12 || len(candidate) > 120 ||
		resumeEmailPattern.MatchString(candidate) || resumePhonePattern.MatchString(candidate) ||
		strings.Contains(strings.ToLower(candidate), "http") || classifyResumeSectionHeading(candidate) != resumeSectionNone {
		return -1
	}
	normalized := normalizeText(candidate)
	indicators := 0
	for _, term := range resumeRoleIndicators {
		if containsTerm(normalized, term) {
			indicators++
		}
	}
	if indicators == 0 {
		if resumeYearPattern.MatchString(candidate) || strings.Contains(candidate, ",") || strings.Contains(strings.ToLower(candidate), ".com") {
			return -1
		}
		for _, term := range resumeCompanyIndicators {
			if containsTerm(normalized, term) {
				return -1
			}
		}
	}
	return 1 + indicators*2
}

func resumeSearchSuggestionForRole(role string) resumeSearchSuggestion {
	role = strings.TrimSpace(role)
	if role == "" {
		return resumeSearchSuggestion{}
	}
	seniority, levels := explicitResumeSeniority(role)
	return resumeSearchSuggestion{Role: role, Seniority: seniority, Levels: levels}
}

func explicitResumeSeniority(role string) (string, string) {
	found := map[string]bool{}
	for _, level := range detectSeniorityLevels(role) {
		found[level] = true
	}
	for _, level := range []string{"lead", "principal", "staff", "manager", "especialista", "senior", "pleno", "junior", "intern"} {
		if !found[level] {
			continue
		}
		switch level {
		case "lead":
			return "Lead", "Lead"
		case "principal":
			return "Principal", "Principal"
		case "staff":
			return "Staff", "Staff"
		case "manager":
			return "Manager", "Manager, Gerente, Coordenador"
		case "especialista":
			return "Especialista", "Especialista, Specialist"
		case "senior":
			return "Senior", "Senior, Sr, Sênior"
		case "pleno":
			return "Pleno", "Pleno, Mid-Level"
		case "junior":
			return "Junior", "Junior, Jr, Júnior"
		case "intern":
			return "Estágio", "Estágio, Intern, Trainee"
		}
	}
	return "", ""
}

type resumeHeuristicSection int

const (
	resumeSectionNone resumeHeuristicSection = iota
	resumeSectionSummary
	resumeSectionExperience
	resumeSectionEducation
	resumeSectionLicenses
	resumeSectionCertifications
	resumeSectionProjects
)

// classifyResumeSectionHeading recognizes common EN+PT resume section
// headings so the offline parser can bucket the lines that follow. Mirrors
// the keyword set already used by looksLikeResumeHeading/extractResumeKeywords
// for consistency, but reports WHICH section matched instead of just a bool.
func classifyResumeSectionHeading(line string) resumeHeuristicSection {
	trimmed := strings.TrimSpace(line)
	if len(trimmed) < 3 || len(trimmed) > 64 {
		return resumeSectionNone
	}
	normalized := normalizeText(trimmed)
	switch {
	case containsAnyTerm(normalized, "experiencia", "experience", "work history", "employment", "historico profissional"):
		return resumeSectionExperience
	case containsAnyTerm(normalized, "formacao", "educacao", "education", "academic"):
		return resumeSectionEducation
	case containsAnyTerm(normalized, "licencas", "licenses", "licensure", "professional registration"):
		return resumeSectionLicenses
	case containsAnyTerm(normalized, "certificacoes", "certifications", "certificates", "certificados"):
		return resumeSectionCertifications
	case containsAnyTerm(normalized, "projetos", "projects"):
		return resumeSectionProjects
	case containsAnyTerm(normalized, "resumo", "summary", "objetivo", "objective", "perfil", "profile", "about"):
		return resumeSectionSummary
	default:
		return resumeSectionNone
	}
}

func containsAnyTerm(haystack string, needles ...string) bool {
	for _, needle := range needles {
		if strings.Contains(haystack, needle) {
			return true
		}
	}
	return false
}

// buildHeuristicCanonical constructs a best-effort CanonicalResume purely
// from raw-text pattern matching - no AI call, no API key required. It is
// intentionally coarse (accurate company/role/date extraction still needs
// the AI parser) but gives diagnoseResume real section signal to work with
// instead of an all-empty structure, so a resume can get a genuinely useful
// offline ATS diagnostic before/without ever calling the AI-gated
// /resume/parse route (AGENTS.md "offline sem chave").
func buildHeuristicCanonical(rawText string) CanonicalResume {
	var r CanonicalResume
	r.SchemaVersion = currentResumeSchemaVersion

	if email := resumeEmailPattern.FindString(rawText); email != "" {
		r.Basics.Email = email
	}
	if phone := resumePhonePattern.FindString(rawText); phone != "" {
		r.Basics.Phone = strings.TrimSpace(phone)
	}

	sections := map[resumeHeuristicSection][]string{}
	current := resumeSectionNone
	for _, line := range strings.Split(rawText, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if section := classifyResumeSectionHeading(trimmed); section != resumeSectionNone {
			current = section
			continue
		}
		if current == resumeSectionNone {
			// Text before any recognized heading is treated as a summary/
			// header block (name, title, contact line, or a profile blurb
			// with no explicit "Summary" heading - common in real resumes).
			current = resumeSectionSummary
		}
		sections[current] = append(sections[current], trimmed)
	}

	if lines := sections[resumeSectionSummary]; len(lines) > 0 {
		r.Summary = truncate(strings.Join(lines, " "), 1200)
	}

	if lines := sections[resumeSectionExperience]; len(lines) > 0 {
		exp := ResumeExperience{Bullets: capResumeLines(lines, 40)}
		if year := resumeYearPattern.FindString(strings.Join(lines, " ")); year != "" {
			exp.Start = year
		}
		r.Experience = append(r.Experience, exp)
	}

	if lines := sections[resumeSectionEducation]; len(lines) > 0 {
		edu := ResumeEducation{Institution: truncate(lines[0], 200)}
		if year := resumeYearPattern.FindString(strings.Join(lines, " ")); year != "" {
			edu.Start = year
		}
		r.Education = append(r.Education, edu)
	}

	if lines := sections[resumeSectionProjects]; len(lines) > 0 {
		r.Projects = append(r.Projects, ResumeProject{Bullets: capResumeLines(lines, 40)})
	}

	if lines := sections[resumeSectionLicenses]; len(lines) > 0 {
		for _, line := range capResumeLines(lines, 10) {
			for _, credential := range strings.Split(line, ";") {
				credential = strings.TrimSpace(credential)
				if credential == "" {
					continue
				}
				if looksLikeProfessionalLicense(credential) {
					r.Licenses = append(r.Licenses, parseHeuristicLicense(credential))
				} else {
					for _, certification := range strings.Split(credential, ",") {
						certification = strings.TrimSpace(certification)
						if certification != "" {
							r.Certifications = append(r.Certifications, ResumeCertification{Name: truncate(certification, 200)})
						}
					}
				}
			}
		}
	}

	if lines := sections[resumeSectionCertifications]; len(lines) > 0 {
		for _, line := range capResumeLines(lines, 10) {
			r.Certifications = append(r.Certifications, ResumeCertification{Name: truncate(line, 200)})
		}
	}

	if keywords := extractResumeKeywords(rawText); len(keywords) > 0 {
		r.Skills.Hard = keywords
	}

	return r
}

func looksLikeProfessionalLicense(value string) bool {
	normalized := normalizeText(value)
	return containsAnyTerm(normalized, "license", "licenca", "licensure", "registered nurse", "state of") ||
		containsTerm(normalized, "rn")
}

func parseHeuristicLicense(value string) ResumeLicense {
	parts := strings.SplitN(strings.TrimSpace(value), ",", 2)
	license := ResumeLicense{Name: truncate(strings.TrimSpace(parts[0]), 200)}
	if len(parts) == 2 {
		license.Jurisdiction = truncate(strings.TrimSpace(parts[1]), 200)
	}
	return license
}

// isEmptyCanonical reports whether a CanonicalResume carries no real
// structure yet - the signal that no AI parse (or an equivalent caller-
// supplied structure) has happened, so the offline diagnostic should fall
// back to buildHeuristicCanonical instead of scoring an all-empty resume.
func isEmptyCanonical(r CanonicalResume) bool {
	return strings.TrimSpace(r.Basics.Name) == "" &&
		strings.TrimSpace(r.Summary) == "" &&
		len(r.Experience) == 0 &&
		len(r.Education) == 0 &&
		len(r.Licenses) == 0 &&
		len(r.Certifications) == 0 &&
		len(r.Skills.Hard) == 0 && len(r.Skills.Soft) == 0 && len(r.Skills.Tools) == 0
}

func capResumeLines(lines []string, max int) []string {
	if len(lines) <= max {
		return append([]string{}, lines...)
	}
	return append([]string{}, lines[:max]...)
}
