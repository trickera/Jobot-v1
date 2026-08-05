package server

import (
	"encoding/json"
	"fmt"
	"strings"
)

// --- Diagnóstico (offline) ---
type atsScores struct {
	Readability int `json:"readability"`
	Content     int `json:"content"`
	Impact      int `json:"impact"`
	Keywords    int `json:"keywords"`
	// ImpactMeasured is false when the resume has no bullets to judge — which is
	// every resume before it has been parsed, since the offline canonical built
	// from raw text has no structured experience yet.
	//
	// Impact is 0 in that case, and 0 is a real score on this scale: the panel drew
	// a red bar at zero and told a user who had just uploaded their resume that it
	// had no impact, when in truth nothing had looked at it. The code already knew
	// the difference — it suppresses the "few bullets with impact metrics" warning
	// in exactly this case — and threw the knowledge away by returning a number.
	// Measured on a real upload: Impact reads 0 before parsing and 38 after, on the
	// same document.
	ImpactMeasured bool `json:"impactMeasured"`
}
type atsIssue struct {
	Code     string `json:"code"`
	Severity string `json:"severity"` // "low" | "medium" | "high"
	Message  string `json:"message"`
}

// --- Vaga ---
type jobRequirements struct {
	Category         string   `json:"category"`
	JobTitle         string   `json:"jobTitle"`
	HardRequirements []string `json:"hardRequirements"`
	NiceToHave       []string `json:"niceToHave"`
	Seniority        string   `json:"seniority"`
	ATSKeywords      []string `json:"atsKeywords"`
}

// --- Gap ---
type gapEvidence string

func (e *gapEvidence) UnmarshalJSON(data []byte) error {
	if string(data) == "null" {
		*e = ""
		return nil
	}
	var scalar string
	if err := json.Unmarshal(data, &scalar); err == nil {
		var embedded []string
		if json.Unmarshal([]byte(strings.TrimSpace(scalar)), &embedded) == nil {
			*e = gapEvidence(flattenGapEvidence(embedded))
		} else {
			*e = gapEvidence(strings.TrimSpace(scalar))
		}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err == nil {
		*e = gapEvidence(flattenGapEvidence(list))
		return nil
	}
	return fmt.Errorf("gap evidence must be a string or string array")
}

func flattenGapEvidence(values []string) string {
	clean := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			clean = append(clean, value)
		}
	}
	return strings.Join(clean, " · ")
}

type gapItem struct {
	Term     string      `json:"term"`
	Evidence gapEvidence `json:"evidence"`
}
type gapResult struct {
	Found     []gapItem `json:"found"`
	Partial   []gapItem `json:"partial"`
	Missing   []gapItem `json:"missing"`
	ToConfirm []gapItem `json:"toConfirm"`
}

// --- Diff / tailoring ---
type jsonPatchOp struct {
	Op         string          `json:"op"` // "replace" | "add" | "remove"
	Path       string          `json:"path"`
	Value      json.RawMessage `json:"value,omitempty"`
	Reason     string          `json:"reason"`
	ReviewRisk string          `json:"reviewRisk,omitempty"` // new_metric | new_skill | new_certification | identity_change | unsupported_claim | high_rewrite_distance
}

// tailorResult is not literally listed in Appendix B.2 (the plan's Task 10
// interface note only writes `tailorResult{ Patches []jsonPatchOp }`), but
// the /resume/optimize handler also needs the operations the deterministic
// anti-invention gate rejected (for optimizeResponse.Rejected), so Rejected
// was added here alongside Patches.
type tailorResult struct {
	Patches  []jsonPatchOp `json:"patches"`
	Rejected []jsonPatchOp `json:"rejected"`
}

// --- Score ---
type scoreBreakdown map[string]int
type scoreResult struct {
	ATS          int            `json:"ats"`
	HR           int            `json:"hr"`
	ATSBreakdown scoreBreakdown `json:"atsBreakdown"`
	HRBreakdown  scoreBreakdown `json:"hrBreakdown"`
}

// --- Persistência (espelham colunas do Apêndice A) ---
type resumeTemplate struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Category string `json:"category"`
	Engine   string `json:"engine"`
	IsATS    bool   `json:"isAts"`
}
type resumeVersion struct {
	ID         string          `json:"id"`
	Name       string          `json:"name,omitempty"`
	DocumentID string          `json:"documentId"`
	JobID      string          `json:"jobId"`
	Canonical  CanonicalResume `json:"canonical"`
	Patches    []jsonPatchOp   `json:"patches"`
	TemplateID string          `json:"templateId"`
	ATSScore   int             `json:"atsScore"`
	HRScore    int             `json:"hrScore"`
	CreatedAt  string          `json:"createdAt"`
}

// docMeta is resume_documents metadata without the (large) canonical JSON
// payload, used for list views.
type docMeta struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	SourceFile   string `json:"sourceFile"`
	SourceFormat string `json:"sourceFormat"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

// resumeAnalysis mirrors resume_analyses rows (diagnosis persisted for a
// document or a version).
type resumeAnalysis struct {
	ID          string     `json:"id"`
	SubjectKind string     `json:"subjectKind"` // "document" | "version"
	SubjectID   string     `json:"subjectId"`
	Scores      atsScores  `json:"scores"`
	Issues      []atsIssue `json:"issues"`
	CreatedAt   string     `json:"createdAt"`
}

// jobResumeMatch mirrors job_resume_matches rows (links a job to a
// generated resume version + its gap analysis + application status).
type jobResumeMatch struct {
	ID        string    `json:"id"`
	JobID     string    `json:"jobId"`
	VersionID string    `json:"versionId"`
	Gap       gapResult `json:"gap"`
	Status    string    `json:"status"` // "gerado" | "aplicado" | "dispensado"
	CreatedAt string    `json:"createdAt"`
	UpdatedAt string    `json:"updatedAt"`
}

// --- Requests/Responses das rotas ---
type resumeParseRequest struct {
	Text string `json:"text"`
}
type resumeParseResponse struct {
	DocumentID   string          `json:"documentId"`
	Canonical    CanonicalResume `json:"canonical"`
	Warnings     []string        `json:"warnings,omitempty"`
	ProviderUsed string          `json:"providerUsed,omitempty"`
}
type diagnoseRequest struct {
	Canonical CanonicalResume `json:"canonical"`
	RawText   string          `json:"rawText,omitempty"`
	// DocumentID is optional and not in Appendix B.2's literal listing: when
	// present, the diagnosis is persisted to resume_analyses (subject_kind
	// "document") as the plan's Task 7 acceptance criteria requires; the
	// spec's diagnoseRequest has no id to key that persistence on otherwise.
	DocumentID string `json:"documentId,omitempty"`
}
type diagnoseResponse struct {
	Scores atsScores  `json:"scores"`
	Issues []atsIssue `json:"issues"`
	// Canonical echoes back the structure that was actually scored - either
	// the caller-supplied canonical, or (when Heuristic is true) the
	// best-effort structure buildHeuristicCanonical derived from RawText.
	// This lets the frontend offer export of a no-AI-key resume without
	// requiring the AI-gated /resume/parse route first.
	Canonical CanonicalResume `json:"canonical"`
	Heuristic bool            `json:"heuristic,omitempty"`
}
type analyzeJobRequest struct {
	JobID       string `json:"jobId,omitempty"`
	Description string `json:"description,omitempty"`
	Category    string `json:"category,omitempty"`
	Seniority   string `json:"seniority,omitempty"`
}
type analyzeJobResponse struct {
	Requirements jobRequirements `json:"requirements"`
	ProviderUsed string          `json:"providerUsed,omitempty"`
}
type gapRequest struct {
	Canonical    CanonicalResume `json:"canonical"`
	Requirements jobRequirements `json:"requirements"`
}
type gapResponse struct {
	Gap          gapResult `json:"gap"`
	ProviderUsed string    `json:"providerUsed,omitempty"`
}
type optimizeRequest struct {
	Canonical    CanonicalResume `json:"canonical"`
	Requirements jobRequirements `json:"requirements"`
	Confirmed    []string        `json:"confirmed"`
	Language     string          `json:"language,omitempty"` // "en"(default)|"pt"|"es"|"auto"
	Voice        string          `json:"voice,omitempty"`    // "first"|"third"(default)
}
type optimizeResponse struct {
	Patches      []jsonPatchOp   `json:"patches"`
	Preview      CanonicalResume `json:"preview"`
	Rejected     []jsonPatchOp   `json:"rejected"`
	ProviderUsed string          `json:"providerUsed,omitempty"`
}
type scoreReq struct {
	Canonical    CanonicalResume `json:"canonical"`
	Requirements jobRequirements `json:"requirements"`
	RawText      string          `json:"rawText,omitempty"`
}
type exportRequest struct {
	Canonical  CanonicalResume `json:"canonical"`
	Format     string          `json:"format"`
	TemplateID string          `json:"templateId"`
	PageSize   string          `json:"pageSize,omitempty"` // "letter"(default)|"a4"
}
type exportResponse struct {
	Format   string `json:"format"`
	Content  string `json:"content"` // PDF: content em base64
	FileName string `json:"fileName"`
}
type resumeTemplatesResponse struct {
	Templates []resumeTemplate `json:"templates"`
}
type saveVersionResponse struct {
	ID string `json:"id"`
}
type saveVersionRequest struct {
	DocumentID string          `json:"documentId"`
	JobID      string          `json:"jobId"`
	Canonical  CanonicalResume `json:"canonical"`
	Patches    []jsonPatchOp   `json:"patches"`
	TemplateID string          `json:"templateId"`
	ATSScore   int             `json:"atsScore"`
	HRScore    int             `json:"hrScore"`
	Gap        gapResult       `json:"gap"`
}
type versionsResponse struct {
	Versions []resumeVersion `json:"versions"`
}
type renameVersionRequest struct {
	Name string `json:"name"`
}
