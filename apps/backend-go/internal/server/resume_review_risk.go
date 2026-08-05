package server

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

const (
	reviewRiskNewMetric           = "new_metric"
	reviewRiskNewSkill            = "new_skill"
	reviewRiskNewCertification    = "new_certification"
	reviewRiskIdentityChange      = "identity_change"
	reviewRiskUnsupportedClaim    = "unsupported_claim"
	reviewRiskHighRewriteDistance = "high_rewrite_distance"
)

var (
	pathReviewIdentityLeaf  = regexp.MustCompile(`^/(experience|education)/\d+/(company|role|institution|degree|start|end)$`)
	pathReviewBullet        = regexp.MustCompile(`^/(experience|education|projects)/\d+/bullets/\d+$`)
	pathReviewSummary       = regexp.MustCompile(`^/summary$`)
	metricPattern           = regexp.MustCompile(`(?i)(\d+\s*%|\$\s*\d|R\$\s*\d|\d+\s*x\b|\b\d+\s*million\b|\b\d+\s*mil\b|doubled|reduced by \d+|increased by \d+)`)
	unsupportedClaimPattern = regexp.MustCompile(`(?i)\b(led|architected|expert in|spearheaded|pioneered)\b`)
)

// assessPatchReviewRisk scores a gate-approved patch for content risks the
// structural anti-invention gate does not cover (metrics, unsupported
// rewrites, job skills surfaced without evidence).
func assessPatchReviewRisk(base CanonicalResume, patch jsonPatchOp, req jobRequirements, confirmed []string) string {
	if patch.Op == "remove" {
		return ""
	}

	if risk := assessIdentityChange(base, patch); risk != "" {
		return risk
	}
	if risk := assessNewCertification(base, patch, confirmed); risk != "" {
		return risk
	}

	newText := patchValueText(patch.Value)
	if newText == "" {
		return ""
	}

	original := originalTextAtPath(base, patch.Path)
	if risk := assessNewMetric(newText, original); risk != "" {
		return risk
	}
	if risk := assessNewSkill(base, newText, req, confirmed); risk != "" {
		return risk
	}
	if risk := assessUnsupportedClaim(newText, original); risk != "" {
		return risk
	}
	if pathReviewBullet.MatchString(patch.Path) || pathReviewSummary.MatchString(patch.Path) {
		if rewriteDistanceHigh(original, newText) {
			return reviewRiskHighRewriteDistance
		}
	}
	return ""
}

func isCriticalReviewRisk(risk string) bool {
	return risk == reviewRiskIdentityChange || risk == reviewRiskNewCertification
}

func assessIdentityChange(base CanonicalResume, patch jsonPatchOp) string {
	path := normalizeTailoringPatchPath(patch.Path)
	if path == "/basics/name" {
		newName := strings.Trim(patchStringValue(patch.Value), `"`)
		if newName != "" && !strings.EqualFold(normalizeText(newName), normalizeText(base.Basics.Name)) {
			return reviewRiskIdentityChange
		}
	}
	if path == "/basics/headline" {
		newHeadline := strings.Trim(patchStringValue(patch.Value), `"`)
		if newHeadline != "" && !strings.EqualFold(normalizeText(newHeadline), normalizeText(base.Basics.Headline)) {
			return reviewRiskIdentityChange
		}
	}
	if !pathReviewIdentityLeaf.MatchString(path) {
		return ""
	}
	original := originalTextAtPath(base, path)
	newText := patchValueText(patch.Value)
	if newText == "" || original == "" {
		return ""
	}
	if normalizeText(newText) != normalizeText(original) {
		return reviewRiskIdentityChange
	}
	return ""
}

func assessNewCertification(base CanonicalResume, patch jsonPatchOp, confirmed []string) string {
	if (patch.Op != "add" && patch.Op != "replace") ||
		(!strings.HasPrefix(patch.Path, "/certifications") && !strings.HasPrefix(patch.Path, "/licenses")) {
		return ""
	}
	name := ""
	if strings.HasPrefix(patch.Path, "/licenses") {
		var license ResumeLicense
		if err := json.Unmarshal(patch.Value, &license); err == nil {
			name = license.Name
		}
	} else {
		var cert ResumeCertification
		if err := json.Unmarshal(patch.Value, &cert); err == nil {
			name = cert.Name
		}
	}
	if name == "" {
		var name string
		if err := json.Unmarshal(patch.Value, &name); err != nil {
			return ""
		}
		if _, source, ok := resumeEvidence(base, confirmed, name); ok && source != resumeEvidenceFreeText {
			return ""
		}
		return reviewRiskNewCertification
	}
	if strings.TrimSpace(name) == "" {
		return ""
	}
	if _, source, ok := resumeEvidence(base, confirmed, name); ok && source != resumeEvidenceFreeText {
		return ""
	}
	return reviewRiskNewCertification
}

func assessNewMetric(newText, original string) string {
	if !metricPattern.MatchString(newText) {
		return ""
	}
	if original != "" && metricPattern.MatchString(original) {
		return ""
	}
	return reviewRiskNewMetric
}

func assessNewSkill(base CanonicalResume, newText string, req jobRequirements, confirmed []string) string {
	terms := append(append([]string{}, req.HardRequirements...), req.NiceToHave...)
	for _, term := range terms {
		term = strings.TrimSpace(term)
		if term == "" {
			continue
		}
		if !containsTerm(normalizeText(newText), term) {
			continue
		}
		if _, source, ok := resumeEvidence(base, confirmed, term); ok && resumeEvidenceSupportsClaim(term, source) {
			continue
		}
		return reviewRiskNewSkill
	}
	return ""
}

func assessUnsupportedClaim(newText, original string) string {
	if !unsupportedClaimPattern.MatchString(newText) {
		return ""
	}
	if original != "" && unsupportedClaimPattern.MatchString(original) {
		return ""
	}
	return reviewRiskUnsupportedClaim
}

func rewriteDistanceHigh(original, updated string) bool {
	origTokens := tokenSet(original)
	newTokens := tokenSet(updated)
	if len(newTokens) == 0 {
		return false
	}
	if len(origTokens) == 0 {
		return len(newTokens) >= 4
	}
	shared := 0
	for token := range newTokens {
		if origTokens[token] {
			shared++
		}
	}
	changedRatio := 1 - float64(shared)/float64(len(newTokens))
	return changedRatio > 0.6
}

func tokenSet(text string) map[string]bool {
	tokens := make(map[string]bool)
	for _, part := range strings.Fields(normalizeText(text)) {
		if len(part) < 2 {
			continue
		}
		tokens[part] = true
	}
	return tokens
}

func patchValueText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return strings.TrimSpace(string(raw))
}

func patchStringValue(raw json.RawMessage) string {
	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		return asString
	}
	return strings.TrimSpace(strings.Trim(string(raw), `"`))
}

func originalTextAtPath(base CanonicalResume, path string) string {
	path = normalizeTailoringPatchPath(path)
	if path == "/summary" {
		return base.Summary
	}
	if path == "/basics/name" {
		return base.Basics.Name
	}

	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) < 2 {
		return ""
	}

	switch parts[0] {
	case "experience":
		return originalExperiencePath(base, parts)
	case "education":
		return originalEducationPath(base, parts)
	case "projects":
		return originalProjectPath(base, parts)
	default:
		return ""
	}
}

func originalExperiencePath(base CanonicalResume, parts []string) string {
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 || idx >= len(base.Experience) {
		return ""
	}
	exp := base.Experience[idx]
	if len(parts) == 3 {
		switch parts[2] {
		case "company":
			return exp.Company
		case "role":
			return exp.Role
		case "start":
			return exp.Start
		case "end":
			return exp.End
		}
	}
	if len(parts) == 4 && parts[2] == "bullets" {
		bulletIdx, err := strconv.Atoi(parts[3])
		if err != nil || bulletIdx < 0 || bulletIdx >= len(exp.Bullets) {
			return ""
		}
		return exp.Bullets[bulletIdx]
	}
	return ""
}

func originalEducationPath(base CanonicalResume, parts []string) string {
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 || idx >= len(base.Education) {
		return ""
	}
	edu := base.Education[idx]
	if len(parts) == 3 {
		switch parts[2] {
		case "institution":
			return edu.Institution
		case "degree":
			return edu.Degree
		case "start":
			return edu.Start
		case "end":
			return edu.End
		}
	}
	return ""
}

func originalProjectPath(base CanonicalResume, parts []string) string {
	idx, err := strconv.Atoi(parts[1])
	if err != nil || idx < 0 || idx >= len(base.Projects) {
		return ""
	}
	proj := base.Projects[idx]
	if len(parts) == 4 && parts[2] == "bullets" {
		bulletIdx, err := strconv.Atoi(parts[3])
		if err != nil || bulletIdx < 0 || bulletIdx >= len(proj.Bullets) {
			return ""
		}
		return proj.Bullets[bulletIdx]
	}
	return ""
}

func applyReviewRiskToTailorResult(base CanonicalResume, req jobRequirements, confirmed []string, result tailorResult) tailorResult {
	var accepted, rejected []jsonPatchOp
	for _, op := range result.Patches {
		risk := assessPatchReviewRisk(base, op, req, confirmed)
		if isCriticalReviewRisk(risk) {
			op.ReviewRisk = risk
			op.Reason = strings.TrimSpace(op.Reason + " [rejected: " + risk + "]")
			rejected = append(rejected, op)
			continue
		}
		if risk != "" {
			op.ReviewRisk = risk
		}
		accepted = append(accepted, op)
	}
	for _, op := range result.Rejected {
		if op.ReviewRisk == "" {
			if risk := assessPatchReviewRisk(base, op, req, confirmed); risk != "" {
				op.ReviewRisk = risk
			}
		}
		rejected = append(rejected, op)
	}
	return tailorResult{Patches: accepted, Rejected: rejected}
}
