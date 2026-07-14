package player

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
)

// DownloadState represents the current state of a download job.
type DownloadState int

const (
	StateQueued DownloadState = iota
	StateActive
	StateDone
	StateFailed
	StateCancelled
	StateSkipped
)

// DownloadJob represents a single download in the queue.
type DownloadJob struct {
	ID         int
	Label      string // the file name being downloaded
	URL        string
	DestPath   string
	State      DownloadState
	Phase      string // "connecting", "allocating", "downloading"
	BytesRead  int64
	TotalBytes int64 // -1 if unknown
	Speed      int64 // bytes/sec
	ETA        string
	Conns      int
	Err        error
	SkipReason string
	StartedAt  time.Time
	FinishedAt time.Time
}

func (j *DownloadJob) Progress() float64 {
	if j.TotalBytes <= 0 {
		return 0
	}
	return float64(j.BytesRead) / float64(j.TotalBytes)
}

// DownloadManager manages a queue of downloads with progress tracking.
type DownloadManager struct {
	mu         sync.Mutex
	jobs       []*DownloadJob
	nextID     int
	cancelFns  map[int]context.CancelFunc // per-job cancel functions
	parallel   int
	active     int
	useAria2c  bool
	aria2cPath string
}

func NewDownloadManager(parallel int, useAria2c bool) *DownloadManager {
	if parallel < 1 {
		parallel = 1
	}
	dm := &DownloadManager{
		parallel:  parallel,
		useAria2c: useAria2c,
		cancelFns: make(map[int]context.CancelFunc),
	}
	if useAria2c {
		if path, err := exec.LookPath("aria2c"); err == nil {
			dm.aria2cPath = path
		}
	}
	return dm
}

// HasAria2c reports whether aria2c was found.
func (dm *DownloadManager) HasAria2c() bool {
	return dm.aria2cPath != ""
}

// Enqueue adds a download job to the queue. Returns the job ID.
func (dm *DownloadManager) Enqueue(label, url, destPath string) int {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	id := dm.nextID
	dm.nextID++
	dm.jobs = append(dm.jobs, &DownloadJob{
		ID:         id,
		Label:      label,
		URL:        url,
		DestPath:   destPath,
		State:      StateQueued,
		TotalBytes: -1,
	})
	return id
}

// Jobs returns a snapshot of all jobs.
func (dm *DownloadManager) Jobs() []*DownloadJob {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	snap := make([]*DownloadJob, len(dm.jobs))
	for i, j := range dm.jobs {
		copy := *j
		snap[i] = &copy
	}
	return snap
}

// ActiveCount returns number of currently downloading jobs.
func (dm *DownloadManager) ActiveCount() int {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	return dm.active
}

// QueuedCount returns number of queued jobs.
func (dm *DownloadManager) QueuedCount() int {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	count := 0
	for _, j := range dm.jobs {
		if j.State == StateQueued {
			count++
		}
	}
	return count
}

// FreeSlots returns how many more jobs can start concurrently.
func (dm *DownloadManager) FreeSlots() int {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	n := dm.parallel - dm.active
	if n < 0 {
		return 0
	}
	return n
}

// CancelJob cancels a specific job (marks queued as cancelled, or cancels active via context).
func (dm *DownloadManager) CancelJob(id int) {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	for _, j := range dm.jobs {
		if j.ID == id {
			if j.State == StateQueued {
				j.State = StateCancelled
			} else if j.State == StateActive {
				if cancel, ok := dm.cancelFns[id]; ok {
					cancel()
				}
			}
			return
		}
	}
}

// CancelAll cancels all queued and active jobs.
func (dm *DownloadManager) CancelAll() {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	for _, j := range dm.jobs {
		if j.State == StateQueued {
			j.State = StateCancelled
		} else if j.State == StateActive {
			if cancel, ok := dm.cancelFns[j.ID]; ok {
				cancel()
			}
		}
	}
}

// ClearDone removes completed/failed/cancelled/skipped jobs from the list.
func (dm *DownloadManager) ClearDone() {
	dm.mu.Lock()
	defer dm.mu.Unlock()
	var kept []*DownloadJob
	for _, j := range dm.jobs {
		if j.State == StateQueued || j.State == StateActive {
			kept = append(kept, j)
		}
	}
	dm.jobs = kept
}

// ProcessQueue processes the next available queued job. Returns the job that was
// started (now finished), or nil if none was available.
func (dm *DownloadManager) ProcessQueue(ctx context.Context) (*DownloadJob, error) {
	dm.mu.Lock()
	if dm.active >= dm.parallel {
		dm.mu.Unlock()
		return nil, nil
	}
	var job *DownloadJob
	for _, j := range dm.jobs {
		if j.State == StateQueued {
			job = j
			break
		}
	}
	if job == nil {
		dm.mu.Unlock()
		return nil, nil
	}
	job.State = StateActive
	job.Phase = "connecting"
	job.StartedAt = time.Now()
	dm.active++

	jobCtx, cancel := context.WithCancel(ctx)
	dm.cancelFns[job.ID] = cancel
	dm.mu.Unlock()

	// Validate destination path early so a bad path (e.g. an unusable
	// directory) fails the job cleanly instead of crashing the app.
	if job.DestPath == "" {
		dm.finish(job, cancel, fmt.Errorf("empty destination path"), jobCtx)
		return job, nil
	}

	if _, err := os.Stat(job.DestPath); err == nil {
		dm.mu.Lock()
		job.State = StateSkipped
		job.SkipReason = "file already exists"
		job.FinishedAt = time.Now()
		dm.active--
		delete(dm.cancelFns, job.ID)
		dm.mu.Unlock()
		cancel()
		return job, nil
	}

	dir := filepath.Dir(job.DestPath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		dm.finish(job, cancel, fmt.Errorf("create directory: %w", err), jobCtx)
		return job, nil
	}

	var err error
	func() {
		// Guard against any unexpected panic in a backend so a single bad
		// download can never take down the whole application.
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("download error: %v", r)
			}
		}()
		if dm.aria2cPath != "" {
			err = dm.downloadWithAria2c(jobCtx, job)
		} else {
			err = dm.downloadHTTP(jobCtx, job)
		}
	}()

	dm.finish(job, cancel, err, jobCtx)
	return job, nil
}

// finish records the terminal state of a job.
func (dm *DownloadManager) finish(job *DownloadJob, cancel context.CancelFunc, err error, jobCtx context.Context) {
	dm.mu.Lock()
	if err != nil {
		if jobCtx.Err() != nil {
			job.State = StateCancelled
		} else {
			job.State = StateFailed
			job.Err = err
		}
	} else {
		job.State = StateDone
		job.Phase = ""
	}
	job.FinishedAt = time.Now()
	dm.active--
	delete(dm.cancelFns, job.ID)
	dm.mu.Unlock()
	cancel()
}

func (dm *DownloadManager) downloadHTTP(ctx context.Context, job *DownloadJob) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, job.URL, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 0}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	dm.mu.Lock()
	job.TotalBytes = resp.ContentLength
	job.Phase = "downloading"
	dm.mu.Unlock()

	tmpPath := job.DestPath + ".part"
	f, err := os.Create(tmpPath)
	if err != nil {
		return fmt.Errorf("create file: %w", err)
	}
	defer func() {
		f.Close()
		if err != nil {
			os.Remove(tmpPath)
		}
	}()

	buf := make([]byte, 32*1024)
	var lastUpdate time.Time
	var lastBytes int64

	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, wErr := f.Write(buf[:n]); wErr != nil {
				err = wErr
				return err
			}
			dm.mu.Lock()
			job.BytesRead += int64(n)
			now := time.Now()
			if now.Sub(lastUpdate) > 500*time.Millisecond {
				elapsed := now.Sub(lastUpdate).Seconds()
				if elapsed > 0 && !lastUpdate.IsZero() {
					job.Speed = int64(float64(job.BytesRead-lastBytes) / elapsed)
				}
				lastUpdate = now
				lastBytes = job.BytesRead
			}
			dm.mu.Unlock()
		}
		if readErr != nil {
			if readErr == io.EOF {
				break
			}
			err = readErr
			return err
		}
	}

	f.Close()
	if renameErr := os.Rename(tmpPath, job.DestPath); renameErr != nil {
		err = renameErr
		return err
	}
	return nil
}

// aria2ProgressRe parses aria2c console progress lines such as:
//
//	[#7d0cf1 12MiB/1.2GiB(1%) CN:16 DL:5.2MiB ETA:3m52s]
var (
	aria2SizeRe  = regexp.MustCompile(`([0-9.]+[KMGT]?i?B)/([0-9.]+[KMGT]?i?B)\((\d+)%\)`)
	aria2DLRe    = regexp.MustCompile(`DL:([0-9.]+[KMGT]?i?B)`)
	aria2ETARe   = regexp.MustCompile(`ETA:([0-9smhd]+)`)
	aria2CNRe    = regexp.MustCompile(`CN:(\d+)`)
	aria2AllocRe = regexp.MustCompile(`(?i)FileAlloc|Allocat`)
)

func (dm *DownloadManager) downloadWithAria2c(ctx context.Context, job *DownloadJob) error {
	dir := filepath.Dir(job.DestPath)
	name := filepath.Base(job.DestPath)

	args := []string{
		"-x", "16",
		"-s", "16",
		"-k", "1M",
		"--dir", dir,
		"--out", name,
		// Skip slow pre-allocation so large files start downloading
		// immediately instead of stalling on file allocation.
		"--file-allocation=none",
		"--auto-file-renaming=false",
		"--allow-overwrite=true",
		"--console-log-level=warn",
		"--summary-interval=1",
		"--download-result=hide",
		job.URL,
	}

	cmd := exec.CommandContext(ctx, dm.aria2cPath, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("aria2c pipe: %w", err)
	}
	cmd.Stderr = cmd.Stdout

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("aria2c start: %w", err)
	}

	dm.mu.Lock()
	job.Phase = "downloading"
	dm.mu.Unlock()

	dm.scanAria2Output(job, stdout)

	if waitErr := cmd.Wait(); waitErr != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("aria2c: %w", waitErr)
	}
	return nil
}

// scanAria2Output reads aria2c's console output (which uses \r to refresh the
// progress line) and updates the job's live progress fields.
func (dm *DownloadManager) scanAria2Output(job *DownloadJob, r io.Reader) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	sc.Split(scanLinesCR)
	for sc.Scan() {
		line := sc.Text()
		if aria2AllocRe.MatchString(line) {
			dm.mu.Lock()
			job.Phase = "allocating"
			dm.mu.Unlock()
		}
		if m := aria2SizeRe.FindStringSubmatch(line); m != nil {
			read := parseAria2Size(m[1])
			total := parseAria2Size(m[2])
			dm.mu.Lock()
			if read > 0 {
				job.BytesRead = read
			}
			if total > 0 {
				job.TotalBytes = total
			}
			job.Phase = "downloading"
			dm.mu.Unlock()
		}
		if m := aria2DLRe.FindStringSubmatch(line); m != nil {
			dm.mu.Lock()
			job.Speed = parseAria2Size(m[1])
			dm.mu.Unlock()
		}
		if m := aria2ETARe.FindStringSubmatch(line); m != nil {
			dm.mu.Lock()
			job.ETA = m[1]
			dm.mu.Unlock()
		}
		if m := aria2CNRe.FindStringSubmatch(line); m != nil {
			n, _ := strconv.Atoi(m[1])
			dm.mu.Lock()
			job.Conns = n
			dm.mu.Unlock()
		}
	}
}

// scanLinesCR is a bufio.SplitFunc that splits on either \n or \r so aria2c's
// carriage-return progress refreshes are seen as individual tokens.
func scanLinesCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i, b := range data {
		if b == '\n' || b == '\r' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// parseAria2Size converts an aria2 size token like "1.2GiB", "512MiB",
// "5.0KiB" or "123B" to bytes.
func parseAria2Size(s string) int64 {
	s = strings.TrimSpace(s)
	re := regexp.MustCompile(`^([0-9.]+)\s*([KMGT]?)i?B$`)
	m := re.FindStringSubmatch(s)
	if m == nil {
		return 0
	}
	val, err := strconv.ParseFloat(m[1], 64)
	if err != nil {
		return 0
	}
	switch m[2] {
	case "T":
		val *= 1024 * 1024 * 1024 * 1024
	case "G":
		val *= 1024 * 1024 * 1024
	case "M":
		val *= 1024 * 1024
	case "K":
		val *= 1024
	}
	return int64(val)
}

// --- Path helpers ---

// SanitizePath removes characters invalid in file/directory names.
func SanitizePath(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
			b.WriteRune('_')
		default:
			if unicode.IsControl(r) {
				continue
			}
			b.WriteRune(r)
		}
	}
	// Trim trailing dots/spaces which are illegal in Windows names.
	return strings.TrimRight(strings.TrimSpace(b.String()), ". ")
}

// yearRe matches the first standalone 4-digit year in a string.
var yearRe = regexp.MustCompile(`\d{4}`)

// normalizeYear extracts the first 4-digit year from a release-info string.
func normalizeYear(year string) string {
	return yearRe.FindString(year)
}

// titledFolder builds "<Name> (<Year>)" or just "<Name>" when year is absent.
func titledFolder(name, year string) string {
	year = normalizeYear(year)
	folder := SanitizePath(name)
	if year != "" {
		folder = fmt.Sprintf("%s (%s)", SanitizePath(name), year)
	}
	if folder == "" {
		folder = "Unknown"
	}
	return folder
}

// MoviePath returns: <base>/<Name> (<Year>)/<filename>
// The original filename is preserved verbatim (only sanitized).
func MoviePath(base, name, year, filename string) string {
	folder := titledFolder(name, year)
	return filepath.Join(base, folder, SanitizePath(filename))
}

// EpisodePath returns: <base>/<Show> (<Year>)/Season XX/<filename>
// The original filename is preserved verbatim (only sanitized).
func EpisodePath(base, showName, year string, season int, filename string) string {
	folder := titledFolder(showName, year)
	seasonDir := fmt.Sprintf("Season %02d", season)
	return filepath.Join(base, folder, seasonDir, SanitizePath(filename))
}

// GuessExtension extracts file extension from URL, defaults to .mkv.
func GuessExtension(url string) string {
	clean := strings.SplitN(url, "?", 2)[0]
	clean = strings.SplitN(clean, "#", 2)[0]
	ext := filepath.Ext(clean)
	switch strings.ToLower(ext) {
	case ".mkv", ".mp4", ".avi", ".webm", ".ts":
		return ext
	}
	return ".mkv"
}

// FilenameFromURL returns the last path segment of a URL if it looks like a
// video file, else "".
func FilenameFromURL(url string) string {
	clean := strings.SplitN(url, "?", 2)[0]
	clean = strings.SplitN(clean, "#", 2)[0]
	base := clean
	if i := strings.LastIndexAny(clean, "/\\"); i >= 0 {
		base = clean[i+1:]
	}
	switch strings.ToLower(filepath.Ext(base)) {
	case ".mkv", ".mp4", ".avi", ".webm", ".ts":
		return base
	}
	return ""
}

// ExtractReleaseGroup pulls release group from stream name.
func ExtractReleaseGroup(streamName string) string {
	name := strings.TrimSpace(streamName)
	if name == "" {
		return "Unknown"
	}
	lines := strings.SplitN(name, "\n", 2)
	firstLine := strings.TrimSpace(lines[0])

	re := regexp.MustCompile(`-([A-Za-z0-9]+)(?:\s*$|\s*\n)`)
	if matches := re.FindStringSubmatch(name); len(matches) > 1 {
		return matches[1]
	}
	re2 := regexp.MustCompile(`\[([A-Za-z0-9]+)\]`)
	if matches := re2.FindStringSubmatch(name); len(matches) > 1 {
		return matches[1]
	}
	return firstLine
}

// ParseResolution extracts resolution from stream name/title.
func ParseResolution(texts ...string) string {
	combined := strings.Join(texts, " ")
	lower := strings.ToLower(combined)
	switch {
	case strings.Contains(lower, "2160p") || strings.Contains(lower, "4k"):
		return "4K"
	case strings.Contains(lower, "1080p"):
		return "1080p"
	case strings.Contains(lower, "720p"):
		return "720p"
	case strings.Contains(lower, "480p"):
		return "480p"
	}
	return ""
}

// ParseSource extracts the file source (WEB-DL, BluRay, etc.) from stream text.
func ParseSource(texts ...string) string {
	lower := strings.ToLower(strings.Join(texts, " "))
	switch {
	case strings.Contains(lower, "bluray") || strings.Contains(lower, "blu-ray") || strings.Contains(lower, "bdrip"):
		return "BluRay"
	case strings.Contains(lower, "web-dl") || strings.Contains(lower, "webdl"):
		return "WEB-DL"
	case strings.Contains(lower, "webrip"):
		return "WEBRip"
	case strings.Contains(lower, "web"):
		return "WEB"
	case strings.Contains(lower, "hdtv"):
		return "HDTV"
	case strings.Contains(lower, "dvdrip") || strings.Contains(lower, "dvd"):
		return "DVD"
	case strings.Contains(lower, "cam") || strings.Contains(lower, "hdcam"):
		return "CAM"
	}
	return ""
}

// ParseCodec extracts the video codec from stream text (HEVC, AVC, AV1).
func ParseCodec(texts ...string) string {
	lower := strings.ToLower(strings.Join(texts, " "))
	switch {
	case strings.Contains(lower, "av1"):
		return "AV1"
	case strings.Contains(lower, "hevc") || strings.Contains(lower, "x265") ||
		strings.Contains(lower, "h.265") || strings.Contains(lower, "h265"):
		return "HEVC"
	case strings.Contains(lower, "avc") || strings.Contains(lower, "x264") ||
		strings.Contains(lower, "h.264") || strings.Contains(lower, "h264"):
		return "AVC"
	}
	return ""
}

// ParseSize extracts file size from stream name/title (e.g. "4.2 GB", "850 MB").
func ParseSize(texts ...string) (bytes int64, display string) {
	combined := strings.Join(texts, " ")
	re := regexp.MustCompile(`(\d+(?:\.\d+)?)\s*(GB|MB|TB)`)
	matches := re.FindStringSubmatch(combined)
	if len(matches) < 3 {
		return 0, ""
	}
	val, _ := strconv.ParseFloat(matches[1], 64)
	unit := strings.ToUpper(matches[2])
	switch unit {
	case "TB":
		bytes = int64(val * 1024 * 1024 * 1024 * 1024)
	case "GB":
		bytes = int64(val * 1024 * 1024 * 1024)
	case "MB":
		bytes = int64(val * 1024 * 1024)
	}
	return bytes, matches[0]
}

// FormatBytes formats bytes to human-readable string.
func FormatBytes(b int64) string {
	switch {
	case b >= 1024*1024*1024:
		return fmt.Sprintf("%.1f GB", float64(b)/(1024*1024*1024))
	case b >= 1024*1024:
		return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
	case b >= 1024:
		return fmt.Sprintf("%.1f KB", float64(b)/1024)
	}
	return fmt.Sprintf("%d B", b)
}
