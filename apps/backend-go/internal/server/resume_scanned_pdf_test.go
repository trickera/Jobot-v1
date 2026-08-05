package server

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestScannedPDFSuspectedHeuristic(t *testing.T) {
	cases := []struct {
		name string
		ext  string
		text string
		want bool
	}{
		{"non-pdf never flagged", ".docx", "", false},
		{"empty pdf text", ".pdf", "", true},
		{"pdf under char threshold", ".pdf", "Jane Doe\nSao Paulo", true},
		{"pdf over char threshold but under word threshold", ".pdf", strings.Repeat("supercalifragilisticexpialidocious ", 10), true},
		{"pdf with plenty of real text", ".pdf", strings.Repeat("Built and operated production systems using Go, AWS, Terraform and Kubernetes. ", 5), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scannedPDFSuspected(tc.ext, tc.text); got != tc.want {
				t.Fatalf("scannedPDFSuspected(%q, %q) = %v, want %v", tc.ext, tc.text, got, tc.want)
			}
		})
	}
}

func TestSaveResumeFlagsScannedPDFSuspected(t *testing.T) {
	store := newTestStore(t)
	sparse := CanonicalResume{Basics: ResumeBasics{Name: "A B"}}
	pdfBytes, err := exportPDF(sparse, resumeTemplate{}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}

	result, err := store.saveResume(resumeUploadRequest{
		FileName:      "resume.pdf",
		MimeType:      "application/pdf",
		ContentBase64: base64.StdEncoding.EncodeToString(pdfBytes),
	})
	if err != nil {
		t.Fatalf("saveResume: %v", err)
	}

	found := false
	for _, w := range result.Warnings {
		if w == "scanned_pdf_suspected" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected scanned_pdf_suspected warning for a near-empty PDF, got warnings=%+v extracted=%q", result.Warnings, result.ExtractedText)
	}
}

func TestSaveResumeDoesNotFlagResumePDFWithRealContent(t *testing.T) {
	store := newTestStore(t)
	pdfBytes, err := exportPDF(sampleExportResume(), resumeTemplate{}, "letter")
	if err != nil {
		t.Fatalf("exportPDF: %v", err)
	}

	result, err := store.saveResume(resumeUploadRequest{
		FileName:      "resume.pdf",
		MimeType:      "application/pdf",
		ContentBase64: base64.StdEncoding.EncodeToString(pdfBytes),
	})
	if err != nil {
		t.Fatalf("saveResume: %v", err)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("expected no warnings for a resume PDF with real content, got %+v", result.Warnings)
	}
}
