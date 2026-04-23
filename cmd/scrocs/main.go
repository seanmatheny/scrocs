package main

import (
	"archive/zip"
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"

	"github.com/jung-kurt/gofpdf"
	"github.com/seanmatheny/scrocs/internal/mtpclient"
)

type config struct {
	Home          string
	LockDir       string
	LogFile       string
	RawDir        string
	PDFDir        string
	StateFile     string
	DevicePattern string
}

func main() {
	cfg := loadConfig()
	if err := os.MkdirAll(cfg.Home, 0o755); err != nil {
		fatalf("create home dir: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(cfg.LogFile), 0o755); err != nil {
		fatalf("create log dir: %v", err)
	}
	logFile, err := os.OpenFile(cfg.LogFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fatalf("open log file: %v", err)
	}
	defer logFile.Close()
	logger := log.New(logFile, "", log.LstdFlags|log.LUTC)

	for _, dir := range []string{cfg.RawDir, cfg.PDFDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			logger.Fatalf("create %s: %v", dir, err)
		}
	}

	release, err := acquireLock(cfg.LockDir, logger)
	if err != nil {
		logger.Printf("%v", err)
		return
	}
	defer release()

	if !isKindleConnected() {
		logger.Printf("Kindle Scribe not detected")
		return
	}

	logger.Printf("Kindle Scribe detected; starting sync")
	if err := mtpclient.SyncRawKindleFiles(cfg.RawDir, cfg.DevicePattern, logger); err != nil {
		logger.Printf("sync failed: %v", err)
		return
	}

	state, err := loadImportState(cfg.StateFile)
	if err != nil {
		logger.Printf("state load failed: %v", err)
		return
	}

	rawFiles, err := listRawFiles(cfg.RawDir)
	if err != nil {
		logger.Printf("list raw files: %v", err)
		return
	}

	for _, rawFile := range rawFiles {
		pdfFile, err := convertToPDF(rawFile, cfg.RawDir, cfg.PDFDir, logger)
		if err != nil {
			logger.Printf("convert failed for %s: %v", rawFile, err)
			continue
		}

		fileHash, err := sha256File(pdfFile)
		if err != nil {
			logger.Printf("hash failed for %s: %v", pdfFile, err)
			continue
		}
		if _, ok := state[fileHash]; ok {
			continue
		}

		if err := importPDFToBear(pdfFile); err != nil {
			logger.Printf("Bear import failed for %s: %v", pdfFile, err)
			continue
		}
		if err := appendImportState(cfg.StateFile, fileHash); err != nil {
			logger.Printf("state update failed for %s: %v", pdfFile, err)
			continue
		}
		state[fileHash] = struct{}{}
		logger.Printf("Imported %s into Bear", filepath.Base(pdfFile))
	}

	logger.Printf("Sync complete")
}

func loadConfig() config {
	home := getenvDefault("SCROCS_HOME", filepath.Join(os.Getenv("HOME"), ".local", "share", "scrocs"))
	return config{
		Home:          home,
		LockDir:       filepath.Join(home, ".lock"),
		LogFile:       getenvDefault("SCROCS_LOG_FILE", filepath.Join(home, "scrocs.log")),
		RawDir:        getenvDefault("SCROCS_RAW_DIR", filepath.Join(home, "raw")),
		PDFDir:        getenvDefault("SCROCS_PDF_DIR", filepath.Join(home, "pdf")),
		StateFile:     getenvDefault("SCROCS_STATE_FILE", filepath.Join(home, "imported.sha256")),
		DevicePattern: getenvDefault("SCROCS_MTP_PATTERN", "(?i)kindle"),
	}
}

func getenvDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func fatalf(format string, args ...any) {
	_, _ = fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}

func acquireLock(lockDir string, logger *log.Logger) (func(), error) {
	if err := os.Mkdir(lockDir, 0o755); err == nil {
		pidFile := filepath.Join(lockDir, "pid")
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
		return func() {
			_ = os.RemoveAll(lockDir)
		}, nil
	}

	pidFile := filepath.Join(lockDir, "pid")
	pidBytes, _ := os.ReadFile(pidFile)
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidBytes)))
	if pid > 0 {
		err := syscall.Kill(pid, 0)
		if err == nil || errors.Is(err, syscall.EPERM) {
			return nil, fmt.Errorf("another sync is already running")
		}
	}

	logger.Printf("Found stale lock; cleaning up")
	_ = os.RemoveAll(lockDir)
	if err := os.Mkdir(lockDir, 0o755); err != nil {
		return nil, fmt.Errorf("create lock: %w", err)
	}
	pidFile = filepath.Join(lockDir, "pid")
	_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
	return func() {
		_ = os.RemoveAll(lockDir)
	}, nil
}

func isKindleConnected() bool {
	if _, err := exec.LookPath("system_profiler"); err != nil {
		return true
	}
	out, err := exec.Command("/usr/sbin/system_profiler", "SPUSBDataType").Output()
	if err != nil {
		return false
	}
	re := regexp.MustCompile(`(?i)Kindle( Scribe)?|Amazon Kindle`)
	return re.Match(out)
}

func listRawFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".notebook" || ext == ".note" || ext == ".pdf" {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(files)
	return files, nil
}

func convertToPDF(inputFile, rawRoot, pdfRoot string, logger *log.Logger) (string, error) {
	rel, err := filepath.Rel(rawRoot, inputFile)
	if err != nil {
		return "", err
	}
	output := filepath.Join(pdfRoot, strings.TrimSuffix(rel, filepath.Ext(rel))+".pdf")
	if !isSafePath(pdfRoot, output) {
		return "", fmt.Errorf("unsafe output path: %s", output)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return "", err
	}
	if upToDate(inputFile, output) {
		return output, nil
	}

	ext := strings.ToLower(filepath.Ext(inputFile))
	if ext == ".pdf" {
		if err := copyFile(inputFile, output); err != nil {
			return "", err
		}
		return output, nil
	}

	if err := convertNotebookLikeToPDF(inputFile, output, logger); err != nil {
		return "", err
	}
	return output, nil
}

func upToDate(input, output string) bool {
	in, err := os.Stat(input)
	if err != nil {
		return false
	}
	out, err := os.Stat(output)
	if err != nil {
		return false
	}
	return !out.ModTime().Before(in.ModTime()) && out.Size() > 0
}

func convertNotebookLikeToPDF(inputFile, outputFile string, logger *log.Logger) error {
	if isLikelyPDF(inputFile) {
		return copyFile(inputFile, outputFile)
	}

	zr, err := zip.OpenReader(inputFile)
	if err != nil {
		return fmt.Errorf("not a supported notebook archive: %w", err)
	}
	defer zr.Close()

	var pdfEntry *zip.File
	var imageEntries []*zip.File
	for _, f := range zr.File {
		if f.FileInfo().IsDir() {
			continue
		}
		if !isSafeArchiveName(f.Name) {
			continue
		}
		ext := strings.ToLower(filepath.Ext(f.Name))
		switch ext {
		case ".pdf":
			if pdfEntry == nil {
				pdfEntry = f
			}
		case ".png", ".jpg", ".jpeg":
			imageEntries = append(imageEntries, f)
		}
	}

	if pdfEntry != nil {
		if err := extractZipFileToPath(pdfEntry, outputFile); err != nil {
			return err
		}
		logger.Printf("Converted %s via embedded PDF", filepath.Base(inputFile))
		return nil
	}

	if len(imageEntries) == 0 {
		return fmt.Errorf("no convertible PDF or images found in notebook archive")
	}
	sort.Slice(imageEntries, func(i, j int) bool { return imageEntries[i].Name < imageEntries[j].Name })

	tempDir, err := os.MkdirTemp("", "scrocs-img-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)

	imgPaths := make([]string, 0, len(imageEntries))
	for i, f := range imageEntries {
		ext := strings.ToLower(filepath.Ext(f.Name))
		p := filepath.Join(tempDir, fmt.Sprintf("%06d%s", i, ext))
		if err := extractZipFileToPath(f, p); err != nil {
			return err
		}
		imgPaths = append(imgPaths, p)
	}

	if err := imagesToPDF(imgPaths, outputFile); err != nil {
		return err
	}
	logger.Printf("Converted %s via embedded images", filepath.Base(inputFile))
	return nil
}

func isLikelyPDF(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()

	header := make([]byte, 5)
	n, err := io.ReadFull(f, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	return n >= 5 && string(header) == "%PDF-"
}

func extractZipFileToPath(zf *zip.File, dest string) error {
	if !isSafeArchiveName(zf.Name) {
		return fmt.Errorf("unsafe archive entry: %s", zf.Name)
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	r, err := zf.Open()
	if err != nil {
		return err
	}
	defer r.Close()

	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(tmp)
		return copyErr
	}
	if closeErr != nil {
		_ = os.Remove(tmp)
		return closeErr
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func isSafeArchiveName(name string) bool {
	clean := filepath.Clean(name)
	if filepath.IsAbs(clean) {
		return false
	}
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return false
	}
	return !strings.Contains(clean, string(filepath.Separator)+".."+string(filepath.Separator))
}

func imagesToPDF(images []string, outFile string) error {
	pdf := gofpdf.New("P", "pt", "A4", "")
	for _, img := range images {
		ext := strings.TrimPrefix(strings.ToLower(filepath.Ext(img)), ".")
		if ext == "jpg" {
			ext = "jpeg"
		}
		opts := gofpdf.ImageOptions{ImageType: ext, ReadDpi: true}
		name := "img-" + strings.ReplaceAll(filepath.Base(img), ".", "-")
		info := pdf.RegisterImageOptions(img, opts)
		if err := pdf.Error(); err != nil {
			return err
		}
		w, h := info.Extent()
		if w <= 0 || h <= 0 {
			return fmt.Errorf("invalid image dimensions for %s", img)
		}
		pdf.AddPageFormat("P", gofpdf.SizeType{Wd: w, Ht: h})
		pdf.ImageOptions(name, 0, 0, w, h, false, opts, 0, "")
	}
	return pdf.OutputFileAndClose(outFile)
}

func copyFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func loadImportState(path string) (map[string]struct{}, error) {
	state := make(map[string]struct{})
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return state, nil
		}
		return nil, err
	}
	defer f.Close()

	s := bufio.NewScanner(f)
	for s.Scan() {
		h := strings.TrimSpace(s.Text())
		if h != "" {
			state[h] = struct{}{}
		}
	}
	return state, s.Err()
}

func appendImportState(path, hash string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintln(f, hash)
	return err
}

func importPDFToBear(pdfPath string) error {
	title := "Kindle Scribe - " + strings.TrimSuffix(filepath.Base(pdfPath), filepath.Ext(pdfPath))
	callbackURL := "bear://x-callback-url/add-file?file=" + url.QueryEscape(pdfPath) +
		"&title=" + url.QueryEscape(title) +
		"&new_window=no&show_window=no"

	cmd := exec.Command("open", "-g", callbackURL)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run()
}

func isSafePath(root, candidate string) bool {
	root = filepath.Clean(root)
	candidate = filepath.Clean(candidate)
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
