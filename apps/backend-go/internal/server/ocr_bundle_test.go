package server

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestOCRInstallDirUsesAppData(t *testing.T) {
	t.Setenv("APPDATA", `C:\fake\appdata`)
	got := ocrInstallDir()
	want := filepath.Join(`C:\fake\appdata`, "Sencia Job", "ocr")
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestOCRStatusReportsNotInstalledInFreshDir(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	a := &api{}

	tesseractOK := fileExists(ocrTesseractExePath())
	pdftoppmOK := fileExists(ocrPdftoppmPath())
	if tesseractOK || pdftoppmOK {
		t.Fatalf("expected neither binary to exist in a fresh temp dir")
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/v1/ocr/status", nil)
	a.ocrStatus(rec, req)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
}

func makeZipFixture(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.zip")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	zw := zip.NewWriter(f)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestExtractOCRZipHonorsBinarySubpath(t *testing.T) {
	zipPath := makeZipFixture(t, map[string]string{
		"poppler-1.0/Library/bin/pdftoppm.exe": "fake pdftoppm binary",
		"poppler-1.0/Library/bin/pdftoppm.dll": "fake dll",
		"poppler-1.0/share/README.txt":         "not a binary, should be skipped",
	})
	destDir := t.TempDir()

	if err := extractOCRZip(zipPath, "poppler-1.0/Library/bin", destDir); err != nil {
		t.Fatalf("extractOCRZip: %v", err)
	}

	exePath := filepath.Join(destDir, "pdftoppm.exe")
	content, err := os.ReadFile(exePath)
	if err != nil {
		t.Fatalf("expected pdftoppm.exe extracted at %s: %v", exePath, err)
	}
	if string(content) != "fake pdftoppm binary" {
		t.Fatalf("unexpected content: %q", content)
	}
	if _, err := os.Stat(filepath.Join(destDir, "README.txt")); err == nil {
		t.Fatal("expected files outside the binary subpath to be skipped")
	}
}

func TestExtractOCRZipRejectsZipSlip(t *testing.T) {
	zipPath := makeZipFixture(t, map[string]string{
		"../../evil.exe": "malicious payload",
	})
	destDir := t.TempDir()

	err := extractOCRZip(zipPath, "", destDir)
	if err == nil {
		t.Fatal("expected an error for a zip-slip path traversal entry")
	}
}

func TestExtractOCRZipFailsClearlyWhenSubpathMissing(t *testing.T) {
	zipPath := makeZipFixture(t, map[string]string{"some/other/path/file.txt": "x"})
	destDir := t.TempDir()

	err := extractOCRZip(zipPath, "does-not-exist/bin", destDir)
	if err == nil {
		t.Fatal("expected an error when the binary subpath matches nothing")
	}
}

func TestDownloadAndVerifyFailsClearlyOnChecksumMismatch(t *testing.T) {
	body := []byte("this is definitely not the real tesseract installer")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer server.Close()

	wrongHash := hex.EncodeToString(sha256.New().Sum(nil)) // sha256("") - guaranteed not to match

	path, err := downloadAndVerify(context.Background(), server.URL, wrongHash)
	if err == nil {
		os.Remove(path)
		t.Fatal("expected a checksum mismatch error")
	}
	if _, statErr := os.Stat(path); statErr == nil {
		t.Fatal("expected the partial/mismatched download to be deleted, not left on disk")
	}
}

func TestDownloadAndVerifySucceedsWithMatchingChecksum(t *testing.T) {
	body := []byte("known content for a deterministic hash")
	sum := sha256.Sum256(body)
	expected := hex.EncodeToString(sum[:])

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(body)
	}))
	defer server.Close()

	path, err := downloadAndVerify(context.Background(), server.URL, expected)
	if err != nil {
		t.Fatalf("downloadAndVerify: %v", err)
	}
	defer os.Remove(path)

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, body) {
		t.Fatalf("expected downloaded bytes to match, got %q", got)
	}
}

func TestDownloadAndVerifyStreamingLimitBoundaries(t *testing.T) {
	const limit int64 = 8
	for _, size := range []int{int(limit - 1), int(limit), int(limit + 1)} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			body := bytes.Repeat([]byte("x"), size)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				_, _ = w.Write(body)
			}))
			defer server.Close()

			sum := sha256.Sum256(body)
			path, err := downloadAndVerifyLimited(context.Background(), server.URL, hex.EncodeToString(sum[:]), limit)
			if int64(size) <= limit {
				if err != nil {
					t.Fatalf("download of %d bytes should succeed: %v", size, err)
				}
				defer os.Remove(path)
				got, readErr := os.ReadFile(path)
				if readErr != nil {
					t.Fatal(readErr)
				}
				if !bytes.Equal(got, body) {
					t.Fatalf("downloaded bytes differ: got %q want %q", got, body)
				}
				return
			}

			var tooLarge *externalResponseTooLargeError
			if err == nil || !errors.As(err, &tooLarge) {
				t.Fatalf("expected typed streaming over-limit error, got %v", err)
			}
			if path != "" {
				t.Fatalf("oversized download returned a path to partial data: %s", path)
			}
		})
	}
}

func TestDownloadAndVerifyRejectsContentLengthBeforeStreaming(t *testing.T) {
	const limit int64 = 8
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", strconv.FormatInt(limit+1, 10))
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var tooLarge *externalResponseTooLargeError
	path, err := downloadAndVerifyLimited(context.Background(), server.URL, "", limit)
	if err == nil || !errors.As(err, &tooLarge) {
		t.Fatalf("expected typed Content-Length over-limit error, got %v", err)
	}
	if path != "" {
		t.Fatalf("Content-Length rejection returned a path: %s", path)
	}
}

func TestOCRDownloadLimitsHaveManifestMargin(t *testing.T) {
	if ocrTesseractComponent.MaxDownloadBytes <= 50175248 {
		t.Fatalf("Tesseract limit %d must exceed the observed 50,175,248-byte artifact", ocrTesseractComponent.MaxDownloadBytes)
	}
	if ocrPopplerComponent.MaxDownloadBytes <= 16107283 {
		t.Fatalf("Poppler limit %d must exceed the observed 16,107,283-byte artifact", ocrPopplerComponent.MaxDownloadBytes)
	}
}

func TestOCRInstallHandlerRefusesConcurrentInstalls(t *testing.T) {
	a := &api{logger: log.New(io.Discard, "", 0)}
	a.ocrInstallState.tryStart()

	req := httptest.NewRequest("POST", "/api/v1/ocr/install", nil)
	rec := httptest.NewRecorder()
	a.ocrInstall(rec, req)

	if rec.Code != 409 {
		t.Fatalf("expected 409 while an install is already running, got %d body=%s", rec.Code, rec.Body.String())
	}
}
