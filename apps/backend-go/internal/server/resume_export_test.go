package server

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPDFPageCount(t *testing.T) {
	// A short resume fits on one page; the page-count helper (used by the
	// one-page auto-fit) must report it accurately from the raw PDF bytes.
	pdfBytes, err := exportPDF(sampleExportResume(), resumeTemplate{ID: resumeATSStrictTemplateID}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}
	pages, err := pdfPageCount(pdfBytes)
	if err != nil {
		t.Fatalf("pdfPageCount: %v", err)
	}
	if pages != 1 {
		t.Fatalf("expected a short resume to be 1 page, got %d", pages)
	}
}

func TestExportPDFAutoFitsBorderlineResumeToOnePage(t *testing.T) {
	// A rich résumé that overflows the comfortable layout by a little (the
	// classic "one orphan line on page 2") must be compacted to a single page
	// by the auto-fit, not left with a near-empty second page.
	pdfBytes, err := exportPDF(qaSampleResume(), resumeTemplate{ID: resumeATSCleanTemplateID}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}
	pages, err := pdfPageCount(pdfBytes)
	if err != nil {
		t.Fatalf("pdfPageCount: %v", err)
	}
	if pages != 1 {
		t.Fatalf("expected the borderline résumé to auto-fit to 1 page, got %d", pages)
	}
}

func TestExportPDFKeepsAllContentWhenGenuinelyLong(t *testing.T) {
	// Auto-fit must never clip content to force one page: a genuinely long
	// résumé legitimately spans multiple pages, and every section — including
	// the last one — must still be present and selectable.
	long := qaSampleResume()
	base := long.Experience[0]
	for i := 0; i < 6; i++ {
		extra := base
		extra.Company = base.Company + " " + strings.Repeat("X", i+1)
		long.Experience = append(long.Experience, extra)
	}
	pdfBytes, err := exportPDF(long, resumeTemplate{ID: resumeATSStrictTemplateID}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}
	pages, err := pdfPageCount(pdfBytes)
	if err != nil {
		t.Fatalf("pdfPageCount: %v", err)
	}
	if pages < 2 {
		t.Fatalf("expected a genuinely long résumé to span multiple pages, got %d", pages)
	}
	path := filepath.Join(t.TempDir(), "long.pdf")
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	extracted, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extractPDFText: %v", err)
	}
	// The last section's content must survive pagination (no clipping).
	if !strings.Contains(extracted, "Spanish") {
		t.Fatalf("expected the last section (Languages) to survive pagination, got:\n%s", extracted)
	}
}

func sampleExportResume() CanonicalResume {
	return CanonicalResume{
		Basics: ResumeBasics{
			Name: "Jane Doe", Email: "jane@example.com", Phone: "+1 555 0100",
			Headline: "DevOps Engineer", Location: "Remote",
		},
		Summary: "DevOps engineer with strong AWS and Terraform background.",
		Skills:  ResumeSkills{Hard: []string{"AWS", "Terraform"}, Tools: []string{"Docker"}},
		Experience: []ResumeExperience{
			{
				Company: "Acme", Role: "Senior DevOps Engineer", Start: "2022-01", End: "present",
				Bullets: []string{"Automated AWS deployments with Terraform, reducing release time by 40%."},
			},
		},
		Education: []ResumeEducation{{Institution: "State University", Degree: "BSc Computer Science", Start: "2014", End: "2018"}},
		Licenses: []ResumeLicense{
			{Name: "Registered Nurse", Jurisdiction: "State of Illinois"},
		},
		Certifications: []ResumeCertification{
			{Name: "AWS Certified Solutions Architect", Issuer: "AWS", Year: "2023"},
		},
		Languages: []ResumeLanguage{{Language: "English", Fluency: "Native"}},
	}
}

func TestExportMarkdownContainsExpectedSections(t *testing.T) {
	md := exportMarkdown(sampleExportResume())

	for _, want := range []string{
		"# Jane Doe",
		"## SUMMARY",
		"## EXPERIENCE",
		"## EDUCATION",
		"## SKILLS",
		"## LICENSES",
		"## CERTIFICATIONS",
		"## LANGUAGES",
		"jane@example.com",
		"Automated AWS deployments",
		"Registered Nurse — State of Illinois",
		"AWS Certified Solutions Architect",
	} {
		if !strings.Contains(md, want) {
			t.Fatalf("expected markdown to contain %q, got:\n%s", want, md)
		}
	}
}

func TestExportMarkdownSkipsEmptySections(t *testing.T) {
	md := exportMarkdown(CanonicalResume{Basics: ResumeBasics{Name: "Jane Doe"}})
	for _, unwanted := range []string{"## SUMMARY", "## EXPERIENCE", "## EDUCATION", "## SKILLS", "## PROJECTS", "## LICENSES", "## CERTIFICATIONS", "## LANGUAGES"} {
		if strings.Contains(md, unwanted) {
			t.Fatalf("expected empty section %q to be skipped, got:\n%s", unwanted, md)
		}
	}
}

func TestExportHTMLEscapesUserContent(t *testing.T) {
	r := sampleExportResume()
	r.Summary = `Engineer with <script>alert('x')</script> experience`
	out := exportHTML(r, resumeTemplate{})
	if strings.Contains(out, "<script>") {
		t.Fatal("expected HTML special characters in resume content to be escaped")
	}
	if !strings.Contains(out, "&lt;script&gt;") {
		t.Fatalf("expected escaped script tag, got:\n%s", out)
	}
	if !strings.Contains(out, "<h1>Jane Doe</h1>") {
		t.Fatalf("expected semantic h1 heading, got:\n%s", out)
	}
	if !strings.Contains(out, "<h2>Licenses</h2>") || !strings.Contains(out, "State of Illinois") {
		t.Fatalf("expected canonical licenses in HTML export, got:\n%s", out)
	}
	if strings.Contains(out, "<table") || strings.Contains(out, "column") {
		t.Fatalf("expected no tables/columns in ATS-safe HTML export, got:\n%s", out)
	}
}

func TestExportHTMLEmbedsTemplateStyle(t *testing.T) {
	r := CanonicalResume{
		Basics: ResumeBasics{Name: "Jane Doe"},
		Experience: []ResumeExperience{{
			Company: "Acme",
			Role:    "Engineer",
			Start:   "2020-01",
			End:     "present",
		}},
	}
	cases := []struct {
		id         string
		wantAccent string
		wantFloat  bool
	}{
		{resumeATSStrictTemplateID, "", false},
		{resumeATSCleanTemplateID, "#1e5aa8", false},
		{resumeModernAccentTemplateID, "#6d28d9", true},
	}
	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			html := exportHTML(r, resumeTemplate{ID: tc.id})
			if !strings.Contains(html, "<style>") {
				t.Fatal("expected embedded <style> block")
			}
			if tc.wantAccent != "" && !strings.Contains(html, tc.wantAccent) {
				t.Fatalf("expected accent %s in style", tc.wantAccent)
			}
			if tc.id == resumeATSStrictTemplateID && strings.Contains(html, "#1e5aa8") {
				t.Fatal("ATS Strict must stay plain black")
			}
			if !strings.Contains(html, `class="role-line"`) || !strings.Contains(html, `class="dates"`) {
				t.Fatal("expected role-line/dates markup for WYSIWYG date alignment")
			}
			hasFloat := strings.Contains(resumeTemplateCSS(resumeTemplate{ID: tc.id}), ".dates{float:right")
			if hasFloat != tc.wantFloat {
				t.Fatalf("dates float mismatch: got %v want %v", hasFloat, tc.wantFloat)
			}
		})
	}
}

func TestExportHTMLSkipsEmptySections(t *testing.T) {
	out := exportHTML(CanonicalResume{Basics: ResumeBasics{Name: "Jane Doe"}}, resumeTemplate{})
	for _, unwanted := range []string{"<h2>Summary</h2>", "<h2>Experience</h2>", "<h2>Skills</h2>"} {
		if strings.Contains(out, unwanted) {
			t.Fatalf("expected empty section %q to be skipped, got:\n%s", unwanted, out)
		}
	}
}

func TestExportPDFProducesValidPDFBytes(t *testing.T) {
	pdfBytes, err := exportPDF(sampleExportResume(), resumeTemplate{}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}
	if len(pdfBytes) < 4 || string(pdfBytes[:4]) != "%PDF" {
		t.Fatalf("expected PDF bytes to start with %%PDF header, got %q", pdfBytes[:min(20, len(pdfBytes))])
	}
}

func TestExportPDFTextIsSelectableRoundTrip(t *testing.T) {
	pdfBytes, err := exportPDF(sampleExportResume(), resumeTemplate{}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}

	path := filepath.Join(t.TempDir(), "resume.pdf")
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatalf("write pdf to disk: %v", err)
	}

	extracted, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extractPDFText round-trip failed (PDF text not selectable): %v", err)
	}

	for _, want := range []string{"Jane Doe", "jane@example.com", "Terraform", "Registered Nurse", "State of Illinois", "AWS Certified Solutions Architect"} {
		if !strings.Contains(extracted, want) {
			t.Fatalf("expected extracted PDF text to contain %q, got:\n%s", want, extracted)
		}
	}
}

func TestParsedLocationDoesNotLeakIntoPDFTitleOrFileName(t *testing.T) {
	raw := `{"schemaVersion":2,"basics":{"name":"RAFAEL MOREIRA Belo Horizonte","headline":"Profissional de Tecnologia","location":"MG"},"experience":[{"company":"Northwind Systems","role":"DevOps","location":"Belo Horizonte, Brasil"}]}`
	canonical, _, err := decodeResumeParseResult(raw, "RAFAEL MOREIRA    Belo Horizonte")
	if err != nil {
		t.Fatalf("decodeResumeParseResult: %v", err)
	}
	pdfBytes, err := exportPDF(canonical, resumeTemplate{ID: resumeATSStrictTemplateID}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}
	path := filepath.Join(t.TempDir(), "resume.pdf")
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatalf("write PDF: %v", err)
	}
	extracted, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extractPDFText: %v", err)
	}
	if strings.Contains(extracted, "RAFAEL MOREIRA Belo Horizonte") {
		t.Fatalf("location leaked into PDF title:\n%s", extracted)
	}
	if !strings.Contains(extracted, "RAFAEL MOREIRA") {
		t.Fatalf("expected the candidate name in PDF title:\n%s", extracted)
	}
	if got := exportFileName(canonical, "pdf"); got != "rafael-moreira.pdf" {
		t.Fatalf("unexpected export filename %q", got)
	}
}

func TestExportPDFAllTemplatesTextIsSelectable(t *testing.T) {
	templates := []resumeTemplate{
		{ID: resumeATSStrictTemplateID, Name: "ATS Strict"},
		{ID: resumeATSCleanTemplateID, Name: "ATS Clean"},
		{ID: resumeModernAccentTemplateID, Name: "Modern Accent"},
	}
	for _, tmpl := range templates {
		t.Run(tmpl.Name, func(t *testing.T) {
			pdfBytes, err := exportPDF(sampleExportResume(), tmpl, "letter")
			if err != nil {
				t.Fatalf("exportPDF(%s): %v", tmpl.Name, err)
			}
			path := filepath.Join(t.TempDir(), "resume.pdf")
			if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
				t.Fatalf("write pdf: %v", err)
			}
			extracted, err := extractPDFText(path)
			if err != nil {
				t.Fatalf("extractPDFText(%s): %v", tmpl.Name, err)
			}
			for _, want := range []string{"Jane Doe", "Terraform", "AWS Certified Solutions Architect"} {
				if !strings.Contains(extracted, want) {
					t.Fatalf("%s: expected extracted text to contain %q, got:\n%s", tmpl.Name, want, extracted)
				}
			}
		})
	}
}

func TestExportPDFStructuralLayout(t *testing.T) {
	pdfBytes, err := exportPDF(sampleExportResume(), resumeTemplate{}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}
	path := filepath.Join(t.TempDir(), "resume.pdf")
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	extracted, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extractPDFText: %v", err)
	}

	// Section headings are rendered UPPERCASE (ATS layout, G.3).
	for _, heading := range []string{"EXPERIENCE", "EDUCATION", "SKILLS", "LICENSES", "CERTIFICATIONS", "LANGUAGES"} {
		if !strings.Contains(extracted, heading) {
			t.Fatalf("expected UPPERCASE section heading %q in PDF, got:\n%s", heading, extracted)
		}
	}
	// Bullets render as "•", not "- ".
	if !strings.Contains(extracted, "•") {
		t.Fatalf("expected bullet character • in PDF text, got:\n%s", extracted)
	}
	// Skills are grouped into ATS categories (AWS→Cloud, Terraform→IaC & CI/CD).
	if !strings.Contains(extracted, "Cloud:") {
		t.Fatalf("expected grouped skills label %q in PDF, got:\n%s", "Cloud:", extracted)
	}
}

func TestGroupSkillsCategorizesWithoutInventing(t *testing.T) {
	in := ResumeSkills{
		Hard:  []string{"AWS", "Terraform", "Grafana", "Kubernetes", "Cobol"},
		Tools: []string{"Docker"},
		Soft:  []string{"Leadership"},
	}
	groups, grouped := groupSkills(in)
	if !grouped {
		t.Fatal("expected grouped=true when known skills are present")
	}

	// Every token — and only those tokens — must appear exactly once across
	// all groups (anti-invention: no skill added, none dropped).
	want := map[string]int{}
	for _, s := range append(append(append([]string{}, in.Hard...), in.Tools...), in.Soft...) {
		want[s]++
	}
	got := map[string]int{}
	byLabel := map[string][]string{}
	for _, g := range groups {
		byLabel[g.label] = g.items
		for _, item := range g.items {
			got[item]++
		}
	}
	if len(got) != len(want) {
		t.Fatalf("skill count changed: got %v want %v", got, want)
	}
	for k, v := range want {
		if got[k] != v {
			t.Fatalf("token %q count mismatch: got %d want %d", k, got[k], v)
		}
	}

	// Spot-check categorization and that the unknown token falls into Additional.
	assertIn := func(label, item string) {
		for _, it := range byLabel[label] {
			if it == item {
				return
			}
		}
		t.Fatalf("expected %q under %q, groups=%v", item, label, byLabel)
	}
	assertIn("Cloud", "AWS")
	// Not "IaC & CI/CD": the compound label used to assert a CI/CD competency
	// that a Terraform-only resume never evidenced (UX-007).
	assertIn("Infrastructure as Code", "Terraform")
	assertIn("Observability", "Grafana")
	assertIn("Containers", "Kubernetes")
	assertIn("Containers", "Docker")
	assertIn("Additional", "Cobol")
	assertIn("Soft Skills", "Leadership")
}

func TestGroupSkillsFallsBackWhenUncategorized(t *testing.T) {
	_, grouped := groupSkills(ResumeSkills{Hard: []string{"Cobol", "Fortran"}})
	if grouped {
		t.Fatal("expected grouped=false when no skill matches a category (flat fallback)")
	}
}

func TestExportPDFSupportsA4PageSize(t *testing.T) {
	pdfBytes, err := exportPDF(sampleExportResume(), resumeTemplate{}, "a4")
	if err != nil {
		t.Fatalf("exportPDF (a4): %v", err)
	}
	if len(pdfBytes) < 4 || string(pdfBytes[:4]) != "%PDF" {
		t.Fatal("expected valid PDF bytes for a4 page size")
	}
}

func TestResumeExportHandlerMarkdown(t *testing.T) {
	store := newTestStore(t)
	a := &api{configStore: store}

	body, _ := json.Marshal(exportRequest{Canonical: sampleExportResume(), Format: "md"})
	req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeExport(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp exportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Format != "md" || !strings.Contains(resp.Content, "# Jane Doe") {
		t.Fatalf("unexpected markdown export response: %+v", resp)
	}
	if !strings.HasSuffix(resp.FileName, ".md") {
		t.Fatalf("expected .md filename, got %q", resp.FileName)
	}
}

func TestResumeExportHandlerPDFWorksWithoutAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{configStore: store}

	body, _ := json.Marshal(exportRequest{Canonical: sampleExportResume(), Format: "pdf"})
	req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeExport(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 (export works without AI key), got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp exportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Format != "pdf" || resp.Content == "" {
		t.Fatalf("unexpected pdf export response: %+v", resp)
	}
}

func TestResumeExportHandlerDOCXWorksWithoutAIKey(t *testing.T) {
	store := newTestStore(t)
	a := &api{configStore: store}

	body, _ := json.Marshal(exportRequest{Canonical: sampleExportResume(), Format: "docx", TemplateID: resumeATSCleanTemplateID})
	req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeExport(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200 (export works without AI key), got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp exportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.Format != "docx" || resp.Content == "" {
		t.Fatalf("unexpected docx export response: %+v", resp)
	}
	if !strings.HasSuffix(resp.FileName, ".docx") {
		t.Fatalf("expected .docx filename, got %q", resp.FileName)
	}
	data, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		t.Fatalf("expected base64 docx content: %v", err)
	}
	text, err := extractDOCXText(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("generated DOCX is not readable: %v", err)
	}
	if !strings.Contains(text, "Jane Doe") || !strings.Contains(text, "Terraform") {
		t.Fatalf("expected exported DOCX text to contain resume content, got %s", text)
	}
}

// TestResumeExportHandlerProducesAutoFitSelectablePDF proves the real HTTP
// export path (the exact endpoint the app's frontend calls) produces the same
// polished, single-page, text-selectable PDF the visual QA renders — no
// separate code path, no mockup.
func TestResumeExportHandlerProducesAutoFitSelectablePDF(t *testing.T) {
	store := newTestStore(t)
	a := &api{configStore: store}

	body, _ := json.Marshal(exportRequest{Canonical: qaSampleResume(), Format: "pdf", TemplateID: resumeATSCleanTemplateID, PageSize: "letter"})
	req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeExport(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var resp exportResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	pdfBytes, err := base64.StdEncoding.DecodeString(resp.Content)
	if err != nil {
		t.Fatalf("expected base64 pdf content: %v", err)
	}
	if !bytes.HasPrefix(pdfBytes, []byte("%PDF")) {
		t.Fatalf("expected a real PDF, got prefix %q", pdfBytes[:min(8, len(pdfBytes))])
	}
	// Auto-fit applied through the handler: the rich résumé lands on one page.
	pages, err := pdfPageCount(pdfBytes)
	if err != nil {
		t.Fatalf("pdfPageCount: %v", err)
	}
	if pages != 1 {
		t.Fatalf("expected the handler's PDF to be auto-fit to 1 page, got %d", pages)
	}
	// Text is selectable/extractable (ATS-safe) straight from the endpoint output.
	path := filepath.Join(t.TempDir(), "handler.pdf")
	if err := os.WriteFile(path, pdfBytes, 0o600); err != nil {
		t.Fatalf("write pdf: %v", err)
	}
	extracted, err := extractPDFText(path)
	if err != nil {
		t.Fatalf("extractPDFText: %v", err)
	}
	for _, want := range []string{"Alex Rivera", "Terraform", "Kubernetes", "Spanish"} {
		if !strings.Contains(extracted, want) {
			t.Fatalf("expected the handler's PDF text to contain %q, got:\n%s", want, extracted)
		}
	}
}

// TestResumeExportHandlerExportsHeuristicCanonicalWithoutAIKey covers the
// no-AI-key product flow (QA-002): upload -> offline diagnose builds a
// heuristic canonical (buildHeuristicCanonical, no Basics.Name, sparse
// structure) -> that same canonical must still export as a valid PDF/DOCX
// without ever calling the AI-gated /resume/parse route.
func TestResumeExportHandlerExportsHeuristicCanonicalWithoutAIKey(t *testing.T) {
	store := newTestStore(t) // no AI key configured at all
	a := &api{configStore: store}
	heuristic := buildHeuristicCanonical(sampleRawResumeText)
	if heuristic.Basics.Name != "" {
		t.Fatalf("expected the heuristic canonical fixture to have no name, got %q", heuristic.Basics.Name)
	}

	for _, format := range []string{"pdf", "docx"} {
		body, _ := json.Marshal(exportRequest{Canonical: heuristic, Format: format})
		req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
		rec := httptest.NewRecorder()
		a.resumeExport(rec, req)

		if rec.Code != 200 {
			t.Fatalf("format=%s: expected 200 exporting a heuristic canonical without AI key, got %d body=%s", format, rec.Code, rec.Body.String())
		}
		var resp exportResponse
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("format=%s: %v", format, err)
		}
		if resp.Content == "" {
			t.Fatalf("format=%s: expected non-empty exported content", format)
		}
	}
}

func TestResumeExportHandlerRejectsInvalidFormat(t *testing.T) {
	store := newTestStore(t)
	a := &api{configStore: store}

	body, _ := json.Marshal(exportRequest{Canonical: sampleExportResume(), Format: "xlsx"})
	req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeExport(rec, req)

	if rec.Code != 400 {
		t.Fatalf("expected 400 for unsupported format, got %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestSeededATSStrictTemplateUsedByExportHandler(t *testing.T) {
	store := newTestStore(t)
	if err := store.seedResumeTemplates(); err != nil {
		t.Fatalf("seedResumeTemplates: %v", err)
	}
	a := &api{configStore: store}

	body, _ := json.Marshal(exportRequest{Canonical: sampleExportResume(), Format: "html", TemplateID: resumeATSStrictTemplateID})
	req := httptest.NewRequest("POST", "/api/v1/resume/export", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	a.resumeExport(rec, req)

	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d body=%s", rec.Code, rec.Body.String())
	}
}
