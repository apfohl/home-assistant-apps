package main

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

//go:embed dashboard.html
var dashboardHTML string

const (
	ingressIP         = "172.30.32.2"
	listenAddr        = ":8099"
	supervisorBaseURL = "http://supervisor"
)

// supervisorInfo mirrors the relevant fields from GET /supervisor/info
type supervisorInfo struct {
	Data struct {
		Version  string `json:"version"`
		Channel  string `json:"channel"`
		Arch     string `json:"arch"`
		Hostname string `json:"hostname"`
	} `json:"data"`
}

// coreInfo mirrors the relevant fields from GET /core/info
type coreInfo struct {
	Data struct {
		Version         string `json:"version"`
		LastVersion     string `json:"version_latest"`
		UpdateAvailable bool   `json:"update_available"`
	} `json:"data"`
}

// hostInfo mirrors the relevant fields from GET /host/info
// boot_timestamp is Unix epoch in microseconds.
type hostInfo struct {
	Data struct {
		Hostname      string  `json:"hostname"`
		OS            string  `json:"operating_system"`
		KernelVersion string  `json:"kernel"`
		BootTimestamp int64   `json:"boot_timestamp"`
		DiskTotal     float64 `json:"disk_total"`
		DiskUsed      float64 `json:"disk_used"`
		DiskFree      float64 `json:"disk_free"`
	} `json:"data"`
}

// pageData is fed into the HTML template
type pageData struct {
	IngressPath           string
	RemoteUserID          string
	RemoteUserName        string
	RemoteUserDisplayName string

	SupervisorVersion string
	SupervisorChannel string
	Arch              string

	HAVersion         string
	HALastVersion     string
	HAUpdateAvailable bool

	Hostname      string
	OS            string
	KernelVersion string
	UptimeHuman   string
	DiskTotal     string
	DiskUsed      string
	DiskFree      string
	DiskPct       int

	FetchError string
	Timestamp  string
}

func main() {
	supervisorToken := os.Getenv("SUPERVISOR_TOKEN")

	mux := http.NewServeMux()

	tmpl, err := template.New("dashboard").Parse(dashboardHTML)
	if err != nil {
		log.Fatalf("failed to parse dashboard template: %v", err)
	}

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// Only accept connections from the HA ingress gateway
		var host string
		host, _, err = net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}
		if host != ingressIP {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}

		data := pageData{
			IngressPath:           r.Header.Get("X-Ingress-Path"),
			RemoteUserID:          r.Header.Get("X-Remote-User-Id"),
			RemoteUserName:        r.Header.Get("X-Remote-User-Name"),
			RemoteUserDisplayName: r.Header.Get("X-Remote-User-Display-Name"),
			Timestamp:             time.Now().UTC().Format("2006-01-02 15:04:05 UTC"),
		}

		if data.IngressPath == "" {
			data.IngressPath = "/"
		}

		if supervisorToken != "" {
			var fetchErrs []string

			// Fetch supervisor info
			var si supervisorInfo
			if si, err = fetchJSON[supervisorInfo](supervisorToken, supervisorBaseURL+"/supervisor/info"); err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("supervisor/info: %v", err))
			} else {
				data.SupervisorVersion = si.Data.Version
				data.SupervisorChannel = si.Data.Channel
				data.Arch = si.Data.Arch
			}

			// Fetch HA core info
			var ci coreInfo
			if ci, err = fetchJSON[coreInfo](supervisorToken, supervisorBaseURL+"/core/info"); err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("core/info: %v", err))
			} else {
				data.HAVersion = ci.Data.Version
				data.HALastVersion = ci.Data.LastVersion
				data.HAUpdateAvailable = ci.Data.UpdateAvailable
			}

			// Fetch host info
			var hi hostInfo
			if hi, err = fetchJSON[hostInfo](supervisorToken, supervisorBaseURL+"/host/info"); err != nil {
				fetchErrs = append(fetchErrs, fmt.Sprintf("host/info: %v", err))
			} else {
				data.Hostname = hi.Data.Hostname
				data.OS = hi.Data.OS
				data.KernelVersion = hi.Data.KernelVersion

				// boot_timestamp is microseconds since Unix epoch
				if hi.Data.BootTimestamp > 0 {
					bootSecs := hi.Data.BootTimestamp / 1_000_000
					uptimeSecs := time.Now().Unix() - bootSecs
					data.UptimeHuman = formatUptime(uptimeSecs)
				}

				// Disk info (values are already in GB)
				if hi.Data.DiskTotal > 0 {
					data.DiskTotal = fmt.Sprintf("%.1f GB", hi.Data.DiskTotal)
					data.DiskUsed = fmt.Sprintf("%.1f GB", hi.Data.DiskUsed)
					data.DiskFree = fmt.Sprintf("%.1f GB", hi.Data.DiskFree)
					data.DiskPct = int(hi.Data.DiskUsed / hi.Data.DiskTotal * 100)
				}
			}

			if len(fetchErrs) > 0 {
				data.FetchError = strings.Join(fetchErrs, "; ")
			}
		} else {
			data.FetchError = "SUPERVISOR_TOKEN not set — running outside Home Assistant?"
		}

		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if err = tmpl.Execute(w, data); err != nil {
			log.Printf("template execute error: %v", err)
		}
	})

	srv := &http.Server{
		Addr:    listenAddr,
		Handler: mux,
	}

	// Graceful shutdown
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		log.Printf("Tavern listening on %s (ingress-only from %s)", listenAddr, ingressIP)
		if err = srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("ListenAndServe error: %v", err)
		}
	}()

	<-quit
	log.Println("Tavern shutting down…")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err = srv.Shutdown(ctx); err != nil {
		log.Printf("Shutdown error: %v", err)
	}

	log.Println("Tavern stopped")
}

// fetchJSON performs a GET against the HA Supervisor API and decodes the JSON response.
func fetchJSON[T any](token, url string) (T, error) {
	var zero T

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return zero, err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return zero, err
	}
	defer func(Body io.ReadCloser) {
		if closeErr := Body.Close(); closeErr != nil {
			log.Printf("Failed to close response body: %v", closeErr)
		}
	}(resp.Body)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return zero, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var result T
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return zero, err
	}

	return result, nil
}

// formatUptime converts seconds to a human-readable string.
func formatUptime(seconds int64) string {
	if seconds <= 0 {
		return "unknown"
	}

	d := seconds / 86400
	h := (seconds % 86400) / 3600
	m := (seconds % 3600) / 60
	s := seconds % 60

	if d > 0 {
		return fmt.Sprintf("%dd %02dh %02dm %02ds", d, h, m, s)
	}

	return fmt.Sprintf("%02dh %02dm %02ds", h, m, s)
}
