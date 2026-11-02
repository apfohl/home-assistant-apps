// Mailcow — a Home Assistant add-on for managing mailbox aliases on hosted mailcow.
//
// Configuration is read from /data/settings.json in production, or from
// ./dev-data/settings.json in development. The add-on exposes an
// ingress-enabled web dashboard on :8080 that lists aliases and allows
// creating and deleting them. It communicates with hosted mailcow through
// the built-in scraping client; no API access is used.
package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

//go:embed create.html
var createHTML string

//go:embed edit.html
var editHTML string

//go:embed settings.html
var settingsHTML string

type environment string

const (
	envDevelopment environment = "development"
	envProduction  environment = "production"
)

const (
	ingressIP       = "172.30.32.2"
	listenAddr      = ":8080"
	roleDomainAdmin = RoleDomainAdmin
	version         = "1.2.1"

	prodSettingsPath = "/data/settings.json"
	prodCookieFile   = "/data/mailcow-cookie-jar.json"
	devSettingsPath  = "./dev-data/settings.json"
	devCookieFile    = "./dev-data/mailcow-cookie-jar.json"
)

func parseEnvironment() (environment, error) {
	v := strings.TrimSpace(os.Getenv("ENVIRONMENT"))
	if v == "" {
		return envProduction, nil
	}
	switch strings.ToLower(v) {
	case "development":
		return envDevelopment, nil
	case "production":
		return envProduction, nil
	default:
		return "", fmt.Errorf("invalid ENVIRONMENT %q, expected development or production", v)
	}
}

func defaultSettingsPath(env environment) string {
	if env == envDevelopment {
		return devSettingsPath
	}
	return prodSettingsPath
}

func defaultCookieFile(env environment) string {
	if env == envDevelopment {
		return devCookieFile
	}
	return prodCookieFile
}

// settings holds the user-configurable credentials for hosted mailcow.
type settings struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// appState holds mutable runtime state.
type appState struct {
	mu       sync.RWMutex
	settings *settings
	client   *Client
	env      environment
}

// dashboardPageData is passed into the dashboard template.
type dashboardPageData struct {
	IngressPath string
	Aliases     []aliasRow
	FetchError  string
	Timestamp   string
	Version     string
}

// aliasRow represents one alias shown on the dashboard.
type aliasRow struct {
	Address string
	Goto    string
}

// createPageData is passed into the create-alias template.
type createPageData struct {
	IngressPath string
	Address     string
	Goto        string
	Error       string
	Version     string
}

// editPageData is passed into the edit-alias template.
type editPageData struct {
	IngressPath string
	Address     string
	Goto        string
	Error       string
	Version     string
}

// settingsPageData is passed into the settings template.
type settingsPageData struct {
	IngressPath string
	Username    string
	SaveError   string
	SaveSuccess bool
	Version     string
}

// ---------------------------------------------------------------------------
// Settings persistence
// ---------------------------------------------------------------------------

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
		log.Printf("settings: loaded from %s", settingsPath)
		return &s, nil
	}

	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("open settings file: %w", err)
	}

	log.Printf("settings: %s not found, writing defaults", settingsPath)
	s := &settings{}
	if writeErr := saveSettings(settingsPath, s); writeErr != nil {
		log.Printf("settings: write defaults: %v", writeErr)
	}
	return s, nil
}

func saveSettings(path string, s *settings) error {
	dir := filepath.Dir(path)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create settings directory: %w", err)
		}
	}

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

func (a *appState) applySettings(path, cookieFile string, s *settings) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if err := saveSettings(path, s); err != nil {
		return err
	}

	client, err := newMailcowClient(s, cookieFile)
	if err != nil {
		return fmt.Errorf("rebuild mailcow client: %w", err)
	}

	a.settings = s
	a.client = client
	return nil
}

func newMailcowClient(s *settings, cookieFile string) (*Client, error) {
	return NewClient(Config{
		Username:   s.Username,
		Password:   s.Password,
		Role:       roleDomainAdmin,
		CookieFile: cookieFile,
	})
}

// ---------------------------------------------------------------------------
// HTTP helpers
// ---------------------------------------------------------------------------

func (a *appState) enforceIngress(w http.ResponseWriter, r *http.Request) bool {
	if a.env == envDevelopment {
		return true
	}
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

func ingressPath(r *http.Request) string {
	return r.Header.Get("X-Ingress-Path")
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

func handleDashboard(tmpl *template.Template, state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !state.enforceIngress(w, r) {
			return
		}

		data := dashboardPageData{
			IngressPath: ingressPath(r),
			Timestamp:   time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
			Version:     version,
		}

		state.mu.RLock()
		client := state.client
		state.mu.RUnlock()

		if client == nil {
			data.FetchError = "Mailcow is not configured. Open Settings and enter your credentials."
		} else {
			aliases, err := client.ListAliases(r.Context())
			if err != nil {
				data.FetchError = fmt.Sprintf("Failed to list aliases: %v", err)
			} else {
				data.Aliases = make([]aliasRow, 0, len(aliases))
				for _, a := range aliases {
					data.Aliases = append(data.Aliases, aliasRow{Address: a.Address, Goto: a.Goto})
				}
			}
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err := tmpl.Execute(w, data); err != nil {
			log.Printf("dashboard template error: %v", err)
		}
	}
}

func handleCreate(tmpl *template.Template, state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !state.enforceIngress(w, r) {
			return
		}

		switch r.Method {
		case http.MethodGet:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Cache-Control", "no-store")
			if err := tmpl.Execute(w, createPageData{
				IngressPath: ingressPath(r),
				Version:     version,
			}); err != nil {
				log.Printf("create template error: %v", err)
			}

		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			address := strings.TrimSpace(r.FormValue("address"))
			gotoStr := strings.TrimSpace(r.FormValue("goto"))

			data := createPageData{
				IngressPath: ingressPath(r),
				Address:     address,
				Goto:        gotoStr,
				Version:     version,
			}

			if address == "" {
				data.Error = "Alias address must not be empty."
				renderCreate(w, tmpl, data)
				return
			}

			gotoAddrs := splitGotoAddresses(gotoStr)
			if len(gotoAddrs) == 0 {
				data.Error = "At least one goto address must be provided."
				renderCreate(w, tmpl, data)
				return
			}

			state.mu.RLock()
			client := state.client
			state.mu.RUnlock()

			if client == nil {
				data.Error = "Mailcow is not configured. Open Settings first."
				renderCreate(w, tmpl, data)
				return
			}

			if err := client.CreateAlias(r.Context(), []string{address}, gotoAddrs, true); err != nil {
				data.Error = fmt.Sprintf("Failed to create alias: %v", err)
				renderCreate(w, tmpl, data)
				return
			}

			http.Redirect(w, r, ingressPath(r)+"/", http.StatusSeeOther)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderCreate(w http.ResponseWriter, tmpl *template.Template, data createPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("create template error: %v", err)
	}
}

func handleEdit(tmpl *template.Template, state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !state.enforceIngress(w, r) {
			return
		}

		switch r.Method {
		case http.MethodGet:
			address := strings.TrimSpace(r.URL.Query().Get("address"))

			data := editPageData{
				IngressPath: ingressPath(r),
				Address:     address,
				Version:     version,
			}

			if address == "" {
				data.Error = "Alias address must not be empty."
				renderEdit(w, tmpl, data)
				return
			}

			state.mu.RLock()
			client := state.client
			state.mu.RUnlock()

			if client == nil {
				data.Error = "Mailcow is not configured. Open Settings first."
				renderEdit(w, tmpl, data)
				return
			}

			alias, err := client.GetAlias(r.Context(), address)
			if err != nil {
				data.Error = fmt.Sprintf("Failed to load alias: %v", err)
				renderEdit(w, tmpl, data)
				return
			}

			data.Goto = alias.Goto
			renderEdit(w, tmpl, data)

		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			address := strings.TrimSpace(r.FormValue("address"))
			gotoStr := strings.TrimSpace(r.FormValue("goto"))

			data := editPageData{
				IngressPath: ingressPath(r),
				Address:     address,
				Goto:        gotoStr,
				Version:     version,
			}

			if address == "" {
				data.Error = "Alias address must not be empty."
				renderEdit(w, tmpl, data)
				return
			}

			gotoAddrs := splitGotoAddresses(gotoStr)
			if len(gotoAddrs) == 0 {
				data.Error = "At least one goto address must be provided."
				renderEdit(w, tmpl, data)
				return
			}

			state.mu.RLock()
			client := state.client
			state.mu.RUnlock()

			if client == nil {
				data.Error = "Mailcow is not configured. Open Settings first."
				renderEdit(w, tmpl, data)
				return
			}

			if err := client.EditAlias(r.Context(), address, gotoAddrs); err != nil {
				data.Error = fmt.Sprintf("Failed to save alias: %v", err)
				renderEdit(w, tmpl, data)
				return
			}

			http.Redirect(w, r, ingressPath(r)+"/", http.StatusSeeOther)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderEdit(w http.ResponseWriter, tmpl *template.Template, data editPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("edit template error: %v", err)
	}
}

func splitGotoAddresses(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func handleDelete(state *appState) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !state.enforceIngress(w, r) {
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

		address := strings.TrimSpace(r.FormValue("address"))
		if address == "" {
			http.Error(w, "invalid address", http.StatusBadRequest)
			return
		}

		state.mu.RLock()
		client := state.client
		state.mu.RUnlock()

		if client == nil {
			http.Error(w, "mailcow not configured", http.StatusServiceUnavailable)
			return
		}

		if err := client.DeleteAlias(r.Context(), address); err != nil {
			log.Printf("delete alias %q: %v", address, err)
			http.Error(w, "failed to delete alias", http.StatusInternalServerError)
			return
		}

		http.Redirect(w, r, ingressPath(r)+"/", http.StatusSeeOther)
	}
}

func handleSettings(tmpl *template.Template, state *appState, settingsPath, cookieFile string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !state.enforceIngress(w, r) {
			return
		}

		switch r.Method {
		case http.MethodGet:
			state.mu.RLock()
			username := state.settings.Username
			state.mu.RUnlock()

			renderSettings(w, tmpl, settingsPageData{
				IngressPath: ingressPath(r),
				Username:    username,
				Version:     version,
			})

		case http.MethodPost:
			if err := r.ParseForm(); err != nil {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}

			username := strings.TrimSpace(r.FormValue("username"))
			password := r.FormValue("password")

			data := settingsPageData{
				IngressPath: ingressPath(r),
				Username:    username,
				Version:     version,
			}

			if username == "" {
				data.SaveError = "Username must not be empty."
				renderSettings(w, tmpl, data)
				return
			}
			if password == "" {
				data.SaveError = "Password must not be empty."
				renderSettings(w, tmpl, data)
				return
			}

			newSettings := &settings{
				Username: username,
				Password: password,
			}

			if err := state.applySettings(settingsPath, cookieFile, newSettings); err != nil {
				data.SaveError = fmt.Sprintf("Failed to save settings: %v", err)
				renderSettings(w, tmpl, data)
				return
			}

			http.Redirect(w, r, ingressPath(r)+"/settings?saved=1", http.StatusSeeOther)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

func renderSettings(w http.ResponseWriter, tmpl *template.Template, data settingsPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if err := tmpl.Execute(w, data); err != nil {
		log.Printf("settings template error: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func main() {
	env, err := parseEnvironment()
	if err != nil {
		log.Fatalf("environment: %v", err)
	}

	settingsPath := flag.String("settings", defaultSettingsPath(env), "path to the Mailcow settings JSON file")
	cookieFile := flag.String("cookie-file", defaultCookieFile(env), "path to the mailcow cookie jar file")
	flag.Parse()

	s, err := loadSettings(*settingsPath)
	if err != nil {
		log.Fatalf("load settings: %v", err)
	}

	log.Printf("mailcow v%s starting in %s mode", version, env)

	var client *Client
	if s.Username != "" && s.Password != "" {
		client, err = newMailcowClient(s, *cookieFile)
		if err != nil {
			log.Fatalf("create mailcow client: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if pingErr := client.Ping(ctx); pingErr != nil {
			log.Printf("mailcow: initial login check failed: %v", pingErr)
		} else {
			log.Printf("mailcow: login check succeeded")
		}
		cancel()
	} else {
		log.Printf("mailcow: no credentials configured; open /settings to add username and password")
	}

	state := &appState{
		settings: s,
		client:   client,
		env:      env,
	}

	dashTmpl, err := template.New("dashboard").Parse(dashboardHTML)
	if err != nil {
		log.Fatalf("parse dashboard template: %v", err)
	}

	createTmpl, err := template.New("create").Parse(createHTML)
	if err != nil {
		log.Fatalf("parse create template: %v", err)
	}

	editTmpl, err := template.New("edit").Parse(editHTML)
	if err != nil {
		log.Fatalf("parse edit template: %v", err)
	}

	settingsTmpl, err := template.New("settings").Parse(settingsHTML)
	if err != nil {
		log.Fatalf("parse settings template: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", handleDashboard(dashTmpl, state))
	mux.HandleFunc("/create", handleCreate(createTmpl, state))
	mux.HandleFunc("/edit", handleEdit(editTmpl, state))
	mux.HandleFunc("/delete", handleDelete(state))
	mux.HandleFunc("/settings", handleSettings(settingsTmpl, state, *settingsPath, *cookieFile))

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	go func() {
		if env == envDevelopment {
			log.Printf("dashboard listening on %s (development mode, no ingress enforcement)", listenAddr)
		} else {
			log.Printf("dashboard listening on %s (ingress-only from %s)", listenAddr, ingressIP)
		}
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("dashboard ListenAndServe error: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("shutting down...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err = srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("dashboard shutdown error: %v", err)
	}

	log.Println("mailcow stopped")
}
