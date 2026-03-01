// Demiplane — a WebDAV server for Home Assistant.
//
// Configuration is read from /data/options.json (the standard Home Assistant
// app options file). Multiple users and multiple directories with
// per-directory access-control lists are supported.
//
// Passwords are supplied as plaintext in the HA options UI; they are bcrypt-
// hashed in memory at startup and never written back to disk by this process.
//
// Usage:
//
//	demiplane [-options /data/options.json]
//	demiplane -hash-password <plaintext> # print a bcrypt hash and exit
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/net/webdav"
)

// ---------------------------------------------------------------------------
// Options model — mirrors /data/options.json produced by HA Supervisor.
// ---------------------------------------------------------------------------

// options is the top-level structure of the Home Assistant options file.
type options struct {
	ServerPort  int       `json:"server_port"`
	Users       []userOpt `json:"users"`
	Directories []dirOpt  `json:"directories"`
}

// userOpt holds a username and a plaintext password as entered in the HA UI.
// The password is hashed in memory at startup; it is never stored or logged.
type userOpt struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// dirOpt maps a URL prefix to an OS path, with an optional set of allowed
// users. An empty AllowedUsers slice means all authenticated users may access
// the directory.
type dirOpt struct {
	Name         string   `json:"name"`
	Prefix       string   `json:"prefix"`
	OsPath       string   `json:"os_path"`
	AllowedUsers []string `json:"allowed_users"`
}

// ---------------------------------------------------------------------------
// Options loading
// ---------------------------------------------------------------------------

func loadOptions(path string) (*options, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open options file: %w", err)
	}
	defer func(f *os.File) {
		if closeErr := f.Close(); closeErr != nil {
			log.Printf("close options file: %v", closeErr)
		}
	}(f)

	var o options
	if err := json.NewDecoder(f).Decode(&o); err != nil {
		return nil, fmt.Errorf("decode options file: %w", err)
	}

	if o.ServerPort == 0 {
		o.ServerPort = 8080
	}

	return &o, nil
}

// ---------------------------------------------------------------------------
// Password hashing
// ---------------------------------------------------------------------------

// hashUsers returns a username → bcrypt-hash map built from the plain-text
// credentials in the options file. This is the only place plaintext passwords
// are handled; they are not stored or logged anywhere.
func hashUsers(users []userOpt) (map[string][]byte, error) {
	m := make(map[string][]byte, len(users))
	for _, u := range users {
		if u.Username == "" {
			return nil, fmt.Errorf("user entry has an empty username")
		}
		if _, dup := m[u.Username]; dup {
			return nil, fmt.Errorf("duplicate username: %q", u.Username)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(u.Password), bcrypt.DefaultCost)
		if err != nil {
			return nil, fmt.Errorf("hash password for %q: %w", u.Username, err)
		}
		m[u.Username] = hash
	}
	return m, nil
}

// ---------------------------------------------------------------------------
// Basic-auth middleware
// ---------------------------------------------------------------------------

// authHandler wraps an http.Handler with HTTP Basic Authentication and an
// optional per-directory user allowlist.
type authHandler struct {
	// hashes maps username → bcrypt hash (pre-computed at startup).
	hashes map[string][]byte

	// allowedUsers is the set of usernames permitted to access this handler.
	// A nil map means all authenticated users are allowed.
	allowedUsers map[string]struct{}

	next http.Handler
}

func (a *authHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok {
		w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	hash, known := a.hashes[username]
	if !known {
		// Use a constant-work path to avoid timing-based user enumeration.
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$invalidhashpadding000000000000000000000000000000000000000"), []byte(password))
		w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		w.Header().Set("WWW-Authenticate", `Basic realm="WebDAV"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Check the directory-level access control list.
	if a.allowedUsers != nil {
		if _, allowed := a.allowedUsers[username]; !allowed {
			http.Error(w, "Forbidden", http.StatusForbidden)
			return
		}
	}

	log.Printf("%s %s %s [user=%s]", r.Method, r.Host, r.URL.Path, username)
	a.next.ServeHTTP(w, r)
}

// newAuthHandler constructs an authHandler for the given directory config.
func newAuthHandler(hashes map[string][]byte, dir dirOpt, next http.Handler) *authHandler {
	var allowedUsers map[string]struct{}
	if len(dir.AllowedUsers) > 0 {
		allowedUsers = make(map[string]struct{}, len(dir.AllowedUsers))
		for _, u := range dir.AllowedUsers {
			allowedUsers[u] = struct{}{}
		}
	}
	return &authHandler{
		hashes:       hashes,
		allowedUsers: allowedUsers,
		next:         next,
	}
}

// ---------------------------------------------------------------------------
// Server setup
// ---------------------------------------------------------------------------

func buildMux(opts *options, hashes map[string][]byte) (*http.ServeMux, error) {
	mux := http.NewServeMux()

	for _, dir := range opts.Directories {
		if dir.Prefix == "" {
			return nil, fmt.Errorf("directory %q has an empty prefix", dir.Name)
		}
		if dir.OsPath == "" {
			return nil, fmt.Errorf("directory %q has an empty os_path", dir.Name)
		}

		// Ensure the backing OS directory exists.
		if err := os.MkdirAll(dir.OsPath, 0o755); err != nil {
			return nil, fmt.Errorf("create directory for %q (%s): %w", dir.Name, dir.OsPath, err)
		}

		// Normalize: the prefix registered on the mux must end with "/".
		prefix := dir.Prefix
		if !strings.HasSuffix(prefix, "/") {
			prefix += "/"
		}

		handler := &webdav.Handler{
			Prefix:     prefix,
			FileSystem: webdav.Dir(dir.OsPath),
			LockSystem: webdav.NewMemLS(),
			Logger: func(r *http.Request, err error) {
				if err != nil {
					log.Printf("webdav error [%s] %s: %v", dir.Name, r.URL.Path, err)
				}
			},
		}

		auth := newAuthHandler(hashes, dir, handler)

		log.Printf(
			"mounting [%s] %s -> %s (allowed: %v)",
			dir.Name, prefix, dir.OsPath, dir.AllowedUsers,
		)

		// Registering a pattern ending in "/" causes http.ServeMux to
		// automatically redirect the bare path (without trailing slash) to
		// the slash-terminated form with a 301. This is correct WebDAV
		// semantics for collections and avoids the webdav "prefix mismatch"
		// error that occurs when the handler receives a path that doesn't
		// match its configured Prefix exactly.
		mux.Handle(prefix, auth)
	}

	return mux, nil
}

// ---------------------------------------------------------------------------
// Entry point
// ---------------------------------------------------------------------------

func main() {
	optionsPath := flag.String("options", "/data/options.json", "path to the HA options JSON file")
	doHash := flag.Bool("hash-password", false, "hash a plaintext password and print it, then exit")
	flag.Parse()

	// Utility sub-command: print a bcrypt hash for the given plaintext.
	if *doHash {
		if flag.NArg() < 1 {
			_, _ = fmt.Fprintln(os.Stderr, "usage: demiplane -hash-password <plaintext>")
			os.Exit(1)
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(flag.Arg(0)), bcrypt.DefaultCost)
		if err != nil {
			log.Fatalf("hash password: %v", err)
		}
		fmt.Println(string(hash))
		return
	}

	opts, err := loadOptions(*optionsPath)
	if err != nil {
		log.Fatalf("load options: %v", err)
	}

	if len(opts.Users) == 0 {
		log.Fatal("no users configured; add at least one user in the app options")
	}
	if len(opts.Directories) == 0 {
		log.Fatal("no directories configured; add at least one directory in the app options")
	}

	log.Printf("hashing passwords for %d user(s)...", len(opts.Users))
	hashes, err := hashUsers(opts.Users)
	if err != nil {
		log.Fatalf("prepare users: %v", err)
	}

	mux, err := buildMux(opts, hashes)
	if err != nil {
		log.Fatalf("build routes: %v", err)
	}

	addr := fmt.Sprintf(":%d", opts.ServerPort)
	log.Printf("starting Demiplane WebDAV server on %s", addr)

	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("server shutdown: %v", err)
	}
}
