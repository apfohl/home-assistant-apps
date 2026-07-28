package main

import (
	"bufio"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type testSpec struct {
	name     string
	protocol string
	addr     string
	url      string
}

type testResult struct {
	spec testSpec
	ok   bool
	err  string
}

func main() {
	var (
		tailnet       = flag.String("tailnet", "", "Tailnet name, e.g. tail1234.ts.net (required)")
		httpService   = flag.String("http-service", "http-test", "Name of the HTTP service")
		httpsService  = flag.String("https-service", "https-test", "Name of the HTTPS service")
		tcpService    = flag.String("tcp-service", "tcp-test", "Name of the TCP service")
		tlsTCPService = flag.String("tls-tcp-service", "tls-tcp-test", "Name of the TLS-terminated TCP service")
		httpPort      = flag.Int("http-port", 80, "Port for the HTTP service")
		httpsPort     = flag.Int("https-port", 443, "Port for the HTTPS service")
		tcpPort       = flag.Int("tcp-port", 10000, "Port for the TCP service")
		tlsTCPPort    = flag.Int("tls-tcp-port", 8443, "Port for the TLS-terminated TCP service")
		timeout       = flag.Duration("timeout", 10*time.Second, "Per-test timeout")
		insecure      = flag.Bool("insecure", false, "Skip TLS certificate verification (for local testing)")
	)
	flag.Parse()

	if *tailnet == "" {
		fmt.Fprintln(os.Stderr, "error: --tailnet is required")
		flag.Usage()
		os.Exit(1)
	}

	client := &http.Client{
		Timeout: *timeout,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{
				InsecureSkipVerify: *insecure,
				MinVersion:         tls.VersionTLS12,
			},
		},
	}

	specs := []testSpec{
		{name: *httpService, protocol: "HTTP", addr: serviceAddr(*httpService, *tailnet, *httpPort), url: serviceURL("http", *httpService, *tailnet, *httpPort, "/")},
		{name: *httpsService, protocol: "HTTPS", addr: serviceAddr(*httpsService, *tailnet, *httpsPort), url: serviceURL("https", *httpsService, *tailnet, *httpsPort, "/")},
		{name: *tcpService, protocol: "TCP", addr: serviceAddr(*tcpService, *tailnet, *tcpPort), url: ""},
		{name: *tlsTCPService, protocol: "TLS-TCP", addr: serviceAddr(*tlsTCPService, *tailnet, *tlsTCPPort), url: ""},
	}

	results := make([]testResult, len(specs))
	for i, spec := range specs {
		results[i] = runTest(client, spec, *timeout, *insecure)
	}

	fmt.Println()
	fmt.Printf("%-40s %-10s %-6s %s\n", "ADDRESS", "PROTOCOL", "RESULT", "DETAIL")
	failed := false
	for _, r := range results {
		status := "PASS"
		if !r.ok {
			status = "FAIL"
			failed = true
		}
		detail := ""
		if !r.ok {
			detail = r.err
		}
		fmt.Printf("%-40s %-10s %-6s %s\n", r.spec.addr, r.spec.protocol, status, detail)
	}

	if failed {
		os.Exit(1)
	}
}

func serviceAddr(service, tailnet string, port int) string {
	return fmt.Sprintf("%s.%s:%d", service, tailnet, port)
}

func serviceURL(scheme, service, tailnet string, port int, path string) string {
	return fmt.Sprintf("%s://%s%s", scheme, serviceAddr(service, tailnet, port), path)
}

func runTest(client *http.Client, spec testSpec, timeout time.Duration, insecure bool) testResult {
	switch spec.protocol {
	case "HTTP", "HTTPS":
		return testHTTP(client, spec)
	case "TCP":
		return testTCP(spec, timeout)
	case "TLS-TCP":
		return testTLSTCP(spec, timeout, insecure)
	default:
		return testResult{spec: spec, ok: false, err: "unknown protocol"}
	}
}

func testHTTP(client *http.Client, spec testSpec) testResult {
	resp, err := client.Get(spec.url)
	if err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}

	if resp.StatusCode != http.StatusOK {
		return testResult{spec: spec, ok: false, err: fmt.Sprintf("HTTP %d", resp.StatusCode)}
	}

	if !strings.Contains(string(body), "protocol-test") {
		return testResult{spec: spec, ok: false, err: "unexpected body"}
	}

	return testResult{spec: spec, ok: true}
}

func testTCP(spec testSpec, timeout time.Duration) testResult {
	conn, err := net.DialTimeout("tcp", spec.addr, timeout)
	if err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}

	reader := bufio.NewReader(conn)

	// The server sends a greeting line on connect; discard it.
	if _, err := reader.ReadString('\n'); err != nil {
		return testResult{spec: spec, ok: false, err: fmt.Sprintf("read greeting: %v", err)}
	}

	if _, err := fmt.Fprintf(conn, "ping\n"); err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}
	line = strings.TrimSpace(line)
	if line != "ECHO: ping" {
		return testResult{spec: spec, ok: false, err: fmt.Sprintf("got %q", line)}
	}

	return testResult{spec: spec, ok: true}
}

func testTLSTCP(spec testSpec, timeout time.Duration, insecure bool) testResult {
	host, _, err := net.SplitHostPort(spec.addr)
	if err != nil {
		host = spec.addr
	}

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := tls.DialWithDialer(dialer, "tcp", spec.addr, &tls.Config{
		ServerName:         host,
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecure,
	})
	if err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}

	reader := bufio.NewReader(conn)

	// The server sends a greeting line on connect; discard it.
	if _, err := reader.ReadString('\n'); err != nil {
		return testResult{spec: spec, ok: false, err: fmt.Sprintf("read greeting: %v", err)}
	}

	if _, err := fmt.Fprintf(conn, "ping\n"); err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}

	line, err := reader.ReadString('\n')
	if err != nil {
		return testResult{spec: spec, ok: false, err: err.Error()}
	}
	line = strings.TrimSpace(line)
	if line != "ECHO: ping" {
		return testResult{spec: spec, ok: false, err: fmt.Sprintf("got %q", line)}
	}

	return testResult{spec: spec, ok: true}
}
