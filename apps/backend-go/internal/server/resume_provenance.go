package server

import "strings"

type resumeEvidenceSource string

const (
	resumeEvidenceLicense       resumeEvidenceSource = "license"
	resumeEvidenceCertification resumeEvidenceSource = "certification"
	resumeEvidenceConfirmed     resumeEvidenceSource = "confirmed"
	resumeEvidenceFreeText      resumeEvidenceSource = "resume_text"
)

var credentialNoiseWords = map[string]bool{
	"active": true, "current": true, "valid": true,
	"license": true, "licensed": true, "licensure": true,
	"certified": true, "certification": true, "certificate": true,
	"credential": true, "credentials": true,
}

var strongCredentialClaimWords = map[string]bool{
	"license": true, "licensed": true, "licensure": true,
	"certified": true, "certification": true, "certificate": true,
	"credential": true, "credentials": true,
}

var credentialAliasGroups = [][]string{
	{"rn", "registered nurse"},
	{"lpn", "licensed practical nurse"},
	{"bls", "basic life support"},
	{"acls", "advanced cardiac life support"},
	{"pals", "pediatric advanced life support"},
	{"ccrn", "critical care registered nurse"},
	{"tncc", "trauma nursing core course"},
}

// resumeEvidence is the single provenance resolver used by gap analysis,
// tailoring review and cover-letter warnings. It returns the concrete resume
// evidence and where it came from so strong credentials are never confused
// with an incidental free-text mention.
func resumeEvidence(base CanonicalResume, confirmed []string, term string) (string, resumeEvidenceSource, bool) {
	term = strings.TrimSpace(term)
	if term == "" {
		return "", "", false
	}

	for _, license := range base.Licenses {
		candidate := strings.Join([]string{license.Name, license.Issuer, license.Jurisdiction, license.Number, license.Expires}, " ")
		if resumeEvidenceMatches(candidate, term) {
			return licenseEvidenceText(license), resumeEvidenceLicense, true
		}
	}
	for _, cert := range base.Certifications {
		candidate := strings.Join([]string{cert.Name, cert.Issuer, cert.Year}, " ")
		if resumeEvidenceMatches(candidate, term) {
			return strings.TrimSpace(strings.Join([]string{cert.Name, cert.Issuer}, " — ")), resumeEvidenceCertification, true
		}
	}
	for _, value := range append(append([]string{}, confirmed...), base.ConfirmedSkills...) {
		if resumeEvidenceMatches(value, term) || resumeEvidenceMatches(term, value) {
			return value, resumeEvidenceConfirmed, true
		}
	}

	// Credential arrays were checked above with strong provenance. Remove them
	// before the generic corpus check so a strong claim cannot be mislabeled as
	// an incidental free-text mention.
	freeText := base
	freeText.Licenses = nil
	freeText.Certifications = nil
	if resumeEvidenceMatches(resumeSearchableText(freeText), term) {
		return term, resumeEvidenceFreeText, true
	}
	return "", "", false
}

func resumeEvidenceSupportsClaim(term string, source resumeEvidenceSource) bool {
	if source != resumeEvidenceFreeText {
		return true
	}
	for _, field := range strings.Fields(normalizeText(term)) {
		if strongCredentialClaimWords[strings.Trim(field, ".,;:()[]{}")] {
			return false
		}
	}
	return true
}

func licenseEvidenceText(license ResumeLicense) string {
	parts := []string{strings.TrimSpace(license.Name)}
	if jurisdiction := strings.TrimSpace(license.Jurisdiction); jurisdiction != "" {
		parts = append(parts, jurisdiction)
	} else if issuer := strings.TrimSpace(license.Issuer); issuer != "" {
		parts = append(parts, issuer)
	}
	return strings.Join(parts, " — ")
}

func resumeEvidenceMatches(text, term string) bool {
	normalizedText := normalizeText(text)
	if normalizedText == "" {
		return false
	}
	if containsTerm(normalizedText, term) {
		return true
	}
	for _, form := range credentialForms(term) {
		if containsTerm(normalizedText, form) {
			return true
		}
	}

	// Preserve the existing conservative scattered-token behavior for atomic
	// skills. Full requirement sentences remain phrase-only.
	tokens := significantTerms(term)
	if len(tokens) == 0 || len(tokens) > shortTermMaxTokens {
		return false
	}
	for _, token := range tokens {
		if !containsTerm(normalizedText, token) {
			return false
		}
	}
	return true
}

func credentialForms(term string) []string {
	fields := strings.Fields(normalizeText(term))
	kept := make([]string, 0, len(fields))
	for _, field := range fields {
		field = strings.Trim(field, ".,;:()[]{}")
		if field == "" || credentialNoiseWords[field] || evidenceStopwords[field] {
			continue
		}
		kept = append(kept, field)
	}
	cleaned := strings.Join(kept, " ")
	forms := []string{cleaned}
	for _, group := range credentialAliasGroups {
		matched := false
		for _, alias := range group {
			if cleaned == normalizeText(alias) || containsTerm(cleaned, alias) {
				matched = true
				break
			}
		}
		if matched {
			forms = append(forms, group...)
		}
	}
	return dedupeStrings(trimStrings(forms))
}
