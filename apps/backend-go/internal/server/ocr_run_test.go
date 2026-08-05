package server

import (
	"context"
	"encoding/base64"
	"io"
	"log"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeMockOCRExe(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestRunOCRPDFUsesRendererAndTesseractMocks(t *testing.T) {
	dir := t.TempDir()
	pdftoppm := filepath.Join(dir, "pdftoppm.exe")
	tesseract := filepath.Join(dir, "tesseract.exe")
	writeMockOCRExe(t, pdftoppm, "mock renderer")
	writeMockOCRExe(t, tesseract, "mock tesseract")
	oldRunner := runOCRCommandFunc
	t.Cleanup(func() { runOCRCommandFunc = oldRunner })
	runOCRCommandFunc = func(_ context.Context, name string, args ...string) (string, error) {
		switch name {
		case pdftoppm:
			prefix := args[len(args)-1]
			return "", os.WriteFile(prefix+"-1.png", []byte("png"), 0o600)
		case tesseract:
			return "Jane Doe\nSenior Go Engineer\n", nil
		default:
			t.Fatalf("unexpected OCR command: %s %v", name, args)
			return "", nil
		}
	}

	result, err := runOCRPDF(context.Background(), []byte("%PDF fake enough for mock renderer"), ocrCommandPaths{
		PDFToPPM:   pdftoppm,
		Tesseract:  tesseract,
		WorkingDir: filepath.Join(dir, "work"),
	})
	if err != nil {
		t.Fatalf("runOCRPDF: %v", err)
	}
	if result.Pages != 1 {
		t.Fatalf("expected 1 page, got %d", result.Pages)
	}
	if !strings.Contains(result.Text, "Jane Doe") || !strings.Contains(result.Text, "Senior Go Engineer") {
		t.Fatalf("unexpected OCR text: %q", result.Text)
	}
}

func TestRunOCRPDFRequiresInstalledBinaries(t *testing.T) {
	_, err := runOCRPDF(context.Background(), []byte("%PDF"), ocrCommandPaths{
		PDFToPPM:  filepath.Join(t.TempDir(), "missing-pdftoppm.exe"),
		Tesseract: filepath.Join(t.TempDir(), "missing-tesseract.exe"),
	})
	if err == nil || !strings.Contains(err.Error(), "ocr_not_installed") {
		t.Fatalf("expected ocr_not_installed, got %v", err)
	}
}

func TestNormalizeOCRTextCleansPageBreakNoise(t *testing.T) {
	got := normalizeOCRText(" Jane Doe \r\n\r\n\f Senior Go Engineer \n")
	want := "Jane Doe\nSenior Go Engineer"
	if got != want {
		t.Fatalf("normalizeOCRText() = %q, want %q", got, want)
	}
}

func TestOCRRunHandlerRejectsWhenOCRNotInstalled(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	a := &api{logger: log.New(io.Discard, "", 0)}
	payload := `{"fileName":"scan.pdf","mimeType":"application/pdf","contentBase64":"` + base64.StdEncoding.EncodeToString([]byte("%PDF")) + `"}`

	req := httptest.NewRequest("POST", "/api/v1/ocr/run", strings.NewReader(payload))
	rec := httptest.NewRecorder()
	a.ocrRun(rec, req)

	if rec.Code != 409 {
		t.Fatalf("expected 409, got %d body=%s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "ocr_not_installed") {
		t.Fatalf("expected typed ocr_not_installed response, got %s", rec.Body.String())
	}
}
