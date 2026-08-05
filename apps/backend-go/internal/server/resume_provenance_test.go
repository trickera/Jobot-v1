package server

import (
	"encoding/json"
	"strings"
	"testing"
)

func licensedNurseResume() CanonicalResume {
	return CanonicalResume{
		Basics:   ResumeBasics{Name: "Priya Raghunathan, RN, BSN"},
		Summary:  "Registered nurse with nine years in acute care.",
		Licenses: []ResumeLicense{{Name: "Registered Nurse", Jurisdiction: "State of Illinois"}},
		Certifications: []ResumeCertification{
			{Name: "CCRN"}, {Name: "ACLS"}, {Name: "BLS"},
		},
	}
}

func TestResumeEvidenceResolvesCredentialAliasesWithStrongProvenance(t *testing.T) {
	base := licensedNurseResume()
	evidence, source, ok := resumeEvidence(base, nil, "active RN license")
	if !ok || source != resumeEvidenceLicense || !strings.Contains(evidence, "State of Illinois") {
		t.Fatalf("expected strong RN license evidence, got evidence=%q source=%q ok=%v", evidence, source, ok)
	}
	if _, source, ok := resumeEvidence(base, nil, "Basic Life Support certification"); !ok || source != resumeEvidenceCertification {
		t.Fatalf("expected BLS alias to resolve to certification provenance, got source=%q ok=%v", source, ok)
	}
}

func TestFreeTextMentionDoesNotProveAProfessionalLicense(t *testing.T) {
	base := CanonicalResume{Summary: "Registered nurse with nine years in acute care."}
	if _, source, ok := resumeEvidence(base, nil, "active RN license"); !ok || source != resumeEvidenceFreeText {
		t.Fatalf("expected the resolver to expose weak free-text provenance, got source=%q ok=%v", source, ok)
	}
	if hasRealEvidence(base, nil, gapItem{Term: "active RN license"}) {
		t.Fatal("a job license requirement must not be proven by a job-title/summary mention alone")
	}
}

func TestGapEvidenceGateKeepsActiveRNLicenseFound(t *testing.T) {
	gap := enforceGapEvidenceGate(licensedNurseResume(), gapResult{
		Found: []gapItem{{Term: "active RN license", Evidence: "Registered Nurse, State of Illinois"}},
	})
	if len(gap.Found) != 1 || len(gap.ToConfirm) != 0 {
		t.Fatalf("expected the real license to remain found, got %+v", gap)
	}
}

func TestCredentialGatesShareResumeEvidence(t *testing.T) {
	base := licensedNurseResume()
	if !skillAllowed(base, nil, "RN", nil) {
		t.Fatal("expected skill gate to honor the canonical license")
	}
	if !certificationAllowed(base, nil, "Registered Nurse") {
		t.Fatal("expected certification gate to recognize the strong credential")
	}
	known := jsonPatchOp{Op: "add", Path: "/licenses/-", Value: json.RawMessage(`{"name":"RN","jurisdiction":"State of Illinois"}`)}
	if ok, reason := validateTailoringPatch(base, nil, known); !ok {
		t.Fatalf("expected an evidenced license patch to pass, got %s", reason)
	}
	unknown := jsonPatchOp{Op: "add", Path: "/licenses/-", Value: json.RawMessage(`{"name":"California physician license"}`)}
	if ok, _ := validateTailoringPatch(base, nil, unknown); ok {
		t.Fatal("expected an unevidenced license patch to be rejected")
	}
	if risk := assessNewCertification(base, unknown, nil); risk != reviewRiskNewCertification {
		t.Fatalf("expected a new license to receive certification-level review risk, got %q", risk)
	}
}

func TestResumeParsePromptIncludesLicenseBucketAndCurrentSchema(t *testing.T) {
	prompt := resumeParsePrompt("Priya Raghunathan, RN")
	if !strings.Contains(prompt, "licenses[{name,issuer,jurisdiction,number,expires}]") ||
		!strings.Contains(prompt, "schemaVersion=2") {
		t.Fatalf("parse prompt does not describe the license-aware schema: %s", prompt)
	}
}
