// Chronicle — a scheduled IMAP backup app for Home Assistant.
//
// Configuration is read from /data/settings.json (the Chronicle-managed
// settings file). If the file does not exist on first start, hardcoded
// defaults are written and the HTTP server starts immediately so the user
// can navigate to /settings to add accounts.
//
// For each configured IMAP account the application:
//
//  1. Generates an imap-backup config.json in a temporary location.
//  2. Runs `imap-backup backup` on the configured cron schedule.
//  3. After each successful sync, creates a compressed tar.gz archive of the
//     account's local mail store into the configured archive path.
//  4. Prunes archives older than retention_days.
//
// The application also exposes an ingress-enabled HTTP dashboard on :8080
// that lists all archives grouped by account, with download and delete actions,
// and a /settings page for managing all configuration without restarting.
//
// The local_path for each account is auto-derived as:
//
//	<backup_base_path>/<sanitized_username>
//
// where sanitized_username replaces '@' with '_at_' and any character that
// is not a letter, digit, hyphen, or underscore with '_'.
//
// Usage:
//
//	chronicle [-settings /data/settings.json]
package main

import (
	"archive/tar"
	"compress/gzip"
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/robfig/cron/v3"
)

//go:embed dashboard.html
var dashboardHTML string

//go:embed settings.html
var settingsHTML string

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	ingressIP  = "172.30.32.2"
	listenAddr = ":8080"
	version    = "1.5.0"
)

// ---------------------------------------------------------------------------
// Settings model — the Chronicle-managed settings file at /data/settings.json.
// This is the runtime source of truth.
// ---------------------------------------------------------------------------

// accountOpt holds the configuration for a single IMAP account.
type accountOpt struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Server         string `json:"server"`
	MirrorMode     bool   `json:"mirror_mode"`
	MultiFetchSize int    `json:"multi_fetch_size"`
}

// This is the runtime source of truth from Phase 1 onwards.
// ---------------------------------------------------------------------------

// settings mirrors the accountOpt struct and is owned and written by Chronicle
// itself (via the dashboard settings page), not by the HA Supervisor.
type settings struct {
	CronExpression string       `json:"cron_expression"`
	BackupBasePath string       `json:"backup_base_path"`
	ArchivePath    string       `json:"archive_path"`
	RetentionDays  int          `json:"retention_days"`
	Verbose        bool         `json:"verbose"`
	Accounts       []accountOpt `json:"accounts"`
}

// ---------------------------------------------------------------------------
// App state — mutable runtime state protected by a RWMutex for hot-reload.
// ---------------------------------------------------------------------------

// appState holds all mutable runtime state. HTTP handlers read from it under
// RLock; the settings-save handler replaces settings and reschedules the cron
// job under a full Lock.
type appState struct {
	mu       sync.RWMutex
	settings *settings
	cron     *cron.Cron
	cronID   cron.EntryID
	ctx      context.Context // background context (cancelled on shutdown)
}

// ---------------------------------------------------------------------------
// imap-backup config model — written to a temp file per account.
// ---------------------------------------------------------------------------

// imapBackupConfig is the format expected by the imap-backup Ruby gem.
type imapBackupConfig struct {
	Version          string        `json:"version"`
	Accounts         []imapAccount `json:"accounts"`
	DownloadStrategy string        `json:"download_strategy"`
}

// imapAccount represents one account entry in the imap-backup config.
type imapAccount struct {
	Username       string   `json:"username"`
	Password       string   `json:"password"`
	LocalPath      string   `json:"local_path"`
	Folders        []string `json:"folders"`
	MirrorMode     bool     `json:"mirror_mode"`
	Server         string   `json:"server"`
	MultiFetchSize int      `json:"multi_fetch_size"`
}

// ---------------------------------------------------------------------------
// Dashboard data model
// ---------------------------------------------------------------------------

// archiveEntry describes a single .tar.gz backup file on disk.
type archiveEntry struct {
	Filename     string    // e.g. chronicle-user_at_example_com-2026-01-15T03-00-01.tar.gz
	ModTime      time.Time // file modification time
	SizeBytes    int64
	SizeHuman    string // human-readable size, e.g. "12.3 MB"
	ModTimeHuman string // human-readable date, e.g. "2026-01-15 03:00"
}

// userBackups groups archives belonging to one account.
type userBackups struct {
	DisplayName string         // sanitized username shown as card title
	Archives    []archiveEntry // sorted newest-first
}

// pageData is passed into the dashboard HTML template.
type pageData struct {
	IngressPath string
	Users       []userBackups
	FetchError  string
	Timestamp   string
	Version     string
}

// settingsPageData is passed into the settings HTML template.
type settingsPageData struct {
	IngressPath string
	Settings    *settings
	SaveError   string
	SaveSuccess bool
	NextRun     string
	Version     string
}

// ---------------------------------------------------------------------------
// Settings persistence
// ---------------------------------------------------------------------------

// applySettingsDefaults fills zero-value fields with sensible defaults.
func applySettingsDefaults(s *settings) {
	if s.CronExpression == "" {
		s.CronExpression = "0 3 * * *"
	}
	if s.BackupBasePath == "" {
		s.BackupBasePath = "/share/chronicle/data"
	}
	if s.ArchivePath == "" {
		s.ArchivePath = "/share/chronicle/archives"
	}
	if s.RetentionDays == 0 {
		s.RetentionDays = 5
	}
	for i := range s.Accounts {
		if s.Accounts[i].MultiFetchSize == 0 {
			s.Accounts[i].MultiFetchSize = 1
		}
	}
}

// loadSettings loads /data/settings.json. If the file does not exist it
// writes hardcoded defaults to settingsPath and returns them.
func loadSettings(settingsPath string) (*settings, error) {
	f, err := os.Open(settingsPath)
	if err == nil {
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("close settings file: %v", closeErr)
			}
		}()
		var s settings
		if decErr := json.NewDecoder(f).Decode(&s); decErr != nil {
			return nil, fmt.Errorf("decode settings file: %w", decErr)
		}
		applySettingsDefaults(&s)
		log.Printf("settings: loaded from %s", settingsPath)
		return &s, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open settings file: %w", err)
	}

	log.Printf("settings: %s not found, writing hardcoded defaults", settingsPath)

	s := &settings{}
	applySettingsDefaults(s)

	if writeErr := saveSettings(settingsPath, s); writeErr != nil {
		log.Printf("settings: write defaults: %v", writeErr)
	}

	return s, nil
}

// saveSettings atomically writes s to path using a temp-file + rename so
// that a crash cannot leave a partial file.
func saveSettings(path string, s *settings) error {
	tmp := path + ".tmp"

	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return fmt.Errorf("create temp settings file: %w", err)
	}

	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if encErr := enc.Encode(s); encErr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("encode settings: %w", encErr)
	}

	if syncErr := f.Sync(); syncErr != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("fsync settings: %w", syncErr)
	}

	if closeErr := f.Close(); closeErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close temp settings file: %w", closeErr)
	}

	if renameErr := os.Rename(tmp, path); renameErr != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename settings file: %w", renameErr)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Hot-reload
// ---------------------------------------------------------------------------

// applySettings replaces the current settings, persists them to disk, and
// reschedules the cron job — all under the write lock. Any in-flight backup
// runs continue uninterrupted because they hold a snapshot of the old *settings
// captured at job dispatch time.
func (a *appState) applySettings(s *settings, settingsPath string) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := saveSettings(settingsPath, s); err != nil {
		return err
	}

	// Remove the old cron entry and add a new one with the (possibly
	// updated) cron expression. The closure captures ctx by value so it
	// remains valid even if ctx is later replaced.
	ctx := a.ctx
	a.cron.Remove(a.cronID)
	id, err := a.cron.AddFunc(s.CronExpression, func() {
		runAllAccounts(ctx, s)
	})
	if err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", s.CronExpression, err)
	}

	a.cronID = id
	a.settings = s

	log.Printf("settings: applied — schedule: %q, retention: %d days, %d account(s)",
		s.CronExpression, s.RetentionDays, len(s.Accounts))
	return nil
}

// ---------------------------------------------------------------------------
// Path helpers
// ---------------------------------------------------------------------------

var nonSafeChars = regexp.MustCompile(`[^a-zA-Z0-9\-_]`)

// sanitizeUsername converts an email address (or any string) into a
// filesystem-safe path component. '@' becomes '_at_'; everything else
// that is not alphanumeric, a hyphen, or an underscore becomes '_'.
func sanitizeUsername(username string) string {
	s := strings.ReplaceAll(username, "@", "_at_")
	return nonSafeChars.ReplaceAllString(s, "_")
}

// localPath returns the directory where imap-backup will store mail for
// a given account.
func localPath(basePath, username string) string {
	return filepath.Join(basePath, sanitizeUsername(username))
}

// ---------------------------------------------------------------------------
// imap-backup config file generation
// ---------------------------------------------------------------------------

// writeImapConfig writes a per-account imap-backup config.json to a
// temporary file and returns its path. The caller is responsible for
// removing the file when done.
func writeImapConfig(acc accountOpt, syncPath string) (string, error) {
	cfg := imapBackupConfig{
		Version: "2.1",
		Accounts: []imapAccount{
			{
				Username:       acc.Username,
				Password:       acc.Password,
				LocalPath:      syncPath,
				Folders:        []string{},
				MirrorMode:     acc.MirrorMode,
				Server:         acc.Server,
				MultiFetchSize: acc.MultiFetchSize,
			},
		},
		DownloadStrategy: "delay_metadata",
	}

	f, err := os.CreateTemp("", "chronicle-config-*.json")
	if err != nil {
		return "", fmt.Errorf("create temp config: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("close temp config: %v", closeErr)
		}
	}()

	if err = json.NewEncoder(f).Encode(cfg); err != nil {
		_ = os.Remove(f.Name())
		return "", fmt.Errorf("encode imap config: %w", err)
	}

	return f.Name(), nil
}

// ---------------------------------------------------------------------------
// Backup execution
// ---------------------------------------------------------------------------

// runImapBackup invokes the imap-backup CLI for a single account.
// It streams stdout/stderr to the Go logger, so the output appears in HA logs.
func runImapBackup(ctx context.Context, acc accountOpt, configPath string, verbose bool) error {
	args := []string{"backup", "--config", configPath}
	if verbose {
		args = append(args, "-v")
	}

	cmd := exec.CommandContext(ctx, "imap-backup", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	log.Printf("[%s] running: imap-backup backup --config %s", acc.Username, configPath)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("imap-backup exited: %w", err)
	}

	return nil
}

// ---------------------------------------------------------------------------
// Archiving (pure Go — no external tar binary)
// ---------------------------------------------------------------------------

// createArchive creates a compressed tar.gz of srcDir and writes it to
// destFile. srcDir must exist; destFile's parent directory must exist.
func createArchive(srcDir, destFile string) error {
	out, err := os.Create(destFile)
	if err != nil {
		return fmt.Errorf("create archive file: %w", err)
	}
	defer func() {
		if closeErr := out.Close(); closeErr != nil {
			log.Printf("close archive file: %v", closeErr)
		}
	}()

	gz := gzip.NewWriter(out)
	defer func() {
		if closeErr := gz.Close(); closeErr != nil {
			log.Printf("close gzip writer: %v", closeErr)
		}
	}()

	tw := tar.NewWriter(gz)
	defer func() {
		if closeErr := tw.Close(); closeErr != nil {
			log.Printf("close tar writer: %v", closeErr)
		}
	}()

	return filepath.Walk(srcDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Build the path relative to the parent of srcDir so that the
		// archive contains the top-level directory itself.
		relPath, err := filepath.Rel(filepath.Dir(srcDir), path)
		if err != nil {
			return fmt.Errorf("compute relative path for %s: %w", path, err)
		}

		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return fmt.Errorf("tar header for %s: %w", path, err)
		}
		hdr.Name = relPath
		if info.IsDir() {
			hdr.Name += "/"
		}

		if err = tw.WriteHeader(hdr); err != nil {
			return fmt.Errorf("write tar header for %s: %w", relPath, err)
		}

		if info.IsDir() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return fmt.Errorf("open file %s: %w", path, err)
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("close archive source %s: %v", path, closeErr)
			}
		}()

		if _, err = io.Copy(tw, f); err != nil {
			return fmt.Errorf("copy %s into archive: %w", path, err)
		}

		return nil
	})
}

// archiveFilename returns a timestamped archive filename for the given account.
func archiveFilename(sanitized string, t time.Time) string {
	return fmt.Sprintf("chronicle-%s-%s.tar.gz", sanitized, t.Format("2006-01-02T15-04-05"))
}

// archiveAccount archives the sync directory for a single account.
func archiveAccount(acc accountOpt, syncPath, archivePath string) error {
	sanitized := sanitizeUsername(acc.Username)
	dest := filepath.Join(archivePath, archiveFilename(sanitized, time.Now()))

	log.Printf("[%s] creating archive: %s", acc.Username, dest)

	if err := createArchive(syncPath, dest); err != nil {
		return fmt.Errorf("create archive for %s: %w", acc.Username, err)
	}

	log.Printf("[%s] archive created: %s", acc.Username, dest)
	return nil
}

// ---------------------------------------------------------------------------
// Archive pruning
// ---------------------------------------------------------------------------

// pruneArchives removes archives for the given account that are older than
// retentionDays days.
func pruneArchives(acc accountOpt, archivePath string, retentionDays int) {
	sanitized := sanitizeUsername(acc.Username)
	prefix := fmt.Sprintf("chronicle-%s-", sanitized)
	cutoff := time.Now().AddDate(0, 0, -retentionDays)

	entries, err := os.ReadDir(archivePath)
	if err != nil {
		log.Printf("[%s] prune: read archive dir %s: %v", acc.Username, archivePath, err)
		return
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}

		var info fs.FileInfo
		info, err = e.Info()
		if err != nil {
			log.Printf("[%s] prune: stat %s: %v", acc.Username, name, err)
			continue
		}

		if info.ModTime().Before(cutoff) {
			full := filepath.Join(archivePath, name)
			if err = os.Remove(full); err != nil {
				log.Printf("[%s] prune: remove %s: %v", acc.Username, full, err)
			} else {
				log.Printf("[%s] prune: removed old archive %s", acc.Username, name)
			}
		}
	}
}

// ---------------------------------------------------------------------------
// Per-account backup job
// ---------------------------------------------------------------------------

// backupAccount runs the full backup pipeline for a single account:
// sync → archive → prune. Errors are logged but do not halt the scheduler.
func backupAccount(ctx context.Context, acc accountOpt, s *settings) {
	syncPath := localPath(s.BackupBasePath, acc.Username)

	log.Printf("[%s] backup job started", acc.Username)

	// Ensure the local sync directory exists.
	if err := os.MkdirAll(syncPath, 0o755); err != nil {
		log.Printf("[%s] create sync dir %s: %v — skipping account", acc.Username, syncPath, err)
		return
	}

	// Write per-account imap-backup config.
	cfgPath, err := writeImapConfig(acc, syncPath)
	if err != nil {
		log.Printf("[%s] write imap config: %v — skipping account", acc.Username, err)
		return
	}
	defer func() {
		if err = os.Remove(cfgPath); err != nil && !os.IsNotExist(err) {
			log.Printf("[%s] remove temp config %s: %v", acc.Username, cfgPath, err)
		}
	}()

	// Run imap-backup.
	if err = runImapBackup(ctx, acc, cfgPath, s.Verbose); err != nil {
		log.Printf("[%s] imap-backup failed: %v — skipping archive", acc.Username, err)
		return
	}

	// Ensure archive directory exists.
	if err = os.MkdirAll(s.ArchivePath, 0o755); err != nil {
		log.Printf("[%s] create archive dir %s: %v — skipping archive", acc.Username, s.ArchivePath, err)
		return
	}

	// Create the tar.gz archive.
	if err = archiveAccount(acc, syncPath, s.ArchivePath); err != nil {
		log.Printf("[%s] archive failed: %v", acc.Username, err)
		// Continue to pruning even if archiving failed.
	}

	// Prune old archives.
	pruneArchives(acc, s.ArchivePath, s.RetentionDays)

	log.Printf("[%s] backup job completed", acc.Username)
}

// ---------------------------------------------------------------------------
// Scheduler
// ---------------------------------------------------------------------------

// runAllAccounts runs the backup job for every configured account sequentially.
func runAllAccounts(ctx context.Context, s *settings) {
	log.Printf("scheduled backup run started for %d account(s)", len(s.Accounts))

	for _, acc := range s.Accounts {
		backupAccount(ctx, acc, s)
	}

	log.Printf("scheduled backup run finished")
}

// ---------------------------------------------------------------------------
// Dashboard helpers
// ---------------------------------------------------------------------------

// formatSize converts bytes to a human-readable string (B / KB / MB / GB).
func formatSize(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// isValidArchiveFilename returns true if name is a safe, well-formed archive
// filename — no path separators, must match the chronicle-*-*.tar.gz pattern.
var archiveFilePattern = regexp.MustCompile(`^chronicle-[a-zA-Z0-9_\-]+-\d{4}-\d{2}-\d{2}T\d{2}-\d{2}-\d{2}\.tar\.gz$`)

func isValidArchiveFilename(name string) bool {
	return archiveFilePattern.MatchString(name) &&
		!strings.Contains(name, "/") &&
		!strings.Contains(name, "\\") &&
		!strings.Contains(name, "..")
}

// scanArchives reads the archive directory and returns archives grouped by
// sanitized username, each group sorted newest-first.
func scanArchives(archivePath string, accounts []accountOpt) ([]userBackups, error) {
	entries, err := os.ReadDir(archivePath)
	if err != nil {
		if os.IsNotExist(err) {
			// Archive dir not yet created — return empty groups per account.
			return buildEmptyGroups(accounts), nil
		}
		return nil, fmt.Errorf("read archive dir: %w", err)
	}

	// Build a map: sanitized username → []archiveEntry
	byUser := make(map[string][]archiveEntry)

	// Seed the map with all configured accounts (even those with no archives yet).
	for _, acc := range accounts {
		san := sanitizeUsername(acc.Username)
		if _, ok := byUser[san]; !ok {
			byUser[san] = nil
		}
	}

	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if !strings.HasPrefix(name, "chronicle-") || !strings.HasSuffix(name, ".tar.gz") {
			continue
		}

		// Extract the sanitized username from the filename.
		// Format: chronicle-<sanitized>-<YYYY-MM-DDThh-mm-ss>.tar.gz
		// The timestamp part is always 19 chars: YYYY-MM-DDThh-mm-ss
		// plus ".tar.gz" = 7 chars, plus the leading dash = 20 chars from the end.
		const suffixLen = len("-2006-01-02T15-04-05.tar.gz") // 27
		trimmed := strings.TrimPrefix(name, "chronicle-")    // <sanitized>-<timestamp>.tar.gz
		if len(trimmed) <= suffixLen {
			continue
		}
		san := trimmed[:len(trimmed)-suffixLen]

		// Only include archives belonging to a configured account.
		if _, ok := byUser[san]; !ok {
			continue
		}

		info, err := e.Info()
		if err != nil {
			log.Printf("dashboard: stat %s: %v", name, err)
			continue
		}

		byUser[san] = append(byUser[san], archiveEntry{
			Filename:     name,
			ModTime:      info.ModTime(),
			SizeBytes:    info.Size(),
			SizeHuman:    formatSize(info.Size()),
			ModTimeHuman: info.ModTime().UTC().Format("2006-01-02 15:04 UTC"),
		})
	}

	// Sort each user's archives newest-first, then build the result slice
	// ordered by the configured account order.
	result := make([]userBackups, 0, len(accounts))
	seen := make(map[string]bool)
	for _, acc := range accounts {
		san := sanitizeUsername(acc.Username)
		if seen[san] {
			continue
		}
		seen[san] = true
		archives := byUser[san]
		sort.Slice(archives, func(i, j int) bool {
			return archives[i].ModTime.After(archives[j].ModTime)
		})
		result = append(result, userBackups{
			DisplayName: acc.Username,
			Archives:    archives,
		})
	}

	return result, nil
}

// buildEmptyGroups returns a slice of userBackups with no archives.
func buildEmptyGroups(accounts []accountOpt) []userBackups {
	result := make([]userBackups, 0, len(accounts))
	seen := make(map[string]bool)
	for _, acc := range accounts {
		san := sanitizeUsername(acc.Username)
		if seen[san] {
			continue
		}
		seen[san] = true
		result = append(result, userBackups{DisplayName: acc.Username})
	}
	return result
}

// ---------------------------------------------------------------------------
// HTTP handlers
// ---------------------------------------------------------------------------

// enforceIngress rejects any request that did not originate from the HA
// ingress gateway IP. Returns false if the request was rejected.
func enforceIngress(w http.ResponseWriter, r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host != ingressIP {
		http.Error(w, "forbidden", http.StatusForbidden)
		return false
	}
	return true
}

// handleDashboard serves the main dashboard page.
func handleDashboard(tmpl *template.Template, state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enforceIngress(w, r) {
			return
		}

		ingressPath := r.Header.Get("X-Ingress-Path")

		state.mu.RLock()
		s := state.settings
		state.mu.RUnlock()

		data := pageData{
			IngressPath: ingressPath,
			Timestamp:   time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
			Version:     version,
		}

		users, err := scanArchives(s.ArchivePath, s.Accounts)
		if err != nil {
			data.FetchError = fmt.Sprintf("Failed to scan archive directory: %v", err)
		} else {
			data.Users = users
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err = tmpl.Execute(w, data); err != nil {
			log.Printf("dashboard template error: %v", err)
		}
	}
}

// handleDownload streams an archive file as an attachment download.
func handleDownload(state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enforceIngress(w, r) {
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		filename := r.URL.Query().Get("file")
		if !isValidArchiveFilename(filename) {
			http.Error(w, "invalid filename", http.StatusBadRequest)
			return
		}

		state.mu.RLock()
		archivePath := state.settings.ArchivePath
		state.mu.RUnlock()

		fullPath := filepath.Join(archivePath, filename)
		f, err := os.Open(fullPath)
		if err != nil {
			if os.IsNotExist(err) {
				http.Error(w, "not found", http.StatusNotFound)
			} else {
				http.Error(w, "internal error", http.StatusInternalServerError)
			}
			return
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil {
				log.Printf("download: close %s: %v", filename, closeErr)
			}
		}()

		info, err := f.Stat()
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		w.Header().Set("Content-Length", fmt.Sprintf("%d", info.Size()))
		w.Header().Set("Cache-Control", "no-store")

		if _, err = io.Copy(w, f); err != nil {
			log.Printf("download: stream %s: %v", filename, err)
		}
	}
}

// handleDelete removes an archive file and redirects back to the dashboard.
func handleDelete(state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enforceIngress(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}

		filename := r.FormValue("file")
		if !isValidArchiveFilename(filename) {
			http.Error(w, "invalid filename", http.StatusBadRequest)
			return
		}

		state.mu.RLock()
		archivePath := state.settings.ArchivePath
		state.mu.RUnlock()

		fullPath := filepath.Join(archivePath, filename)
		if err := os.Remove(fullPath); err != nil && !os.IsNotExist(err) {
			log.Printf("delete: remove %s: %v", filename, err)
			http.Error(w, "failed to delete file", http.StatusInternalServerError)
			return
		}

		log.Printf("dashboard: deleted archive %s", filename)

		// Post/Redirect/Get — redirect back to the dashboard root.
		ingressPath := r.Header.Get("X-Ingress-Path")
		http.Redirect(w, r, ingressPath+"/", http.StatusSeeOther)
	}
}

// handleSettings serves the settings page (GET) and saves new settings (POST).
func handleSettings(tmpl *template.Template, state *appState, settingsPath string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !enforceIngress(w, r) {
			return
		}

		ingressPath := r.Header.Get("X-Ingress-Path")

		switch r.Method {
		case http.MethodGet:
			state.mu.RLock()
			s := state.settings
			nextRun := nextRunFromState(state)
			state.mu.RUnlock()

			renderSettings(w, tmpl, settingsPageData{
				IngressPath: ingressPath,
				Settings:    s,
				NextRun:     nextRun,
				Version:     version,
			})

		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			s, validationErr := parseSettingsForm(r)
			if validationErr != "" {
				state.mu.RLock()
				current := state.settings
				nextRun := nextRunFromState(state)
				state.mu.RUnlock()

				// Preserve the submitted form values so the user sees their input.
				renderSettings(w, tmpl, settingsPageData{
					IngressPath: ingressPath,
					Settings:    s,
					SaveError:   validationErr,
					NextRun:     nextRun,
					Version:     version,
				})
				_ = current // current settings available if needed
				return
			}

			if err := state.applySettings(s, settingsPath); err != nil {
				state.mu.RLock()
				nextRun := nextRunFromState(state)
				state.mu.RUnlock()

				renderSettings(w, tmpl, settingsPageData{
					IngressPath: ingressPath,
					Settings:    s,
					SaveError:   fmt.Sprintf("Failed to save settings: %v", err),
					NextRun:     nextRun,
					Version:     version,
				})
				return
			}

			// PRG — redirect back to settings with success indicator.
			http.Redirect(w, r, ingressPath+"/settings?saved=1", http.StatusSeeOther)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// renderSettings executes the settings template, writing to w.
func renderSettings(w http.ResponseWriter, tmpl *template.Template, data settingsPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("settings template error: %v", err)
	}
}

// nextRunFromState returns a human-readable string for the next scheduled run.
// Must be called with at least a read lock held on state.
func nextRunFromState(state *appState) string {
	entries := state.cron.Entries()
	for _, e := range entries {
		if e.ID == state.cronID {
			return e.Next.UTC().Format("2006-01-02 15:04:05 UTC")
		}
	}
	return "—"
}

// parseSettingsForm reads and validates an http.Request form, returning a
// populated *settings and a non-empty error string on validation failure.
func parseSettingsForm(r *http.Request) (*settings, string) {
	s := &settings{}

	s.CronExpression = strings.TrimSpace(r.FormValue("cron_expression"))
	if s.CronExpression == "" {
		return s, "Cron expression must not be empty."
	}
	// Validate cron expression using the same parser as the scheduler.
	if _, err := cron.ParseStandard(s.CronExpression); err != nil {
		return s, fmt.Sprintf("Invalid cron expression: %v", err)
	}

	s.BackupBasePath = strings.TrimSpace(r.FormValue("backup_base_path"))
	if s.BackupBasePath == "" {
		return s, "Backup base path must not be empty."
	}

	s.ArchivePath = strings.TrimSpace(r.FormValue("archive_path"))
	if s.ArchivePath == "" {
		return s, "Archive path must not be empty."
	}

	retStr := strings.TrimSpace(r.FormValue("retention_days"))
	ret, err := strconv.Atoi(retStr)
	if err != nil || ret <= 0 {
		return s, "Retention days must be a positive integer."
	}
	s.RetentionDays = ret

	s.Verbose = r.FormValue("verbose") == "true"

	// Accounts: the form sends parallel arrays:
	//   account_username[], account_password[], account_server[],
	//   account_mirror_mode[], account_multi_fetch_size[]
	usernames := r.Form["account_username"]
	passwords := r.Form["account_password"]
	servers := r.Form["account_server"]
	mirrors := r.Form["account_mirror_mode"]
	fetchSizes := r.Form["account_multi_fetch_size"]

	if len(usernames) == 0 {
		return s, "At least one account must be configured."
	}

	for i, username := range usernames {
		username = strings.TrimSpace(username)
		if username == "" {
			return s, fmt.Sprintf("Account %d: username must not be empty.", i+1)
		}

		password := ""
		if i < len(passwords) {
			password = passwords[i]
		}

		server := ""
		if i < len(servers) {
			server = strings.TrimSpace(servers[i])
		}
		if server == "" {
			return s, fmt.Sprintf("Account %d (%s): server must not be empty.", i+1, username)
		}

		mirror := false
		if i < len(mirrors) {
			mirror = mirrors[i] == "true"
		}

		fetchSize := 1
		if i < len(fetchSizes) {
			if fs, fsErr := strconv.Atoi(fetchSizes[i]); fsErr == nil && fs > 0 {
				fetchSize = fs
			}
		}

		s.Accounts = append(s.Accounts, accountOpt{
			Username:       username,
			Password:       password,
			Server:         server,
			MirrorMode:     mirror,
			MultiFetchSize: fetchSize,
		})
	}

	return s, ""
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func main() {
	settingsPath := flag.String("settings", "/data/settings.json", "path to the Chronicle settings JSON file")
	flag.Parse()

	s, err := loadSettings(*settingsPath)
	if err != nil {
		log.Fatalf("load settings: %v", err)
	}

	log.Printf("chronicle v%s starting", version)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// ── Cron scheduler ──────────────────────────────────────────────────────

	c := cron.New()

	var cronID cron.EntryID

	if len(s.Accounts) == 0 {
		log.Printf("no accounts configured; open the Chronicle dashboard and add accounts in Settings")
	} else {
		log.Printf("%d account(s) configured, schedule: %q, retention: %d days",
			len(s.Accounts), s.CronExpression, s.RetentionDays)
		log.Printf("backup base path : %s", s.BackupBasePath)
		log.Printf("archive path     : %s", s.ArchivePath)

		for _, acc := range s.Accounts {
			log.Printf("  account: %s @ %s -> %s",
				acc.Username, acc.Server, localPath(s.BackupBasePath, acc.Username))
		}

		cronID, err = c.AddFunc(s.CronExpression, func() {
			state.mu.RLock()
			current := state.settings
			state.mu.RUnlock()
			runAllAccounts(ctx, current)
		})
		if err != nil {
			log.Fatalf("invalid cron expression %q: %v", s.CronExpression, err)
		}
	}

	c.Start()

	// ── App state ────────────────────────────────────────────────────────────

	state = &appState{
		settings: s,
		cron:     c,
		cronID:   cronID,
		ctx:      ctx,
	}

	if len(s.Accounts) > 0 {
		log.Printf("scheduler started; next run at %v", nextRun(c))
	}

	// ── HTTP dashboard ───────────────────────────────────────────────────────

	dashTmpl, err := template.New("dashboard").Funcs(template.FuncMap{
		"not": func(v interface{}) bool {
			if v == nil {
				return true
			}
			if s2, ok := v.([]userBackups); ok {
				return len(s2) == 0
			}
			return false
		},
	}).Parse(dashboardHTML)
	if err != nil {
		log.Fatalf("parse dashboard template: %v", err)
	}

	settingsTmpl, err := template.New("settings").Funcs(template.FuncMap{
		"inc": func(i int) int { return i + 1 },
	}).Parse(settingsHTML)
	if err != nil {
		log.Fatalf("parse settings template: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard(dashTmpl, state))
	mux.HandleFunc("/download", handleDownload(state))
	mux.HandleFunc("/delete", handleDelete(state))
	mux.HandleFunc("/settings", handleSettings(settingsTmpl, state, *settingsPath))

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	go func() {
		log.Printf("dashboard listening on %s (ingress-only from %s)", listenAddr, ingressIP)
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("dashboard ListenAndServe error: %v", err)
		}
	}()

	// ── Graceful shutdown ────────────────────────────────────────────────────

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("shutting down...")
	cancel() // cancel any in-progress imap-backup subprocess

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err = srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("dashboard shutdown error: %v", err)
	}

	<-c.Stop().Done()
	log.Println("chronicle stopped")
}

// state is the global app state, set in main() before the HTTP server starts.
// Using a package-level variable avoids threading it through every closure
// while keeping all mutation inside appState.applySettings.
var state *appState

// nextRun returns the next scheduled time from the cron scheduler, or a zero
// time if no entries are scheduled.
func nextRun(c *cron.Cron) time.Time {
	entries := c.Entries()
	if len(entries) == 0 {
		return time.Time{}
	}

	return entries[0].Next
}
