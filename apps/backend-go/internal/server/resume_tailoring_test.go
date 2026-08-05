package server

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func sampleTailoringBaseResume() CanonicalResume {
	return CanonicalResume{
		Basics:  ResumeBasics{Name: "Jane Doe", Email: "jane@example.com"},
		Summary: "Backend engineer.",
		Skills:  ResumeSkills{Hard: []string{"AWS", "Docker"}},
		Experience: []ResumeExperience{
			{Company: "Acme", Role: "Engineer", Start: "2020-01", End: "present", Bullets: []string{"Worked on backend systems."}},
			{Company: "Globex", Role: "Junior Engineer", Start: "2018-01", End: "2019-12", Bullets: []string{"Supported the platform team."}},
		},
		Education: []ResumeEducation{{Institution: "State University", Degree: "BSc", Start: "2014", End: "2018"}},
	}
}

func patchOp(op, path, value, reason string) jsonPatchOp {
	return jsonPatchOp{Op: op, Path: path, Value: json.RawMessage(value), Reason: reason}
}

func TestValidateTailoringPatchAllowsSummaryRewrite(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/summary", `"Cloud-focused backend engineer with AWS and Docker experience."`, "align summary to job")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected summary rewrite to be allowed, got rejection: %s", reason)
	}
}

func TestValidateTailoringPatchAllowsBulletRewrite(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/experience/0/bullets/0", `"Automated backend deployments using AWS and Docker."`, "add keywords")
	ok, _ := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatal("expected bullet rewrite to be allowed")
	}
}

func TestValidateTailoringPatchRejectsNewUnconfirmedSkillItem(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/skills/hard/-", `"Kubernetes"`, "add relevant skill")
	ok, reason := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected unconfirmed new skill to be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestValidateTailoringPatchAllowsConfirmedSkillItem(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/skills/hard/-", `"Kubernetes"`, "add confirmed skill")
	ok, _ := validateTailoringPatch(base, []string{"Kubernetes"}, op)
	if !ok {
		t.Fatal("expected confirmed skill to be allowed")
	}
}

func TestValidateTailoringPatchAllowsSkillAlreadyInResumeText(t *testing.T) {
	base := sampleTailoringBaseResume()
	base.Summary = "Experienced with Terraform-based infrastructure."
	op := patchOp("add", "/skills/hard/-", `"Terraform"`, "surface existing skill")
	ok, _ := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatal("expected a skill already present in the resume text to be allowed")
	}
}

func TestValidateTailoringPatchAllowsSkillFromPersistedConfirmedSkills(t *testing.T) {
	// A confirmation the user made in an earlier session is persisted on the
	// resume (ConfirmedSkills) and must survive without being re-passed in the
	// request's confirmed list — that is what makes confirmation durable.
	base := sampleTailoringBaseResume()
	base.ConfirmedSkills = []string{"Kubernetes"}
	op := patchOp("add", "/skills/hard/-", `"Kubernetes"`, "add durably-confirmed skill")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected a persisted confirmed skill to be allowed with no request confirmations, got: %s", reason)
	}
}

func TestValidateTailoringPatchAllowsConfirmedSkillWithFormattingVariation(t *testing.T) {
	// The user confirmed "Kubernetes"; the tailoring AI proposes the same
	// capability worded slightly differently ("Kubernetes (K8s)"). Exact
	// string equality rejected this, so a user who did exactly what the UI
	// asked still had their confirmation silently ignored.
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/skills/hard/-", `"Kubernetes (K8s)"`, "add confirmed skill")
	ok, reason := validateTailoringPatch(base, []string{"Kubernetes"}, op)
	if !ok {
		t.Fatalf("expected a confirmed skill with a formatting variation to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsWholeArraySkillsWithUnconfirmedEntry(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/skills/hard", `["AWS","Docker","Kubernetes"]`, "reorganize skills")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected whole-array skills replace with an unconfirmed new entry to be rejected")
	}
}

func TestValidateTailoringPatchAllowsWholeArraySkillsReorder(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/skills/hard", `["Docker","AWS"]`, "prioritize Docker")
	ok, _ := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatal("expected reordering existing skills to be allowed")
	}
}

func TestValidateTailoringPatchRejectsNewExperienceEntry(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/experience/-", `{"company":"FakeCorp","role":"Staff Engineer","start":"2022-01","end":"present","bullets":[]}`, "add relevant experience")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a brand-new experience entry to be rejected")
	}
}

func TestValidateTailoringPatchRejectsExperienceCompanyChange(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/experience/0/company", `"NotAcme"`, "typo fix")
	ok, reason := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected company field modification to be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestValidateTailoringPatchRejectsExperienceDatesChange(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/experience/0/start", `"2019-01"`, "extend tenure")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected start-date modification to be rejected")
	}
}

func TestValidateTailoringPatchAllowsExperienceRemoval(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("remove", "/experience/1", "", "remove irrelevant older role")
	ok, _ := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatal("expected removing an entire experience entry to be allowed")
	}
}

func TestValidateTailoringPatchAllowsExperienceReorderPreservingIdentity(t *testing.T) {
	base := sampleTailoringBaseResume()
	reordered := []ResumeExperience{base.Experience[1], base.Experience[0]}
	reordered[1].Bullets = []string{"Rewrote bullet with AWS and Docker keywords."}
	value, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	op := patchOp("replace", "/experience", string(value), "reorder by relevance")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected identity-preserving reorder to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsExperienceReplaceIntroducingNewEntry(t *testing.T) {
	base := sampleTailoringBaseResume()
	tampered := make([]ResumeExperience, len(base.Experience))
	copy(tampered, base.Experience)
	tampered[0].Company = "FakeCorp"
	value, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	op := patchOp("replace", "/experience", string(value), "reorder by relevance")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a fabricated company introduced via whole-array replace to be rejected")
	}
}

func TestValidateTailoringPatchRejectsIndexedSkillReplaceIntroducingUnconfirmedSkill(t *testing.T) {
	// Gate loophole: the fabrication check for a single skill slot only fired
	// on op == "add". An indexed "replace" (e.g. /skills/hard/0) fell through
	// to the permissive default and let a fabricated skill in undetected.
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/skills/hard/0", `"Kubernetes"`, "swap in a trendier skill")
	ok, reason := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected an indexed skill replace with an unconfirmed skill to be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestValidateTailoringPatchAllowsIndexedSkillReplaceWithConfirmedSkill(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("replace", "/skills/hard/0", `"Kubernetes"`, "confirmed skill")
	ok, _ := validateTailoringPatch(base, []string{"Kubernetes"}, op)
	if !ok {
		t.Fatal("expected an indexed skill replace with a confirmed skill to be allowed")
	}
}

func TestValidateTailoringPatchAllowsIndividualSkillRemoval(t *testing.T) {
	// Phase 4: the migrated prompt drops skills via an individual remove op
	// instead of a whole-array replace. Removing a skill never fabricates
	// anything, so it must be allowed.
	base := sampleTailoringBaseResume()
	op := patchOp("remove", "/skills/hard/0", "", "drop an irrelevant skill")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected individual skill removal to be allowed, got rejection: %s", reason)
	}
}

func TestDecodeTailorResultKeepsNewMetricBulletAsAcceptedWarning(t *testing.T) {
	// Phase 4: a bullet rewrite that introduces a metric absent from the
	// corresponding base bullet is flagged new_metric — but that is a WARNING
	// for the user to verify, not a hard block. It must stay in Patches
	// (accepted) carrying the reviewRisk, never moved to Rejected.
	base := sampleTailoringBaseResume() // experience[0].bullets[0] has no metric
	raw := `[{"op":"replace","path":"/experience/0/bullets/0","value":"Cut deploy time by 40% across 12 services.","reason":"quantify impact"}]`
	result, err := decodeTailorResult(base, jobRequirements{}, nil, raw)
	if err != nil {
		t.Fatalf("decodeTailorResult: %v", err)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("expected a new_metric bullet to stay accepted, but it was rejected: %+v", result.Rejected)
	}
	if len(result.Patches) != 1 {
		t.Fatalf("expected the bullet patch to be accepted, got %+v", result.Patches)
	}
	if result.Patches[0].ReviewRisk != reviewRiskNewMetric {
		t.Fatalf("expected reviewRisk %q on the accepted patch, got %q", reviewRiskNewMetric, result.Patches[0].ReviewRisk)
	}
	if isCriticalReviewRisk(reviewRiskNewMetric) {
		t.Fatal("new_metric must be a warning, not a critical (blocking) risk")
	}
}

func TestValidateTailoringPatchRejectsUnconfirmedCertification(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/certifications/-", `{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}`, "add relevant cert")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected unconfirmed certification to be rejected")
	}
}

func TestValidateTailoringPatchAllowsConfirmedCertification(t *testing.T) {
	base := sampleTailoringBaseResume()
	op := patchOp("add", "/certifications/-", `{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}`, "add confirmed cert")
	ok, _ := validateTailoringPatch(base, []string{"AWS Certified Solutions Architect"}, op)
	if !ok {
		t.Fatal("expected confirmed certification to be allowed")
	}
}

func sampleCertifiedBaseResume() CanonicalResume {
	base := sampleTailoringBaseResume()
	base.Certifications = []ResumeCertification{
		{Name: "CompTIA Security+", Issuer: "CompTIA", Year: "2021"},
		{Name: "Docker Certified Associate", Issuer: "Docker", Year: "2022"},
	}
	return base
}

func TestValidateTailoringPatchRejectsCertificationReplaceWithFabricatedCert(t *testing.T) {
	// Gate hole: the certification case only fired on op == "add"; a "replace"
	// on /certifications/<i> fell through to the permissive default, letting the
	// AI overwrite a real certification with a fabricated one undetected.
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications/0", `{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}`, "swap in a more relevant cert")
	ok, reason := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a fabricated certification via replace to be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestValidateTailoringPatchAllowsCertificationReplaceOfExistingCert(t *testing.T) {
	// Rewriting an entry while keeping the same real certification name (e.g.
	// fixing issuer formatting) does not fabricate anything.
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications/0", `{"name":"CompTIA Security+","issuer":"CompTIA (Computing Technology Industry Association)","year":"2021"}`, "clarify issuer")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected replacing a certification with the same real cert to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsCertificationReplaceOnMissingIndex(t *testing.T) {
	// prepareTailoringPatches upgrades a replace on a missing pointer to an
	// add, so a "replace" on an out-of-range index is effectively an append —
	// the gate must treat it exactly like an unconfirmed add.
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications/5", `{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}`, "add relevant cert")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a fabricated certification via replace-on-missing-index to be rejected")
	}
}

func TestValidateTailoringPatchAllowsConfirmedCertificationReplace(t *testing.T) {
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications/1", `{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}`, "swap in confirmed cert")
	ok, reason := validateTailoringPatch(base, []string{"AWS Certified Solutions Architect"}, op)
	if !ok {
		t.Fatalf("expected a user-confirmed certification replace to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsWholeArrayCertificationsIntroducingNewEntry(t *testing.T) {
	// A whole-array /certifications replace also fell through to the default;
	// each entry must correspond to a real (or user-confirmed) certification.
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications", `[{"name":"CompTIA Security+","issuer":"CompTIA","year":"2021"},{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}]`, "reorganize certs")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a whole-array certifications replace introducing a new cert to be rejected")
	}
}

func TestValidateTailoringPatchAllowsWholeArrayCertificationsReorder(t *testing.T) {
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications", `[{"name":"Docker Certified Associate","issuer":"Docker","year":"2022"},{"name":"CompTIA Security+","issuer":"CompTIA","year":"2021"}]`, "prioritize Docker cert")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected reordering existing certifications to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchAllowsCertificationRemoval(t *testing.T) {
	// Removing a certification never fabricates anything.
	base := sampleCertifiedBaseResume()
	op := patchOp("remove", "/certifications/1", "", "drop irrelevant cert")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected certification removal to be allowed, got rejection: %s", reason)
	}
}

func TestAssessNewCertificationFlagsReplaceOp(t *testing.T) {
	// The review-risk layer mirrored the gate's add-only blind spot: a
	// fabricated certification arriving via replace returned no risk at all.
	base := sampleCertifiedBaseResume()
	op := patchOp("replace", "/certifications/0", `{"name":"AWS Certified Solutions Architect","issuer":"AWS","year":"2023"}`, "swap cert")
	if risk := assessNewCertification(base, op, nil); risk != reviewRiskNewCertification {
		t.Fatalf("expected reviewRisk %q for a fabricated cert via replace, got %q", reviewRiskNewCertification, risk)
	}
	// Same-name replace stays clean.
	op = patchOp("replace", "/certifications/0", `{"name":"CompTIA Security+","issuer":"CompTIA","year":"2021"}`, "reformat")
	if risk := assessNewCertification(base, op, nil); risk != "" {
		t.Fatalf("expected no risk for replacing with the same real cert, got %q", risk)
	}
}

func sampleProjectLanguageBaseResume() CanonicalResume {
	base := sampleTailoringBaseResume()
	base.Projects = []ResumeProject{
		{Name: "Inventory API", Description: "Internal stock service", URL: "https://github.com/jane/inventory", Bullets: []string{"Built the REST API."}},
		{Name: "Legacy Migration", Description: "Batch ETL", URL: "", Bullets: []string{"Migrated nightly jobs."}},
	}
	base.Languages = []ResumeLanguage{
		{Language: "English", Fluency: "Fluent"},
		{Language: "Portuguese", Fluency: "Native"},
	}
	return base
}

func TestValidateTailoringPatchRejectsNewProjectEntry(t *testing.T) {
	// Gate hole: /projects had no case at all, so a wholly fabricated project
	// (with invented achievements) fell through to the permissive default.
	base := sampleProjectLanguageBaseResume()
	op := patchOp("add", "/projects/-", `{"name":"Payments Platform","description":"High-scale payments","url":"","bullets":["Architected a system handling 2M req/s"]}`, "add impressive project")
	ok, reason := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a fabricated project entry to be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestValidateTailoringPatchRejectsProjectEntryReplace(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/projects/0", `{"name":"Payments Platform","description":"High-scale payments","url":"","bullets":[]}`, "swap project")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected replacing a whole project entry to be rejected")
	}
}

func TestValidateTailoringPatchAllowsProjectRemoval(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("remove", "/projects/1", "", "drop irrelevant project")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected project removal to be allowed, got rejection: %s", reason)
	}
}

func TestValidateTailoringPatchAllowsProjectBulletRewrite(t *testing.T) {
	// Bullets are tailoring prose — rewriting them is the point (same rule as
	// experience bullets); the human diff review covers free-text honesty.
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/projects/0/bullets/0", `"Built the REST API consumed by 3 internal teams."`, "add keywords")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected project bullet rewrite to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsProjectNameChange(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/projects/0/name", `"Enterprise Payments Platform"`, "punchier name")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected project name modification to be rejected")
	}
}

func TestValidateTailoringPatchRejectsProjectURLChange(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/projects/0/url", `"https://github.com/other/repo"`, "update link")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected project URL modification to be rejected")
	}
}

func TestValidateTailoringPatchAllowsWholeArrayProjectsReorder(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	reordered := []ResumeProject{base.Projects[1], base.Projects[0]}
	reordered[1].Bullets = []string{"Rewrote bullet with relevant keywords."}
	value, err := json.Marshal(reordered)
	if err != nil {
		t.Fatal(err)
	}
	op := patchOp("replace", "/projects", string(value), "reorder by relevance")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected identity-preserving project reorder to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsWholeArrayProjectsIntroducingNewEntry(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	tampered := make([]ResumeProject, len(base.Projects))
	copy(tampered, base.Projects)
	tampered[0].Name = "Payments Platform"
	value, err := json.Marshal(tampered)
	if err != nil {
		t.Fatal(err)
	}
	op := patchOp("replace", "/projects", string(value), "reorder by relevance")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a fabricated project introduced via whole-array replace to be rejected")
	}
}

func TestValidateTailoringPatchRejectsNewUnconfirmedLanguage(t *testing.T) {
	// Gate hole: /languages had no case at all, so a fabricated language/
	// fluency claim passed the gate untouched.
	base := sampleProjectLanguageBaseResume()
	op := patchOp("add", "/languages/-", `{"language":"French","fluency":"Native"}`, "job asks for French")
	ok, reason := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected an unconfirmed new language to be rejected")
	}
	if reason == "" {
		t.Fatal("expected a rejection reason")
	}
}

func TestValidateTailoringPatchAllowsConfirmedLanguage(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("add", "/languages/-", `{"language":"French","fluency":"Professional"}`, "user confirmed French")
	ok, reason := validateTailoringPatch(base, []string{"French"}, op)
	if !ok {
		t.Fatalf("expected a user-confirmed language to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsLanguageFluencyUpgrade(t *testing.T) {
	// Upgrading a real language's fluency ("Fluent" -> "Native") is a new
	// claim the user never made — same class as changing dates.
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/languages/0/fluency", `"Native"`, "stronger claim")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected a fluency modification to be rejected")
	}
}

func TestValidateTailoringPatchRejectsLanguageEntryFluencyUpgradeViaReplace(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/languages/0", `{"language":"English","fluency":"Native"}`, "stronger claim")
	ok, _ := validateTailoringPatch(base, nil, op)
	if ok {
		t.Fatal("expected an entry replace that upgrades fluency to be rejected")
	}
}

func TestValidateTailoringPatchAllowsWholeArrayLanguagesReorder(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("replace", "/languages", `[{"language":"Portuguese","fluency":"Native"},{"language":"English","fluency":"Fluent"}]`, "put native language first")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected identity-preserving language reorder to be allowed, got: %s", reason)
	}
}

func TestValidateTailoringPatchAllowsLanguageRemoval(t *testing.T) {
	base := sampleProjectLanguageBaseResume()
	op := patchOp("remove", "/languages/1", "", "drop irrelevant language")
	ok, reason := validateTailoringPatch(base, nil, op)
	if !ok {
		t.Fatalf("expected language removal to be allowed, got rejection: %s", reason)
	}
}

func TestValidateTailoringPatchRejectsUnknownOpTypes(t *testing.T) {
	// jsonPatchOp has no "from" field, so a move/copy op that reached
	// applyPatches lost its source pointer and made evanphx fail the ENTIRE
	// batch (502) — every valid suggestion was thrown away with it. The gate
	// must reject anything that is not add/remove/replace so the stray op
	// lands in "rejected" (with a reason) and the rest of the batch survives.
	base := sampleTailoringBaseResume()
	for _, opType := range []string{"move", "copy", "test", "weird"} {
		op := patchOp(opType, "/summary", `"whatever"`, "reorganize")
		ok, reason := validateTailoringPatch(base, nil, op)
		if ok {
			t.Fatalf("expected op type %q to be rejected", opType)
		}
		if reason == "" {
			t.Fatalf("expected a rejection reason for op type %q", opType)
		}
	}
}

func TestDecodeTailorResultRejectsMoveOpButKeepsBatch(t *testing.T) {
	base := sampleTailoringBaseResume()
	raw := `[
		{"op":"move","from":"/experience/1","path":"/experience/0","reason":"reorder"},
		{"op":"replace","path":"/summary","value":"Cloud-focused backend engineer.","reason":"align summary"}
	]`
	result, err := decodeTailorResult(base, jobRequirements{}, nil, raw)
	if err != nil {
		t.Fatalf("decodeTailorResult: %v", err)
	}
	if len(result.Patches) != 1 || result.Patches[0].Path != "/summary" {
		t.Fatalf("expected the valid replace to survive, got %+v", result.Patches)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Op != "move" {
		t.Fatalf("expected the move op rejected, got %+v", result.Rejected)
	}
	// The surviving accepted batch must actually apply without error.
	if _, _, err := applyPatches(base, result.Patches); err != nil {
		t.Fatalf("expected accepted batch to apply cleanly, got: %v", err)
	}
}

func TestApplyPatchesBasicsSummaryAlias(t *testing.T) {
	base := sampleTailoringBaseResume()
	ops := []jsonPatchOp{
		patchOp("replace", "/basics/summary", `"Cloud-focused engineer."`, "align to job"),
	}
	result, _, err := applyPatches(base, ops)
	if err != nil {
		t.Fatalf("applyPatches: %v", err)
	}
	if result.Summary != "Cloud-focused engineer." {
		t.Fatalf("expected summary patched via /basics/summary alias, got %q", result.Summary)
	}
}

func TestPrepareTailoringPatchesUpgradesMissingReplace(t *testing.T) {
	baseJSON := []byte(`{"schemaVersion":1,"basics":{"name":"Jane Doe"},"skills":{"hard":["AWS"]}}`)
	ops := []jsonPatchOp{patchOp("replace", "/summary", `"New summary."`, "add summary")}
	prepared := prepareTailoringPatches(baseJSON, ops)
	if len(prepared) != 1 || prepared[0].Op != "add" {
		t.Fatalf("expected replace upgraded to add, got %+v", prepared)
	}
}

func TestApplyPatchesAppliesAcceptedOps(t *testing.T) {
	base := sampleTailoringBaseResume()
	ops := []jsonPatchOp{
		patchOp("replace", "/summary", `"Cloud-focused engineer."`, "align to job"),
	}
	result, _, err := applyPatches(base, ops)
	if err != nil {
		t.Fatalf("applyPatches: %v", err)
	}
	if result.Summary != "Cloud-focused engineer." {
		t.Fatalf("expected summary patched, got %q", result.Summary)
	}
	if result.Basics.Name != "Jane Doe" {
		t.Fatalf("expected the rest of the resume to be unaffected, got %+v", result.Basics)
	}
}

func TestApplyPatchesNoOpsReturnsBaseUnchanged(t *testing.T) {
	base := sampleTailoringBaseResume()
	result, _, err := applyPatches(base, nil)
	if err != nil {
		t.Fatalf("applyPatches: %v", err)
	}
	if result.Summary != base.Summary {
		t.Fatalf("expected unchanged resume, got %+v", result)
	}
}

func TestApplyPatchesProducesValidCanonical(t *testing.T) {
	base := sampleTailoringBaseResume()
	ops := []jsonPatchOp{
		patchOp("add", "/skills/hard/-", `"Kubernetes"`, "surfaced confirmed skill"),
	}
	result, _, err := applyPatches(base, ops)
	if err != nil {
		t.Fatalf("applyPatches: %v", err)
	}
	if err := result.Validate(); err != nil {
		t.Fatalf("expected patched resume to remain valid: %v", err)
	}
	found := false
	for _, s := range result.Skills.Hard {
		if s == "Kubernetes" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected Kubernetes to be present, got %+v", result.Skills.Hard)
	}
}

func TestResolveResumeLanguageDefaultsToEnglish(t *testing.T) {
	if got := resolveResumeLanguage("", jobRequirements{}); got != "English" {
		t.Fatalf("expected English default, got %q", got)
	}
	if got := resolveResumeLanguage("en", jobRequirements{}); got != "English" {
		t.Fatalf("expected English, got %q", got)
	}
}

func TestResolveResumeLanguagePortuguese(t *testing.T) {
	if got := resolveResumeLanguage("pt", jobRequirements{}); got != "Portuguese" {
		t.Fatalf("expected Portuguese, got %q", got)
	}
}

func TestResolveResumeLanguageAutoDetectsPortuguese(t *testing.T) {
	req := jobRequirements{JobTitle: "Desenvolvedor", HardRequirements: []string{"experiência em produção"}}
	if got := resolveResumeLanguage("auto", req); got != "Portuguese" {
		t.Fatalf("expected auto-detected Portuguese, got %q", got)
	}
}

func TestResolveResumeLanguageAutoFallsBackToEnglish(t *testing.T) {
	req := jobRequirements{JobTitle: "Software Engineer", HardRequirements: []string{"Kubernetes", "AWS"}}
	if got := resolveResumeLanguage("auto", req); got != "English" {
		t.Fatalf("expected auto fallback to English, got %q", got)
	}
}

func TestResolveResumeLanguageSpanish(t *testing.T) {
	if got := resolveResumeLanguage("es", jobRequirements{}); got != "Spanish" {
		t.Fatalf("expected Spanish, got %q", got)
	}
}

func TestResolveResumeLanguageAutoDetectsSpanish(t *testing.T) {
	req := jobRequirements{JobTitle: "Desarrollador", HardRequirements: []string{"experiencia en produccion", "vacante"}}
	if got := resolveResumeLanguage("auto", req); got != "Spanish" {
		t.Fatalf("expected auto-detected Spanish, got %q", got)
	}
}

func TestResolveResumeVoiceDefaultsToThird(t *testing.T) {
	if got := resolveResumeVoice(""); got != "third person (no first-person pronouns)" {
		t.Fatalf("expected third-person default, got %q", got)
	}
	if got := resolveResumeVoice("invalid-value"); got != "third person (no first-person pronouns)" {
		t.Fatalf("expected invalid voice to default to third person, got %q", got)
	}
}

func TestResolveResumeVoiceFirst(t *testing.T) {
	if got := resolveResumeVoice("first"); got != "first person (I/my)" {
		t.Fatalf("expected first-person voice, got %q", got)
	}
}

func TestTailorResumePromptAsksForIndividualRemoveOps(t *testing.T) {
	// Phase 4: reorganizing/dropping entries must be expressed as individual
	// add/remove operations, not a whole-array replace — a whole-array replace
	// that changes the count is rejected all-or-nothing, so one bad item used
	// to sink the entire batch. The prompt must steer the model to the granular
	// shape and forbid rewriting whole arrays.
	prompt := tailorResumePrompt("{}", "{}", "[]", "English", resolveResumeVoice("third"))
	if !strings.Contains(prompt, `"op":"remove"`) {
		t.Fatalf("expected the prompt to show the individual remove-operation shape, got: %s", prompt)
	}
	if !strings.Contains(prompt, "array inteiro") {
		t.Fatalf("expected the prompt to forbid rewriting a whole array, got: %s", prompt)
	}
}

func TestTailorResumePromptContainsVoiceInstruction(t *testing.T) {
	prompt := tailorResumePrompt("{}", "{}", "[]", "English", resolveResumeVoice("first"))
	if !strings.Contains(prompt, "first person") {
		t.Fatalf("expected prompt to contain the first-person instruction, got: %s", prompt)
	}

	prompt = tailorResumePrompt("{}", "{}", "[]", "English", resolveResumeVoice(""))
	if !strings.Contains(prompt, "third person") {
		t.Fatalf("expected prompt to default to third person, got: %s", prompt)
	}
}

// --- tailorResume (AI + gate integration) ---

const sampleTailoringPatchesJSON = `[{"op":"replace","path":"/summary","value":"Cloud-focused backend engineer.","reason":"align summary"},{"op":"add","path":"/skills/hard/-","value":"Kubernetes","reason":"add relevant skill"}]`

func TestTailorResumeSplitsAcceptedAndRejected(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(sampleTailoringPatchesJSON)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	result, err := a.tailorResume(context.Background(), config, "test-key", sampleTailoringBaseResume(), jobRequirements{}, nil, "en", "third")
	if err != nil {
		t.Fatalf("tailorResume: %v", err)
	}
	if len(result.Patches) != 1 || result.Patches[0].Path != "/summary" {
		t.Fatalf("expected only the summary patch accepted, got %+v", result.Patches)
	}
	if len(result.Rejected) != 1 || result.Rejected[0].Path != "/skills/hard/-" {
		t.Fatalf("expected the unconfirmed Kubernetes skill rejected, got %+v", result.Rejected)
	}
}

func TestTailorResumeAcceptsConfirmedSkill(t *testing.T) {
	bridge := newTestScraperBridge(&captureTransport{respBody: geminiJSONResponse(sampleTailoringPatchesJSON)})
	a := &api{logger: log.New(io.Discard, "", 0), scraper: bridge}

	config := defaultConfig()
	config.Form.Provider = "gemini"

	result, err := a.tailorResume(context.Background(), config, "test-key", sampleTailoringBaseResume(), jobRequirements{}, []string{"Kubernetes"}, "en", "third")
	if err != nil {
		t.Fatalf("tailorResume: %v", err)
	}
	if len(result.Patches) != 2 {
		t.Fatalf("expected both patches accepted once Kubernetes is confirmed, got %+v", result.Patches)
	}
	if len(result.Rejected) != 0 {
		t.Fatalf("expected no rejections, got %+v", result.Rejected)
	}
}

func newResumeOptimizeTestAPI(t *testing.T, respBody string) *api {
	t.Helper()
	store := newTestStore(t)
	if err := store.save(geminiTestConfig(configForm{Provider: "gemini", APIKey: "test-key"})); err != nil {
		t.Fatalf("seed config: %v", err)
	}
	bridge := newTestScraperBridge(&captureTransport{respBody: respBody})
	bridge.store = store
	return &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: bridge}
}

func TestResumeOptimizeHandlerReturnsPreviewAndRejected(t *testing.T) {
	a := newResumeOptimizeTestAPI(t, geminiJSONResponse(sampleTailoringPatchesJSON))

	body, _ := json.Marshal(optimizeRequest{Canonical: sampleTailoringBaseResume(), Requirements: jobRequirements{}})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/optimize", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeOptimize(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp optimizeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Patches) != 1 {
		t.Fatalf("expected 1 accepted patch, got %+v", resp.Patches)
	}
	if len(resp.Rejected) != 1 {
		t.Fatalf("expected 1 rejected patch, got %+v", resp.Rejected)
	}
	if resp.Preview.Summary != "Cloud-focused backend engineer." {
		t.Fatalf("expected preview to reflect the accepted patch, got %+v", resp.Preview)
	}
	// The rejected Kubernetes skill must NOT show up in the preview.
	for _, s := range resp.Preview.Skills.Hard {
		if s == "Kubernetes" {
			t.Fatal("rejected skill must not appear in the preview")
		}
	}
}

func TestResumeOptimizeHandlerRequiresAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{logger: log.New(io.Discard, "", 0), configStore: store, scraper: newTestScraperBridge(&captureTransport{})}

	body, _ := json.Marshal(optimizeRequest{Canonical: sampleTailoringBaseResume()})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/resume/optimize", strings.NewReader(string(body)))
	rec := httptest.NewRecorder()
	a.resumeOptimize(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected 409 without an AI key, got %d body=%s", rec.Code, rec.Body.String())
	}
}
