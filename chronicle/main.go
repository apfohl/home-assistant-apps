// Chronicle — a scheduled IMAP backup app for Home Assistant.
//
// Configuration is read from /data/options.json (the standard Home Assistant
// app options file). For each configured IMAP account the application:
//
//  1. Generates an imap-backup config.json in a temporary location.
//  2. Runs `imap-backup backup` on the configured cron schedule.
//  3. After each successful sync, creates a compressed tar.gz archive of the
//     account's local mail store into the configured archive path.
//  4. Prunes archives older than retention_days.
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
//	chronicle [-options /data/options.json]
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/robfig/cron/v3"
)

// ---------------------------------------------------------------------------
// Options model — mirrors /data/options.json produced by HA Supervisor.
// ---------------------------------------------------------------------------

// options is the top-level structure of the Home Assistant options file.
type options struct {
	CronExpression string       `json:"cron_expression"`
	BackupBasePath string       `json:"backup_base_path"`
	ArchivePath    string       `json:"archive_path"`
	RetentionDays  int          `json:"retention_days"`
	Accounts       []accountOpt `json:"accounts"`
}

// accountOpt holds the configuration for a single IMAP account.
type accountOpt struct {
	Username       string `json:"username"`
	Password       string `json:"password"`
	Server         string `json:"server"`
	MirrorMode     bool   `json:"mirror_mode"`
	MultiFetchSize int    `json:"multi_fetch_size"`
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
// Options loading
// ---------------------------------------------------------------------------

func loadOptions(path string) (*options, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open options file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("close options file: %v", closeErr)
		}
	}()

	var o options
	if err = json.NewDecoder(f).Decode(&o); err != nil {
		return nil, fmt.Errorf("decode options file: %w", err)
	}

	// Apply defaults.
	if o.CronExpression == "" {
		o.CronExpression = "0 3 * * *"
	}
	if o.BackupBasePath == "" {
		o.BackupBasePath = "/share/chronicle/data"
	}
	if o.ArchivePath == "" {
		o.ArchivePath = "/share/chronicle/archives"
	}
	if o.RetentionDays == 0 {
		o.RetentionDays = 5
	}
	for i := range o.Accounts {
		if o.Accounts[i].MultiFetchSize == 0 {
			o.Accounts[i].MultiFetchSize = 1
		}
	}

	return &o, nil
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
func runImapBackup(acc accountOpt, configPath string) error {
	cmd := exec.Command("imap-backup", "backup", "--config", configPath)
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
func backupAccount(acc accountOpt, opts *options) {
	syncPath := localPath(opts.BackupBasePath, acc.Username)

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
	if err = runImapBackup(acc, cfgPath); err != nil {
		log.Printf("[%s] imap-backup failed: %v — skipping archive", acc.Username, err)
		return
	}

	// Ensure archive directory exists.
	if err = os.MkdirAll(opts.ArchivePath, 0o755); err != nil {
		log.Printf("[%s] create archive dir %s: %v — skipping archive", acc.Username, opts.ArchivePath, err)
		return
	}

	// Create the tar.gz archive.
	if err = archiveAccount(acc, syncPath, opts.ArchivePath); err != nil {
		log.Printf("[%s] archive failed: %v", acc.Username, err)
		// Continue to pruning even if archiving failed.
	}

	// Prune old archives.
	pruneArchives(acc, opts.ArchivePath, opts.RetentionDays)

	log.Printf("[%s] backup job completed", acc.Username)
}

// ---------------------------------------------------------------------------
// Scheduler
// ---------------------------------------------------------------------------

// runAllAccounts runs the backup job for every configured account sequentially.
func runAllAccounts(opts *options) {
	log.Printf("scheduled backup run started for %d account(s)", len(opts.Accounts))

	for _, acc := range opts.Accounts {
		backupAccount(acc, opts)
	}

	log.Printf("scheduled backup run finished")
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func main() {
	optionsPath := flag.String("options", "/data/options.json", "path to the HA options JSON file")
	flag.Parse()

	opts, err := loadOptions(*optionsPath)
	if err != nil {
		log.Fatalf("load options: %v", err)
	}

	if len(opts.Accounts) == 0 {
		log.Fatal("no accounts configured; add at least one account in the app options")
	}

	log.Printf("chronicle starting with %d account(s), schedule: %q, retention: %d days",
		len(opts.Accounts), opts.CronExpression, opts.RetentionDays)
	log.Printf("backup base path : %s", opts.BackupBasePath)
	log.Printf("archive path     : %s", opts.ArchivePath)

	for _, acc := range opts.Accounts {
		log.Printf("  account: %s @ %s -> %s",
			acc.Username, acc.Server, localPath(opts.BackupBasePath, acc.Username))
	}

	// Set up the cron scheduler.
	c := cron.New()

	_, err = c.AddFunc(opts.CronExpression, func() {
		runAllAccounts(opts)
	})
	if err != nil {
		log.Fatalf("invalid cron expression %q: %v", opts.CronExpression, err)
	}

	c.Start()
	log.Printf("scheduler started; next run at %v", nextRun(c))

	// Block forever — the s6-overlay finish script handles restarts.
	select {}
}

// nextRun returns the next scheduled time from the cron scheduler, or a zero
// time if no entries are scheduled.
func nextRun(c *cron.Cron) time.Time {
	entries := c.Entries()
	if len(entries) == 0 {
		return time.Time{}
	}

	return entries[0].Next
}
