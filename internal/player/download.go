package player

import (
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
	ID          int
	Label       string // e.g. "S01E05 - Gray Matter"
	URL         string
	DestPath    string
	State       DownloadState
	BytesRead   int64
	TotalBytes  int64 // -1 if unknown
	Speed       int64 // bytes/sec
	Err         error
	SkipReason  string
	StartedAt   time.Time
	FinishedAt  time.Time
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

// ProcessQueue processes the next available queued job. Returns true if a job was started.
// This should be called repeatedly via tea.Cmd.
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
	job.StartedAt = time.Now()
	dm.active++

	// Create per-job cancel context
	jobCtx, cancel := context.WithCancel(ctx)
	dm.cancelFns[job.ID] = cancel
	dm.mu.Unlock()

	// Check if file already exists
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

	// Create parent directories
	dir := filepath.Dir(job.DestPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		dm.mu.Lock()
		job.State = StateFailed
		job.Err = fmt.Errorf("create directory: %w", err)
		job.FinishedAt = time.Now()
		dm.active--
		delete(dm.cancelFns, job.ID)
		dm.mu.Unlock()
		cancel()
		return job, nil
	}

	var err error
	if dm.aria2cPath != "" {
		err = dm.downloadWithAria2c(jobCtx, job)
	} else {
		err = dm.downloadHTTP(jobCtx, job)
	}

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
	}
	job.FinishedAt = time.Now()
	dm.active--
	delete(dm.cancelFns, job.ID)
	dm.mu.Unlock()
	cancel()

	return job, nil
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
				if elapsed > 0 {
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

func (dm *DownloadManager) downloadWithAria2c(ctx context.Context, job *DownloadJob) error {
	tmpDir := filepath.Dir(job.DestPath)
	tmpName := filepath.Base(job.DestPath)

	args := []string{
		"-x", "16",
		"-s", "16",
		"-k", "1M",
		"--dir", tmpDir,
		"--out", tmpName,
		"--console-log-level=error",
		"--summary-interval=1",
		"--download-result=hide",
		job.URL,
	}

	cmd := exec.CommandContext(ctx, dm.aria2cPath, args...)
	cmd.Stdout = nil
	cmd.Stderr = nil

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("aria2c start: %w", err)
	}

	// Poll file size for progress
	go func() {
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		var lastSize int64
		var lastTime time.Time
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				info, err := os.Stat(filepath.Join(tmpDir, tmpName))
				if err != nil {
					// aria2c may use .aria2 suffix during download
					info, err = os.Stat(filepath.Join(tmpDir, tmpName+".aria2"))
					if err != nil {
						continue
					}
				}
				now := time.Now()
				size := info.Size()
				dm.mu.Lock()
				job.BytesRead = size
				if !lastTime.IsZero() {
					elapsed := now.Sub(lastTime).Seconds()
					if elapsed > 0 {
						job.Speed = int64(float64(size-lastSize) / elapsed)
					}
				}
				dm.mu.Unlock()
				lastSize = size
				lastTime = now
			}
		}
	}()

	return cmd.Wait()
}

// --- Utility functions ---

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
	return strings.TrimSpace(b.String())
}

// PlexMoviePath returns: <base>/Movies/<Name> (<Year>)/<Name> (<Year>).<ext>
func PlexMoviePath(base, name, year, ext string) string {
	folder := SanitizePath(name)
	if year != "" {
		folder = fmt.Sprintf("%s (%s)", SanitizePath(name), year)
	}
	filename := folder + ext
	return filepath.Join(base, "Movies", folder, filename)
}

// PlexEpisodePath returns: <base>/TV Shows/<Show> (<Year>)/Season XX/<Show> - SXXEXX - Title.<ext>
func PlexEpisodePath(base, showName, year string, season, episode int, epTitle, ext string) string {
	show := SanitizePath(showName)
	if year != "" {
		show = fmt.Sprintf("%s (%s)", SanitizePath(showName), year)
	}
	seasonDir := fmt.Sprintf("Season %02d", season)
	filename := fmt.Sprintf("%s - S%02dE%02d", show, season, episode)
	if epTitle != "" {
		filename += " - " + SanitizePath(epTitle)
	}
	filename += ext
	return filepath.Join(base, "TV Shows", show, seasonDir, filename)
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
