package server

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// OCR component manifest (CH-09/D10). Pinned by version + SHA-256 so every
// install downloads the exact same, verified bytes - never trust an
// unpinned "latest" URL for something that gets executed (the Tesseract
// installer) or loaded. Mirrored in scripts/ocr-manifest.json for humans;
// this is the copy the binary actually uses at runtime. Update both
// together when bumping versions. Neither binary is committed to the repo.
type ocrComponent struct {
	Name             string
	Version          string
	URL              string
	SHA256           string
	MaxDownloadBytes int64
	Kind             string // "zip" | "installer"
	FileName         string
	BinarySubpath    string // zip only: directory inside the archive holding the binaries
}

var ocrTesseractComponent = ocrComponent{
	Name:    "tesseract",
	Version: "5.4.0.20240606",
	URL:     "https://github.com/UB-Mannheim/tesseract/releases/download/v5.4.0.20240606/tesseract-ocr-w64-setup-5.4.0.20240606.exe",
	SHA256:  "c885fff6998e0608ba4bb8ab51436e1c6775c2bafc2559a19b423e18678b60c9",
	// Observed official artifact: 50,175,248 bytes (HEAD, 2026-08-04);
	// 60 MiB leaves ~25% margin for a replacement build at this pin.
	MaxDownloadBytes: 60 << 20,
	Kind:             "installer",
	FileName:         "tesseract-ocr-w64-setup-5.4.0.20240606.exe",
}

var ocrPopplerComponent = ocrComponent{
	Name:    "poppler",
	Version: "26.02.0-0",
	URL:     "https://github.com/oschwartz10612/poppler-windows/releases/download/v26.02.0-0/Release-26.02.0-0.zip",
	SHA256:  "993e4a94376ed712fafc7058d724ea0b943d118bbd2305cd9ed55174eb85cda5",
	// Observed official artifact: 16,107,283 bytes (HEAD, 2026-08-04);
	// 20 MiB leaves ~30% margin for a replacement build at this pin.
	MaxDownloadBytes: 20 << 20,
	Kind:             "zip",
	FileName:         "poppler-26.02.0-0.zip",
	BinarySubpath:    "poppler-26.02.0/Library/bin",
}

// Used only by the legacy helper retained for existing callers/tests. The
// production installer uses each manifest component's calibrated cap.
const maxOCRDownloadBytes int64 = 64 << 20

// ocrInstallDir is where OCR components are installed - never inside the
// app's own install directory (CH-09: the app must work fully without OCR;
// this is an optional, user-triggered, per-machine add-on).
func ocrInstallDir() string {
	base := strings.TrimSpace(os.Getenv("APPDATA"))
	if base == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			base = dir
		}
	}
	return filepath.Join(base, "Sencia Job", "ocr")
}

func ocrTesseractExePath() string {
	return filepath.Join(ocrInstallDir(), "tesseract", "tesseract.exe")
}

func ocrPdftoppmPath() string {
	return filepath.Join(ocrInstallDir(), "poppler", "pdftoppm.exe")
}

type ocrStatusResponse struct {
	Installed        bool   `json:"installed"`
	Installing       bool   `json:"installing"`
	InstallMessage   string `json:"installMessage,omitempty"`
	TesseractVersion string `json:"tesseractVersion,omitempty"`
	PopplerVersion   string `json:"popplerVersion,omitempty"`
	TesseractPath    string `json:"tesseractPath,omitempty"`
	PdftoppmPath     string `json:"pdftoppmPath,omitempty"`
}

// ocrInstallState tracks the one background OCR install job, mirroring the
// running/done/message shape already used for the Camoufox bootstrap
// (browserBootstrapState) and the background search (liveSearchState).
type ocrInstallState struct {
	mu      sync.RWMutex
	running bool
	done    bool
	success bool
	message string
}

func (s *ocrInstallState) tryStart() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.running {
		return false
	}
	s.running = true
	s.done = false
	s.success = false
	s.message = "Installing OCR components..."
	return true
}

func (s *ocrInstallState) finish(success bool, message string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.running = false
	s.done = true
	s.success = success
	s.message = message
}

func (s *ocrInstallState) snapshot() (running bool, message string) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running, s.message
}

// downloadAndVerify streams url to a temp file while hashing it, and fails
// closed (deleting the partial download) if the SHA-256 does not match the
// pinned value - this is the only integrity check standing between a
// compromised/mismatched mirror and code that later executes (Tesseract) or
// gets loaded into the resume pipeline (poppler).
func downloadAndVerify(ctx context.Context, url, expectedSHA256 string) (string, error) {
	return downloadAndVerifyLimited(ctx, url, expectedSHA256, maxOCRDownloadBytes)
}

// downloadAndVerifyLimited streams url to a temporary file while hashing it,
// enforcing Content-Length before creating the file and a hard pre-write cap
// for chunked/unknown-length responses. Any failure removes the partial file.
func downloadAndVerifyLimited(ctx context.Context, url, expectedSHA256 string, maxBytes int64) (string, error) {
	if maxBytes <= 0 {
		return "", fmt.Errorf("invalid download limit %d", maxBytes)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
	}
	if resp.ContentLength > maxBytes {
		return "", &externalResponseTooLargeError{Protocol: "OCR download", Limit: maxBytes}
	}

	tmp, err := os.CreateTemp("", "sencia-ocr-*.download")
	if err != nil {
		return "", err
	}
	tmpPath := tmp.Name()
	removePartial := func() {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
	}

	hasher := sha256.New()
	writer := &limitedWriter{
		dst:      io.MultiWriter(tmp, hasher),
		limit:    maxBytes,
		protocol: "OCR download",
	}
	if _, err := io.Copy(writer, resp.Body); err != nil {
		removePartial()
		return "", err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return "", err
	}

	actual := hex.EncodeToString(hasher.Sum(nil))
	if !strings.EqualFold(actual, expectedSHA256) {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("sha256 mismatch for download: expected %s, got %s", expectedSHA256, actual)
	}
	return tmpPath, nil
}

// extractOCRZip extracts only the files under subpath (if given) from the
// zip into destDir, flattening that prefix away, with a zip-slip guard.
func extractOCRZip(zipPath, subpath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return err
	}
	defer zr.Close()

	prefix := subpath
	if prefix != "" && !strings.HasSuffix(prefix, "/") {
		prefix += "/"
	}
	cleanDestDir := filepath.Clean(destDir)

	extracted := 0
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		name := f.Name
		if prefix != "" {
			if !strings.HasPrefix(name, prefix) {
				continue
			}
			name = strings.TrimPrefix(name, prefix)
		}
		if name == "" {
			continue
		}
		destPath := filepath.Join(destDir, filepath.Clean(name))
		if destPath != cleanDestDir && !strings.HasPrefix(destPath, cleanDestDir+string(os.PathSeparator)) {
			return fmt.Errorf("unsafe zip entry path: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(destPath), 0o755); err != nil {
			return err
		}
		if err := extractZipFile(f, destPath); err != nil {
			return err
		}
		extracted++
	}
	if extracted == 0 {
		return fmt.Errorf("no files extracted from zip (subpath %q not found)", subpath)
	}
	return nil
}

func extractZipFile(f *zip.File, destPath string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, rc)
	return err
}

// runSilentInstaller runs an NSIS installer silently into destDir. NSIS
// convention: /S = silent, /D=<dir> must be the LAST argument and must not
// be quoted.
func runSilentInstaller(ctx context.Context, installerPath, destDir string) error {
	cmd := exec.CommandContext(ctx, installerPath, "/S", "/D="+destDir)
	hideWorkerConsole(cmd)
	return cmd.Run()
}

// installOCRComponent downloads+verifies component, then either extracts it
// (zip) or runs it as a silent installer targeting destDir.
func installOCRComponent(ctx context.Context, component ocrComponent, destDir string) error {
	downloadPath, err := downloadAndVerifyLimited(ctx, component.URL, component.SHA256, component.MaxDownloadBytes)
	if err != nil {
		return fmt.Errorf("%s: %w", component.Name, err)
	}
	defer os.Remove(downloadPath)

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return err
	}

	switch component.Kind {
	case "zip":
		if err := extractOCRZip(downloadPath, component.BinarySubpath, destDir); err != nil {
			return fmt.Errorf("%s: %w", component.Name, err)
		}
	case "installer":
		if err := runSilentInstaller(ctx, downloadPath, destDir); err != nil {
			return fmt.Errorf("%s: %w", component.Name, err)
		}
	default:
		return fmt.Errorf("%s: unknown OCR component kind %q", component.Name, component.Kind)
	}
	return nil
}

const ocrInstallTimeout = 10 * time.Minute

// ocrInstall downloads, verifies and installs both OCR components
// (poppler's pdftoppm + Tesseract) in the background (CH-09/D10). The app
// is fully functional without ever calling this - OCR is an explicit,
// user-triggered opt-in from Settings/Resume Studio.
func (a *api) ocrInstall(w http.ResponseWriter, r *http.Request) {
	if !a.ocrInstallState.tryStart() {
		writeJSON(w, http.StatusConflict, map[string]string{"message": "OCR installation is already in progress."})
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), ocrInstallTimeout)
		defer cancel()

		if err := installOCRComponent(ctx, ocrPopplerComponent, filepath.Join(ocrInstallDir(), "poppler")); err != nil {
			a.log("error", "ocr install: %v", err)
			a.ocrInstallState.finish(false, "Failed to install the PDF renderer (poppler). Check the logs.")
			return
		}
		if err := installOCRComponent(ctx, ocrTesseractComponent, filepath.Join(ocrInstallDir(), "tesseract")); err != nil {
			a.log("error", "ocr install: %v", err)
			a.ocrInstallState.finish(false, "Failed to install Tesseract OCR. Check the logs.")
			return
		}
		a.log("success", "[ OCR ] Tesseract + poppler installed at %s", ocrInstallDir())
		a.ocrInstallState.finish(true, "OCR components installed successfully.")
	}()

	writeJSON(w, http.StatusAccepted, map[string]string{"message": "OCR installation started."})
}

func (a *api) ocrStatus(w http.ResponseWriter, r *http.Request) {
	tesseractPath := ocrTesseractExePath()
	pdftoppmPath := ocrPdftoppmPath()
	tesseractOK := fileExists(tesseractPath)
	pdftoppmOK := fileExists(pdftoppmPath)
	running, message := a.ocrInstallState.snapshot()

	resp := ocrStatusResponse{
		Installed:      tesseractOK && pdftoppmOK,
		Installing:     running,
		InstallMessage: message,
	}
	if tesseractOK {
		resp.TesseractVersion = ocrTesseractComponent.Version
		resp.TesseractPath = tesseractPath
	}
	if pdftoppmOK {
		resp.PopplerVersion = ocrPopplerComponent.Version
		resp.PdftoppmPath = pdftoppmPath
	}
	writeJSON(w, http.StatusOK, resp)
}
