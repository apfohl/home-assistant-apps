package main

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
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

const (
	defaultListenHost = "127.0.0.1"
	defaultHTTPPort   = 8090
	defaultHTTPSPort  = 8091
	defaultTCPPort    = 8092
	defaultTLSTCPPort = 8093
	defaultCertDir    = "/data/certs"
	appName           = "protocol-test"
)

func main() {
	host := envString("LISTEN_HOST", defaultListenHost)
	httpPort := envInt("HTTP_PORT", defaultHTTPPort)
	httpsPort := envInt("HTTPS_PORT", defaultHTTPSPort)
	tcpPort := envInt("TCP_PORT", defaultTCPPort)
	tlstcpPort := envInt("TLS_TCP_PORT", defaultTLSTCPPort)
	certDir := envString("CERT_DIR", defaultCertDir)

	if err := os.MkdirAll(certDir, 0o700); err != nil {
		log.Fatalf("failed to create cert directory: %v", err)
	}

	certFile, keyFile, err := ensureCert(certDir)
	if err != nil {
		log.Fatalf("failed to set up TLS certificates: %v", err)
	}

	tlsConfig := &tls.Config{
		MinVersion: tls.VersionTLS12,
	}
	tlsConfig.Certificates = make([]tls.Certificate, 1)
	tlsConfig.Certificates[0], err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Fatalf("failed to load TLS certificates: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", httpHandler)
	mux.HandleFunc("/health", healthHandler)

	servers := &serverGroup{}

	// HTTP server
	servers.Add(startHTTPServer(host, httpPort, mux))

	// HTTPS server
	servers.Add(startHTTPSServer(host, httpsPort, mux, tlsConfig))

	// TCP echo server
	servers.Add(startTCPServer(host, tcpPort, "tcp"))

	// TLS-terminated TCP echo server
	servers.Add(startTLSTCPServer(host, tlstcpPort, tlsConfig))

	log.Printf("%s listening on http://%s:%d, https://%s:%d, tcp://%s:%d, tls-tcp://%s:%d",
		appName, host, httpPort, host, httpsPort, host, tcpPort, host, tlstcpPort)

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("protocol-test shutting down...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := servers.Shutdown(shutdownCtx); err != nil {
		log.Printf("shutdown error: %v", err)
	}

	log.Println("protocol-test stopped")
}

func httpHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	resp := map[string]string{
		"app":       appName,
		"protocol":  "http",
		"path":      r.URL.Path,
		"hostname":  hostname(),
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	}

	if err := json.NewEncoder(w).Encode(resp); err != nil {
		log.Printf("failed to encode http response: %v", err)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func handleTCPConnection(conn net.Conn, protocol string) {
	defer conn.Close()

	remote := conn.RemoteAddr().String()
	log.Printf("[%s] connection from %s", protocol, remote)

	_, _ = fmt.Fprintf(conn, "PROTOCOL-%s %s %s\n",
		strings.ToUpper(protocol), hostname(), time.Now().UTC().Format(time.RFC3339))

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}
		_, _ = fmt.Fprintf(conn, "ECHO: %s\n", line)
	}

	if err := scanner.Err(); err != nil && !errors.Is(err, net.ErrClosed) {
		log.Printf("[%s] connection from %s closed with error: %v", protocol, remote, err)
	}
}

func ensureCert(dir string) (certFile string, keyFile string, err error) {
	certFile = filepath.Join(dir, "protocol-test.crt")
	keyFile = filepath.Join(dir, "protocol-test.key")

	if _, err := os.Stat(certFile); err == nil {
		if _, err := os.Stat(keyFile); err == nil {
			return certFile, keyFile, nil
		}
	}

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", fmt.Errorf("generate key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return "", "", fmt.Errorf("generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"protocol-test"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:              []string{"localhost"},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("create certificate: %w", err)
	}

	certOut, err := os.OpenFile(certFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", "", fmt.Errorf("open cert file: %w", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: certDER}); err != nil {
		_ = certOut.Close()
		return "", "", fmt.Errorf("write cert file: %w", err)
	}
	_ = certOut.Close()

	keyOut, err := os.OpenFile(keyFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return "", "", fmt.Errorf("open key file: %w", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		_ = keyOut.Close()
		return "", "", fmt.Errorf("marshal private key: %w", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		_ = keyOut.Close()
		return "", "", fmt.Errorf("write key file: %w", err)
	}
	_ = keyOut.Close()

	log.Printf("generated self-signed certificate: %s", certFile)
	return certFile, keyFile, nil
}

func hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return "unknown"
	}
	return h
}

func envString(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func envInt(key string, defaultValue int) int {
	if v := os.Getenv(key); v != "" {
		var n int
		if _, err := fmt.Sscanf(v, "%d", &n); err == nil {
			return n
		}
		log.Printf("ignoring invalid %s=%q, using default %d", key, v, defaultValue)
	}
	return defaultValue
}

// server represents one of the running listeners.
type server interface {
	Shutdown(ctx context.Context) error
}

type serverGroup struct {
	mu       sync.Mutex
	servers  []server
	httpErrs []error
}

func (sg *serverGroup) Add(s server) {
	sg.mu.Lock()
	defer sg.mu.Unlock()
	sg.servers = append(sg.servers, s)
}

func (sg *serverGroup) Shutdown(ctx context.Context) error {
	sg.mu.Lock()
	servers := make([]server, len(sg.servers))
	copy(servers, sg.servers)
	sg.mu.Unlock()

	var wg sync.WaitGroup
	for _, s := range servers {
		wg.Add(1)
		go func(s server) {
			defer wg.Done()
			if err := s.Shutdown(ctx); err != nil {
				sg.mu.Lock()
				sg.httpErrs = append(sg.httpErrs, err)
				sg.mu.Unlock()
			}
		}(s)
	}
	wg.Wait()

	if len(sg.httpErrs) > 0 {
		return sg.httpErrs[0]
	}
	return nil
}

// httpServer wraps an *http.Server so it satisfies the server interface.
type httpServer struct {
	*http.Server
	addr string
}

func (s *httpServer) Shutdown(ctx context.Context) error {
	return s.Server.Shutdown(ctx)
}

func startHTTPServer(host string, port int, handler http.Handler) *httpServer {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	srv := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	go func() {
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server error: %v", err)
		}
	}()

	return &httpServer{Server: srv, addr: addr}
}

func startHTTPSServer(host string, port int, handler http.Handler, config *tls.Config) *httpServer {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	srv := &http.Server{
		Addr:      addr,
		Handler:   handler,
		TLSConfig: config,
	}

	go func() {
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("https server error: %v", err)
		}
	}()

	return &httpServer{Server: srv, addr: addr}
}

// tcpServer wraps a net.Listener so it can be shut down.
type tcpServer struct {
	listener net.Listener
	protocol string
}

func (s *tcpServer) Shutdown(ctx context.Context) error {
	return s.listener.Close()
}

func startTCPServer(host string, port int, protocol string) *tcpServer {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("tcp server listen error: %v", err)
	}

	srv := &tcpServer{listener: ln, protocol: protocol}

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				log.Printf("tcp accept error: %v", err)
				continue
			}
			go handleTCPConnection(conn, protocol)
		}
	}()

	return srv
}

// tlsTCPServer wraps a TLS net.Listener so it can be shut down.
type tlsTCPServer struct {
	listener net.Listener
}

func (s *tlsTCPServer) Shutdown(ctx context.Context) error {
	return s.listener.Close()
}

func startTLSTCPServer(host string, port int, config *tls.Config) *tlsTCPServer {
	addr := net.JoinHostPort(host, fmt.Sprintf("%d", port))
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("tls-tcp server listen error: %v", err)
	}
	tlsLn := tls.NewListener(ln, config)

	srv := &tlsTCPServer{listener: tlsLn}

	go func() {
		for {
			conn, err := tlsLn.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				log.Printf("tls-tcp accept error: %v", err)
				continue
			}
			go handleTCPConnection(conn, "tls-tcp")
		}
	}()

	return srv
}
