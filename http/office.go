package fbhttp

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/afero"

	"github.com/filebrowser/filebrowser/v2/files"
)

// Office document preview.
//
// Office files (doc/docx/ppt/pptx/xls/xlsx and ODF variants) are converted to
// PDF server-side with LibreOffice (`soffice`) and streamed to the browser.
// This keeps the client fast over slow/remote links: instead of downloading a
// large source file and parsing it in-browser, the browser receives a compact
// PDF that its native viewer renders progressively via range requests.
//
// Converted PDFs are cached on disk keyed by path+modtime+size, so each file is
// only converted once. Conversion runs under a small concurrency limit to bound
// memory, since soffice is heavy.

var officePreviewExts = map[string]bool{
	".doc": true, ".docx": true, ".docm": true, ".dot": true, ".dotx": true,
	".odt": true, ".ott": true, ".rtf": true, ".fodt": true,
	".xls": true, ".xlsx": true, ".xlsm": true, ".xlt": true, ".xltx": true,
	".ods": true, ".ots": true, ".fods": true,
	".ppt": true, ".pptx": true, ".pptm": true, ".pot": true, ".potx": true,
	".odp": true, ".otp": true, ".fodp": true,
}

// IsOfficePreviewExt reports whether an extension can be previewed as PDF.
func IsOfficePreviewExt(ext string) bool {
	return officePreviewExts[strings.ToLower(ext)]
}

// officeConvertSem bounds concurrent soffice processes (memory protection).
var officeConvertSem = make(chan struct{}, 1)

func officeCacheDir() string {
	return filepath.Join(os.TempDir(), "fb-office-pdf")
}

func sofficeBinary() string {
	for _, name := range []string{"soffice", "libreoffice"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	return ""
}

// convertToPDF converts src (an on-disk office file) to a PDF and returns the
// path to the generated PDF inside a unique output directory.
func convertToPDF(bin, src string) (string, error) {
	work, err := os.MkdirTemp("", "fb-office-conv-")
	if err != nil {
		return "", err
	}
	profile := filepath.Join(work, "profile")
	outDir := filepath.Join(work, "out")
	if mkErr := os.MkdirAll(outDir, 0o700); mkErr != nil {
		return "", mkErr
	}

	ctxCmd := exec.Command(bin,
		"--headless",
		"--norestore",
		"-env:UserInstallation=file://"+profile,
		"--convert-to", "pdf",
		"--outdir", outDir,
		src,
	)
	ctxCmd.Env = append(os.Environ(), "HOME="+work)

	done := make(chan error, 1)
	if startErr := ctxCmd.Start(); startErr != nil {
		return "", startErr
	}
	go func() { done <- ctxCmd.Wait() }()

	select {
	case <-time.After(120 * time.Second):
		_ = ctxCmd.Process.Kill()
		return "", errors.New("conversion timed out")
	case waitErr := <-done:
		if waitErr != nil {
			return "", fmt.Errorf("soffice: %w", waitErr)
		}
	}

	// soffice names the output <basename>.pdf in outDir.
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src)) + ".pdf"
	out := filepath.Join(outDir, base)
	if _, statErr := os.Stat(out); statErr != nil {
		return "", errors.New("conversion produced no output")
	}
	return out, nil
}

// PLACEHOLDER_OFFICE_HANDLER

// officePreviewHandler serves a cached/freshly-converted PDF rendering of an
// Office document at the request path.
var officePreviewHandler = withUser(func(w http.ResponseWriter, r *http.Request, d *data) (int, error) {
	if !d.user.Perm.Download {
		return http.StatusForbidden, nil
	}

	bin := sofficeBinary()
	if bin == "" {
		return http.StatusNotImplemented, errors.New("libreoffice (soffice) not available")
	}

	file, err := files.NewFileInfo(&files.FileOptions{
		Fs:         d.user.Fs,
		Path:       r.URL.Path,
		Modify:     d.user.Perm.Modify,
		Expand:     false,
		ReadHeader: d.server.TypeDetectionByHeader,
		Checker:    d,
	})
	if err != nil {
		return errToStatus(err), err
	}
	if file.IsDir || !IsOfficePreviewExt(file.Extension) {
		return http.StatusBadRequest, errors.New("not an office document")
	}

	// Cache key is stable per file content version.
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d", file.Path, file.ModTime.UnixNano(), file.Size)))
	key := hex.EncodeToString(sum[:])
	cacheDir := officeCacheDir()
	if mkErr := os.MkdirAll(cacheDir, 0o700); mkErr != nil {
		return http.StatusInternalServerError, mkErr
	}
	cached := filepath.Join(cacheDir, key+".pdf")

	if _, statErr := os.Stat(cached); statErr != nil {
		// Not cached yet: convert under the concurrency limit.
		officeConvertSem <- struct{}{}
		defer func() { <-officeConvertSem }()

		// Another request may have produced it while we waited for the slot.
		if _, statErr2 := os.Stat(cached); statErr2 != nil {
			if convErr := produceOfficePDF(d.user.Fs, file.Path, file.Extension, bin, cached); convErr != nil {
				return http.StatusInternalServerError, convErr
			}
		}
	}

	fd, err := os.Open(cached)
	if err != nil {
		return http.StatusInternalServerError, err
	}
	defer fd.Close()
	info, err := fd.Stat()
	if err != nil {
		return http.StatusInternalServerError, err
	}

	w.Header().Set("Content-Type", "application/pdf")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	http.ServeContent(w, r, file.Name+".pdf", info.ModTime(), fd)
	return 0, nil
})

// produceOfficePDF copies the source document to a temp file, converts it with
// soffice, and atomically moves the resulting PDF into dst.
func produceOfficePDF(fs afero.Fs, path, ext, bin, dst string) error {
	content, err := afero.ReadFile(fs, path)
	if err != nil {
		return err
	}

	inDir, err := os.MkdirTemp("", "fb-office-in-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(inDir)

	src := filepath.Join(inDir, "input"+strings.ToLower(ext))
	if writeErr := os.WriteFile(src, content, 0o600); writeErr != nil {
		return writeErr
	}

	pdf, err := convertToPDF(bin, src)
	if err != nil {
		return err
	}
	defer os.RemoveAll(filepath.Dir(filepath.Dir(pdf)))

	data, err := os.ReadFile(pdf)
	if err != nil {
		return err
	}

	tmp := dst + ".tmp"
	if writeErr := os.WriteFile(tmp, data, 0o600); writeErr != nil {
		return writeErr
	}
	return os.Rename(tmp, dst)
}
