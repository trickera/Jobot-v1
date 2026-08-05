package server

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	ocrRunTimeout  = 3 * time.Minute
	ocrMaxPDFPages = 10
)

var (
	errOCRNotInstalled = errors.New("ocr_not_installed")
	errOCRNoText       = errors.New("ocr_no_text")
)

type ocrRunRequest struct {
	FileName      string `json:"fileName"`
	MimeType      string `json:"mimeType"`
	ContentBase64 string `json:"contentBase64"`
}

type ocrRunResponse struct {
	Text     string   `json:"text"`
	Pages    int      `json:"pages"`
	Warnings []string `json:"warnings,omitempty"`
}

type ocrCommandPaths struct {
	PDFToPPM   string
	Tesseract  string
	WorkingDir string
}

func defaultOCRCommandPaths() ocrCommandPaths {
	return ocrCommandPaths{
		PDFToPPM:  ocrPdftoppmPath(),
		Tesseract: ocrTesseractExePath(),
	}
}

func (p ocrCommandPaths) validateInstalled() error {
	if !fileExists(p.PDFToPPM) || !fileExists(p.Tesseract) {
		return errOCRNotInstalled
	}
	return nil
}

func (a *api) ocrRun(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()

	var payload ocrRunRequest
	if err := jsonDecodeLimited(r.Body, &payload, maxResumeUploadBytes*2); err != nil {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_request", "Invalid OCR request body."))
		return
	}
	name := sanitizeResumeName(payload.FileName)
	if strings.ToLower(filepath.Ext(name)) != ".pdf" {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "unsupported_format", "OCR currently supports scanned PDF files only."))
		return
	}
	content, err := base64.StdEncoding.DecodeString(payload.ContentBase64)
	if err != nil || len(content) == 0 {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "invalid_file", "Invalid or empty PDF file."))
		return
	}
	if len(content) > maxResumeUploadBytes {
		writeResumeError(w, resumeErrorFor(http.StatusBadRequest, "file_too_large", "The resume file exceeds 8 MB."))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), ocrRunTimeout)
	defer cancel()
	result, err := runOCRPDF(ctx, content, defaultOCRCommandPaths())
	if err != nil {
		a.log("error", "ocr run: %v", err)
		switch {
		case errors.Is(err, errOCRNotInstalled):
			writeResumeError(w, resumeErrorFor(http.StatusConflict, "ocr_not_installed", "OCR is not installed yet. Set up OCR before running it."))
		case errors.Is(err, errOCRNoText):
			writeResumeError(w, resumeErrorFor(http.StatusUnprocessableEntity, "ocr_no_text", "OCR ran, but no readable text was found. Try another scan or paste the text manually."))
		case errors.Is(err, context.DeadlineExceeded):
			writeResumeError(w, resumeErrorFor(http.StatusGatewayTimeout, "ocr_timeout", "OCR took too long. Try a shorter PDF or paste the text manually."))
		default:
			writeResumeError(w, resumeErrorFor(http.StatusBadGateway, "ocr_failed", "OCR failed. Check the app logs or paste the text manually."))
		}
		return
	}
	a.log("success", "[ OCR ] extracted %d chars from %d page(s)", len(result.Text), result.Pages)
	writeJSON(w, http.StatusOK, result)
}

var runOCRCommandFunc = runOCRCommand

func runOCRPDF(ctx context.Context, pdfBytes []byte, paths ocrCommandPaths) (ocrRunResponse, error) {
	if err := paths.validateInstalled(); err != nil {
		return ocrRunResponse{}, err
	}
	workDir := paths.WorkingDir
	cleanup := func() {}
	if strings.TrimSpace(workDir) == "" {
		dir, err := os.MkdirTemp("", "sencia-ocr-run-*")
		if err != nil {
			return ocrRunResponse{}, err
		}
		workDir = dir
		cleanup = func() { os.RemoveAll(dir) }
	}
	defer cleanup()

	if err := os.MkdirAll(workDir, 0o700); err != nil {
		return ocrRunResponse{}, err
	}
	inputPath := filepath.Join(workDir, "input.pdf")
	if err := os.WriteFile(inputPath, pdfBytes, 0o600); err != nil {
		return ocrRunResponse{}, err
	}

	prefix := filepath.Join(workDir, "page")
	renderArgs := []string{"-r", "300", "-png", "-f", "1", "-l", fmt.Sprintf("%d", ocrMaxPDFPages), inputPath, prefix}
	if output, err := runOCRCommandFunc(ctx, paths.PDFToPPM, renderArgs...); err != nil {
		return ocrRunResponse{}, fmt.Errorf("pdftoppm: %w: %s", err, truncate(strings.TrimSpace(output), 600))
	}

	pageImages, err := filepath.Glob(prefix + "-*.png")
	if err != nil {
		return ocrRunResponse{}, err
	}
	sort.Strings(pageImages)
	if len(pageImages) == 0 {
		return ocrRunResponse{}, fmt.Errorf("%w: pdftoppm rendered no pages", errOCRNoText)
	}
	if len(pageImages) > ocrMaxPDFPages {
		pageImages = pageImages[:ocrMaxPDFPages]
	}

	var builder strings.Builder
	for index, imagePath := range pageImages {
		output, err := runOCRCommandFunc(ctx, paths.Tesseract, imagePath, "stdout", "-l", "eng", "--psm", "6")
		if err != nil {
			return ocrRunResponse{}, fmt.Errorf("tesseract page %d: %w: %s", index+1, err, truncate(strings.TrimSpace(output), 600))
		}
		text := normalizeOCRText(output)
		if text == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(text)
	}

	text := strings.TrimSpace(builder.String())
	if text == "" {
		return ocrRunResponse{}, errOCRNoText
	}
	resp := ocrRunResponse{Text: truncate(text, 24000), Pages: len(pageImages)}
	if len(pageImages) == ocrMaxPDFPages {
		resp.Warnings = append(resp.Warnings, "ocr_page_limit_reached")
	}
	return resp, nil
}

func runOCRCommand(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	hideWorkerConsole(cmd)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func normalizeOCRText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	value = strings.ReplaceAll(value, "\f", "\n")
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		line = cleanText(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return strings.Join(lines, "\n")
}
