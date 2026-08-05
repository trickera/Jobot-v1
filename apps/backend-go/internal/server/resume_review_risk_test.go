package server

import (
	"context"
	"io"
	"log"
	"testing"
)

func TestAssessPatchReviewRiskNewMetric(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/experience/0/bullets/0", `"Reduced cloud costs by 30% using AWS rightsizing."`, "quantify impact")
	got := assessPatchReviewRisk(base, op, jobRequirements{}, nil)
	if got != reviewRiskNewMetric {
		t.Fatalf("expected new_metric, got %q", got)
	}
}

func TestAssessPatchReviewRiskNewSkill(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/summary", `"Backend engineer with Kubernetes expertise."`, "align summary")
	req := jobRequirements{HardRequirements: []string{"Kubernetes"}}
	got := assessPatchReviewRisk(base, op, req, nil)
	if got != reviewRiskNewSkill {
		t.Fatalf("expected new_skill, got %q", got)
	}
}

func TestAssessPatchReviewRiskIdentityChange(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/experience/0/company", `"NotAcme"`, "typo fix")
	got := assessPatchReviewRisk(base, op, jobRequirements{}, nil)
	if got != reviewRiskIdentityChange {
		t.Fatalf("expected identity_change, got %q", got)
	}
}

func TestAssessPatchReviewRiskRejectsHeadlineTitleEscalation(t *testing.T) {
	// Installed-app E2E: a Digital Marketing Manager targeting a Director job
	// received a pre-accepted "Safe" /basics/headline patch claiming the target
	// title. A headline is user-visible identity prose in the exported PDF, not
	// a free target field, so changing it must go through the critical identity
	// gate just like changing an experience role.
	base := sampleTailoringBaseResume()
	base.Basics.Headline = "Digital Marketing Manager"
	op := patchOp("replace", "/basics/headline", `"Director of Growth Marketing | Performance & Lifecycle Strategy"`, "align headline")

	got := assessPatchReviewRisk(base, op, jobRequirements{}, nil)
	if got != reviewRiskIdentityChange {
		t.Fatalf("expected headline escalation to be identity_change, got %q", got)
	}
}

func TestAssessPatchReviewRiskNewCertification(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/certifications/-", `{"name":"CKA","issuer":"CNCF","year":"2023"}`, "add cert")
	got := assessPatchReviewRisk(base, op, jobRequirements{}, nil)
	if got != reviewRiskNewCertification {
		t.Fatalf("expected new_certification, got %q", got)
	}
}

func TestAssessPatchReviewRiskCosmeticRewriteNoRisk(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/summary", `"Backend engineer."`, "minor polish")
	got := assessPatchReviewRisk(base, op, jobRequirements{}, nil)
	if got != "" {
		t.Fatalf("expected no review risk, got %q", got)
	}
}

func TestDecodeTailorResultRejectsCriticalReviewRisk(t *testing.T) {
	base := sampleTailoringBaseResume()
	raw := `[{"op":"replace","path":"/experience/0/bullets/0","value":"Reduced costs by 30%.","reason":"metric"},{"op":"replace","path":"/experience/0/company","value":"FakeCorp","reason":"company"}]`
	result, err := decodeTailorResult(base, jobRequirements{}, nil, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Patches) != 1 {
		t.Fatalf("expected metric patch accepted with warning, got %+v", result.Patches)
	}
	if result.Patches[0].ReviewRisk != reviewRiskNewMetric {
		t.Fatalf("expected new_metric on accepted patch, got %q", result.Patches[0].ReviewRisk)
	}
	if len(result.Rejected) != 1 {
		t.Fatalf("expected company change rejected, got %+v", result.Rejected)
	}
	if result.Rejected[0].ReviewRisk != reviewRiskIdentityChange {
		t.Fatalf("expected identity_change on rejected patch, got %q", result.Rejected[0].ReviewRisk)
	}
}

func TestTailorResumeAttachesReviewRiskOnAcceptedPatch(t *testing.T) {
	raw := `[{"op":"replace","path":"/experience/0/bullets/0","value":"Reduced infrastructure spend by 30% with AWS automation.","reason":"quantify"}]`
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(raw)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}
	config := defaultConfig()
	config.Form.Provider = "gemini"

	result, err := a.tailorResume(context.Background(), config, "test-key", sampleTailoringBaseResume(), jobRequirements{}, nil, "en", "third")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Patches) != 1 || result.Patches[0].ReviewRisk != reviewRiskNewMetric {
		t.Fatalf("expected accepted patch with new_metric, got %+v", result.Patches)
	}
}
