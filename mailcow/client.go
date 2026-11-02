// Scraping client for the hosted mailcow UI.
//
// This is deliberately stdlib-only (net/http, net/http/cookiejar): no
// goquery/chromedp dependency. It logs in as a domain admin and scrapes the
// manager tables and forms used for mailbox alias management.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	baseURL       = "https://hosted.mailcow.de"
	loginPath     = "/"
	loginTimeout  = 15 * time.Second
	sessionMaxAge = 20 * time.Minute
	managerPath   = "/manager"
)

var (
	tableByIDPattern = regexp.MustCompile(`(?is)<table[^>]*\bid\s*=\s*["']([^"']+)["'][^>]*>(.*?)</table>`)
	tbodyPattern     = regexp.MustCompile(`(?is)<tbody[^>]*>(.*?)</tbody>`)
	rowPattern       = regexp.MustCompile(`(?is)<tr[^>]*>(.*?)</tr>`)
	cellPattern      = regexp.MustCompile(`(?is)<t[dh][^>]*>(.*?)</t[dh]>`)
	tagPattern       = regexp.MustCompile(`(?is)<[^>]+>`)

	inputPattern            = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	attributePattern        = regexp.MustCompile(`(?is)([a-zA-Z_:][a-zA-Z0-9_:.-]*)\s*=\s*["']([^"']*)["']`)
	hiddenInputPattern      = regexp.MustCompile(`(?is)<input[^>]*name\s*=\s*["']address["'][^>]*>`)
	hiddenInputValuePattern = regexp.MustCompile(`(?is)value\s*=\s*["']([^"']*)["']`)
	textareaPattern         = regexp.MustCompile(`(?is)<textarea[^>]*name\s*=\s*["']goto["'][^>]*>(.*?)</textarea>`)
	alertPattern            = regexp.MustCompile(`(?is)<div[^>]*id\s*=\s*["']alert-fade["'][^>]*class\s*=\s*["']alert alert-(success|danger)["'][^>]*>(.*?)</div>`)

	ErrAliasInvalid      = errors.New("alias creation failed: access denied or invalid/incomplete data")
	ErrAliasDeleteFailed = errors.New("alias deletion failed: access denied or invalid/incomplete data")
	ErrAliasEditFailed   = errors.New("alias edit failed: access denied or invalid/incomplete data")
)

// Client holds one authenticated scraping session. Mailcow's session
// (and the scrape target in general) is not safe for concurrent use in the
// naive sense — a second login or page fetch while one is in flight can
// invalidate cookies or trip whatever anti-automation exists. Every method
// therefore serializes through mu.
type Client struct {
	httpClient *http.Client
	mu         sync.Mutex

	username   string
	password   string
	role       Role
	cookieFile string

	loggedInAt time.Time
}

type persistedSession struct {
	LoggedInAt time.Time      `json:"logged_in_at,omitzero"`
	Cookies    []*http.Cookie `json:"cookies,omitzero"`
}

// Role distinguishes the two login paths on the shared mailcow instance.
type Role string

const (
	RoleDomainAdmin Role = "domain_admin"
	RoleMailboxUser Role = "mailbox_user"
)

type Config struct {
	Username   string
	Password   string
	Role       Role
	CookieFile string
}

func NewClient(cfg Config) (*Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("mailcow: creating cookie jar: %w", err)
	}

	c := &Client{
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: loginTimeout,
		},
		username:   cfg.Username,
		password:   cfg.Password,
		role:       cfg.Role,
		cookieFile: strings.TrimSpace(cfg.CookieFile),
	}

	if err := c.ensureCookieStoreFile(); err != nil {
		return nil, err
	}

	if err := c.loadSession(); err != nil {
		return nil, err
	}

	return c, nil
}

// ensureSession logs in if we have no session yet, or if the last one is
// old enough that it's likely expired.
func (c *Client) ensureSession(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.loggedInAt.IsZero() && time.Since(c.loggedInAt) < sessionMaxAge {
		log.Printf("mailcow: using in-memory authenticated session (age=%s)", time.Since(c.loggedInAt).Round(time.Second))
		return nil
	}

	log.Printf("mailcow: checking persisted cookie session")
	if ok, err := c.sessionLooksActive(ctx); err == nil && ok {
		log.Printf("mailcow: persisted cookie session is active; skipping credential login")
		c.loggedInAt = time.Now()
		if err := c.saveSession(); err != nil {
			return err
		}
		return nil
	} else if err != nil {
		log.Printf("mailcow: persisted cookie session check failed, falling back to credential login: %v", err)
	} else {
		log.Printf("mailcow: persisted cookie session is not active, falling back to credential login")
	}

	log.Printf("mailcow: starting credential login flow")
	return c.login(ctx)
}

// Ping exercises the login flow only, without fetching any real data yet.
func (c *Client) Ping(ctx context.Context) error {
	return c.ensureSession(ctx)
}

// Login performs an explicit login attempt.
func (c *Client) Login(ctx context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.login(ctx)
}

// ListDomains returns domain entries from the manager UI table.
func (c *Client) ListDomains(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, managerPath)
	if err != nil {
		return nil, err
	}

	rows, err := parseTableFirstColumn(body, "domaintable")
	if err != nil {
		return nil, fmt.Errorf("mailcow: listing domains: %w", err)
	}

	return rows, nil
}

// ListDomainAliases returns domain alias entries from the manager UI table.
func (c *Client) ListDomainAliases(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, managerPath)
	if err != nil {
		return nil, err
	}

	rows, err := parseTableFirstColumn(body, "domainaliastable")
	if err != nil {
		return nil, fmt.Errorf("mailcow: listing domain aliases: %w", err)
	}

	return rows, nil
}

// ListMailboxes returns mailbox entries from the manager UI table.
func (c *Client) ListMailboxes(ctx context.Context) ([]string, error) {
	body, err := c.get(ctx, managerPath)
	if err != nil {
		return nil, err
	}

	rows, err := parseTableFirstColumn(body, "mailboxtable")
	if err != nil {
		return nil, fmt.Errorf("mailcow: listing mailboxes: %w", err)
	}

	return rows, nil
}

// Alias holds one mailbox alias together with its destination addresses.
type Alias struct {
	Address string
	Goto    string
}

// ListAliases returns mailbox alias entries from the manager UI table.
func (c *Client) ListAliases(ctx context.Context) ([]Alias, error) {
	body, err := c.get(ctx, managerPath)
	if err != nil {
		return nil, err
	}

	rows, err := parseTableColumns(body, "aliastable", 2)
	if err != nil {
		return nil, fmt.Errorf("mailcow: listing aliases: %w", err)
	}

	aliases := make([]Alias, 0, len(rows))
	for _, cells := range rows {
		alias := Alias{Address: cells[0]}
		if len(cells) > 1 {
			alias.Goto = cells[1]
		}
		aliases = append(aliases, alias)
	}

	return aliases, nil
}

// CreateAlias creates one or more mailbox aliases via the manager UI form.
// addresses and gotoAddresses are joined with commas, matching the textarea
// behaviour of the original page. The SOGo visibility checkbox is always
// left unchecked.
func (c *Client) CreateAlias(ctx context.Context, addresses, gotoAddresses []string, active bool) error {
	form := url.Values{}
	form.Set("trigger_mailbox_action", "addalias")
	form.Set("address", strings.Join(addresses, ","))
	form.Set("goto", strings.Join(gotoAddresses, ","))
	if active {
		form.Set("active", "on")
	}

	body, err := c.postForm(ctx, "/add/alias", "/add/alias", form)
	if err != nil {
		return fmt.Errorf("mailcow: creating alias: %w", err)
	}

	if msg, ok := extractAlertMessage(body, "danger"); ok {
		return fmt.Errorf("mailcow: creating alias: %s", msg)
	}
	if _, ok := extractAlertMessage(body, "success"); !ok {
		return fmt.Errorf("mailcow: creating alias for %v: %w", addresses, ErrAliasInvalid)
	}
	return nil
}

// DeleteAlias removes a single mailbox alias.
// It requires an explicit success message from the hosted UI.
func (c *Client) DeleteAlias(ctx context.Context, address string) error {
	form := url.Values{}
	form.Set("trigger_mailbox_action", "deletealias")
	form.Set("address", address)

	body, err := c.postForm(ctx, "/delete/alias/"+url.PathEscape(address), "/manager", form)
	if err != nil {
		return fmt.Errorf("mailcow: deleting alias %q: %w", address, err)
	}

	if msg, ok := extractAlertMessage(body, "danger"); ok {
		return fmt.Errorf("mailcow: deleting alias %q: %s", address, msg)
	}
	if _, ok := extractAlertMessage(body, "success"); !ok {
		return fmt.Errorf("mailcow: deleting alias %q: %w", address, ErrAliasDeleteFailed)
	}
	return nil
}

// GetAlias fetches the current edit form for a single alias and extracts its
// destination addresses. The goto value returned is the raw comma-separated
// string exactly as shown in the mailcow textarea.
func (c *Client) GetAlias(ctx context.Context, address string) (*Alias, error) {
	body, err := c.get(ctx, editAliasPath(address))
	if err != nil {
		return nil, fmt.Errorf("mailcow: fetching alias %q: %w", address, err)
	}

	if looksLikeLoginPage([]byte(body)) {
		return nil, fmt.Errorf("mailcow: loading alias %q: session expired or not authenticated", address)
	}

	if msg, ok := extractAlertMessage(body, "danger"); ok {
		return nil, fmt.Errorf("mailcow: loading alias %q: %s", address, msg)
	}

	hiddenMatch := hiddenInputPattern.FindString(body)
	if hiddenMatch == "" {
		return nil, fmt.Errorf("mailcow: alias %q edit form missing address field", address)
	}

	valueMatch := hiddenInputValuePattern.FindStringSubmatch(hiddenMatch)
	if len(valueMatch) != 2 {
		return nil, fmt.Errorf("mailcow: alias %q edit form address field has no value", address)
	}
	if html.UnescapeString(valueMatch[1]) != address {
		return nil, fmt.Errorf("mailcow: alias form address mismatch: got %q, want %q", valueMatch[1], address)
	}

	textareaMatch := textareaPattern.FindStringSubmatch(body)
	if len(textareaMatch) != 2 {
		return nil, fmt.Errorf("mailcow: alias %q edit form missing goto field", address)
	}

	return &Alias{
		Address: address,
		Goto:    strings.TrimSpace(html.UnescapeString(textareaMatch[1])),
	}, nil
}

// EditAlias updates the destination addresses of an existing alias. The active
// and SOGo visibility flags are left untouched.
func (c *Client) EditAlias(ctx context.Context, address string, gotoAddresses []string) error {
	form := url.Values{}
	form.Set("trigger_mailbox_action", "editalias")
	form.Set("address", address)
	form.Set("goto", strings.Join(gotoAddresses, ","))
	body, err := c.postForm(ctx, editAliasPath(address), editAliasPath(address), form)
	if err != nil {
		return fmt.Errorf("mailcow: editing alias %q: %w", address, err)
	}

	if msg, ok := extractAlertMessage(body, "success"); ok {
		_ = msg
		return nil
	}

	if msg, ok := extractAlertMessage(body, "danger"); ok {
		return fmt.Errorf("mailcow: editing alias %q: %s", address, msg)
	}

	return fmt.Errorf("mailcow: editing alias %q: %w", address, ErrAliasEditFailed)
}

func extractAlertMessage(page, class string) (string, bool) {
	matches := alertPattern.FindAllStringSubmatch(page, -1)
	for _, m := range matches {
		if len(m) != 3 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m[1]), class) {
			return normalizeHTMLText(m[2]), true
		}
	}
	return "", false
}

// editAliasPath returns the hosted mailcow edit path for an alias address.
// The @ sign is encoded as %40 to match the browser's URL.
func editAliasPath(address string) string {
	return "/edit/alias/" + url.PathEscape(address)
}

// login performs the credential POST and verifies success deterministically:
// POST / must redirect to /manager, and /manager must return 200 afterwards.
func (c *Client) login(ctx context.Context) error {
	roles := c.rolesToTry()

	var errs []error
	for _, role := range roles {
		log.Printf("mailcow: trying login with role %q", role)
		if err := c.loginWithRole(ctx, role); err != nil {
			errs = append(errs, fmt.Errorf("role %q: %w", role, err))
			log.Printf("mailcow: login failed for role %q: %v", role, err)
			continue
		}

		log.Printf("mailcow: login succeeded with role %q", role)
		c.loggedInAt = time.Now()
		if err := c.saveSession(); err != nil {
			return err
		}
		return nil
	}

	if len(errs) == 0 {
		return fmt.Errorf("mailcow: login failed: no roles to try")
	}

	return fmt.Errorf("mailcow: login failed for all roles: %w", errors.Join(errs...))
}

func (c *Client) loginWithRole(ctx context.Context, role Role) error {
	loginReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+loginPath, nil)
	if err != nil {
		return fmt.Errorf("mailcow: building login page request: %w", err)
	}
	loginPage, err := c.httpClient.Do(loginReq)
	if err != nil {
		return fmt.Errorf("mailcow: fetching login page: %w", err)
	}
	loginBody, readErr := io.ReadAll(loginPage.Body)
	_ = loginPage.Body.Close()
	if readErr != nil {
		return fmt.Errorf("mailcow: reading login page: %w", readErr)
	}
	if loginPage.StatusCode != http.StatusOK {
		return fmt.Errorf("mailcow: login page returned status %d", loginPage.StatusCode)
	}
	form := url.Values{}
	if token, ok := inputValue(string(loginBody), "csrf"); ok {
		form.Set("csrf", token)
	}
	form.Set("fake_user[name]", "Autofill Prevent")
	form.Set("login_user", c.username)
	form.Set("pass_user", c.password)
	form.Set("login_role", role.loginRoleValue())

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+loginPath, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("mailcow: building login request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	loginClient := *c.httpClient
	loginClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := loginClient.Do(req)
	if err != nil {
		return fmt.Errorf("mailcow: login request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusFound, http.StatusSeeOther:
		if !isManagerLocation(resp.Header.Get("Location")) {
			return fmt.Errorf("mailcow: login rejected: unexpected redirect target %q", resp.Header.Get("Location"))
		}
	case http.StatusOK:
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return fmt.Errorf("mailcow: reading login response body: %w", err)
		}
		if looksLikeLoginPage(body) {
			return fmt.Errorf("mailcow: login rejected: credentials or role were not accepted")
		}
	default:
		return fmt.Errorf("mailcow: login rejected: unexpected status %d", resp.StatusCode)
	}

	managerReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/manager", nil)
	if err != nil {
		return fmt.Errorf("mailcow: building manager verification request: %w", err)
	}

	managerClient := *c.httpClient
	managerClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	managerResp, err := managerClient.Do(managerReq)
	if err != nil {
		return fmt.Errorf("mailcow: verifying manager page after login: %w", err)
	}
	defer func() { _ = managerResp.Body.Close() }()

	if managerResp.StatusCode != http.StatusOK {
		return fmt.Errorf("mailcow: login rejected: /manager returned status %d", managerResp.StatusCode)
	}

	managerBody, err := io.ReadAll(managerResp.Body)
	if err != nil {
		return fmt.Errorf("mailcow: reading /manager verification body: %w", err)
	}
	if looksLikeLoginPage(managerBody) {
		return fmt.Errorf("mailcow: login rejected: /manager resolved to login page")
	}

	return nil
}

func (c *Client) rolesToTry() []Role {
	switch c.role {
	case RoleMailboxUser:
		return []Role{RoleMailboxUser}
	case RoleDomainAdmin:
		return []Role{RoleDomainAdmin}
	default:
		return []Role{RoleDomainAdmin, RoleMailboxUser}
	}
}

func (r Role) loginRoleValue() string {
	switch r {
	case RoleMailboxUser:
		return "mailboxuser"
	case RoleDomainAdmin:
		return "domainadmin"
	default:
		return "domainadmin"
	}
}

func isManagerLocation(location string) bool {
	if strings.HasPrefix(location, "/manager") {
		return true
	}

	u, err := url.Parse(location)
	if err != nil {
		return false
	}

	return strings.HasPrefix(u.Path, "/manager")
}

func looksLikeLoginPage(body []byte) bool {
	b := strings.ToLower(string(body))

	if strings.Contains(b, "id=\"login-form\"") {
		return true
	}
	if strings.Contains(b, "name=\"login_user\"") && strings.Contains(b, "name=\"pass_user\"") {
		return true
	}

	return false
}

func parseTableFirstColumn(page string, tableID string) ([]string, error) {
	rows, err := parseTableColumns(page, tableID, 1)
	if err != nil {
		return nil, err
	}

	out := make([]string, 0, len(rows))
	for _, cells := range rows {
		if len(cells) == 0 {
			continue
		}
		value := cells[0]
		if value == "" || value == "-" {
			continue
		}
		out = append(out, value)
	}

	return out, nil
}

// parseTableColumns extracts up to n cells from each row of the table with
// the given id. Empty cells and cells containing only "-" are skipped.
func parseTableColumns(page string, tableID string, n int) ([][]string, error) {
	tableHTML, err := extractTableHTML(page, tableID)
	if err != nil {
		return nil, err
	}

	tbodyHTML := tableHTML
	if tbodyMatch := tbodyPattern.FindStringSubmatch(tableHTML); len(tbodyMatch) == 2 {
		tbodyHTML = tbodyMatch[1]
	}

	rowMatches := rowPattern.FindAllStringSubmatch(tbodyHTML, -1)
	if len(rowMatches) == 0 {
		return [][]string{}, nil
	}

	rows := make([][]string, 0, len(rowMatches))
	for _, row := range rowMatches {
		if len(row) != 2 {
			continue
		}

		cellMatches := cellPattern.FindAllStringSubmatch(row[1], -1)
		if len(cellMatches) == 0 {
			continue
		}

		cells := make([]string, 0, n)
		for i, cm := range cellMatches {
			if i >= n {
				break
			}
			if len(cm) != 2 {
				continue
			}
			value := normalizeHTMLText(cm[1])
			cells = append(cells, value)
		}

		if len(cells) == 0 {
			continue
		}
		if cells[0] == "" || cells[0] == "-" {
			continue
		}

		rows = append(rows, cells)
	}

	return rows, nil
}

func extractTableHTML(page string, tableID string) (string, error) {
	matches := tableByIDPattern.FindAllStringSubmatch(page, -1)
	for _, m := range matches {
		if len(m) != 3 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(m[1]), tableID) {
			return m[2], nil
		}
	}

	return "", fmt.Errorf("table %q not found", tableID)
}

func normalizeHTMLText(v string) string {
	noTags := tagPattern.ReplaceAllString(v, " ")
	unescaped := html.UnescapeString(noTags)
	return strings.Join(strings.Fields(strings.TrimSpace(unescaped)), " ")
}

func (c *Client) sessionLooksActive(ctx context.Context) (bool, error) {
	managerReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/manager", nil)
	if err != nil {
		return false, fmt.Errorf("mailcow: building session verification request: %w", err)
	}

	managerClient := *c.httpClient
	managerClient.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	managerResp, err := managerClient.Do(managerReq)
	if err != nil {
		return false, fmt.Errorf("mailcow: verifying existing session: %w", err)
	}
	defer func() { _ = managerResp.Body.Close() }()

	if managerResp.StatusCode != http.StatusOK {
		return false, nil
	}

	managerBody, err := io.ReadAll(managerResp.Body)
	if err != nil {
		return false, fmt.Errorf("mailcow: reading existing session verification body: %w", err)
	}

	return !looksLikeLoginPage(managerBody), nil
}

func (c *Client) loadSession() error {
	if c.cookieFile == "" {
		log.Printf("mailcow: cookie jar file is not configured")
		return nil
	}

	data, err := os.ReadFile(c.cookieFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			log.Printf("mailcow: cookie jar file %q does not exist yet", c.cookieFile)
			return nil
		}
		return fmt.Errorf("mailcow: reading cookie jar file %q: %w", c.cookieFile, err)
	}

	var persisted persistedSession
	if err := json.Unmarshal(data, &persisted); err != nil {
		return fmt.Errorf("mailcow: parsing cookie jar file %q: %w", c.cookieFile, err)
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("mailcow: parsing base url: %w", err)
	}

	c.httpClient.Jar.SetCookies(base, persisted.Cookies)
	c.loggedInAt = persisted.LoggedInAt
	log.Printf(
		"mailcow: loaded %d cookies from %q (logged_in_at=%s)",
		len(persisted.Cookies),
		c.cookieFile,
		persisted.LoggedInAt.Format(time.RFC3339),
	)

	return nil
}

func (c *Client) ensureCookieStoreFile() error {
	if c.cookieFile == "" {
		return nil
	}

	dir := filepath.Dir(c.cookieFile)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mailcow: creating cookie jar directory %q: %w", dir, err)
		}
	}

	f, err := os.OpenFile(c.cookieFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			log.Printf("mailcow: using existing cookie jar file %q", c.cookieFile)
			return nil
		}
		return fmt.Errorf("mailcow: creating cookie jar file %q: %w", c.cookieFile, err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.WriteString("{}\n"); err != nil {
		return fmt.Errorf("mailcow: initializing cookie jar file %q: %w", c.cookieFile, err)
	}
	log.Printf("mailcow: created cookie jar file %q", c.cookieFile)

	return nil
}

func (c *Client) saveSession() error {
	if c.cookieFile == "" {
		return nil
	}

	base, err := url.Parse(baseURL)
	if err != nil {
		return fmt.Errorf("mailcow: parsing base url: %w", err)
	}

	persisted := persistedSession{
		LoggedInAt: c.loggedInAt,
		Cookies:    c.httpClient.Jar.Cookies(base),
	}

	data, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("mailcow: encoding cookie jar file %q: %w", c.cookieFile, err)
	}

	dir := filepath.Dir(c.cookieFile)
	if dir != "." {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("mailcow: creating cookie jar directory %q: %w", dir, err)
		}
	}

	if err := os.WriteFile(c.cookieFile, data, 0o600); err != nil {
		return fmt.Errorf("mailcow: writing cookie jar file %q: %w", c.cookieFile, err)
	}
	log.Printf("mailcow: persisted %d cookies to %q", len(persisted.Cookies), c.cookieFile)

	return nil
}

// get fetches an authenticated page and returns its raw body.
func (c *Client) get(ctx context.Context, path string) (string, error) {
	if err := c.ensureSession(ctx); err != nil {
		return "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+path, nil)
	if err != nil {
		return "", fmt.Errorf("mailcow: building request for %s: %w", path, err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("mailcow: fetching %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mailcow: %s returned status %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mailcow: reading response body for %s: %w", path, err)
	}

	if looksLikeLoginPage(body) {
		return "", fmt.Errorf("mailcow: session expired while fetching %s", path)
	}
	return string(body), nil
}

// postForm submits an authenticated form POST and returns the response body.
func (c *Client) postForm(ctx context.Context, formPath, path string, form url.Values) (string, error) {
	if err := c.ensureSession(ctx); err != nil {
		return "", err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	pageReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+formPath, nil)
	if err != nil {
		return "", fmt.Errorf("mailcow: building form request: %w", err)
	}
	pageResp, err := c.httpClient.Do(pageReq)
	if err != nil {
		return "", fmt.Errorf("mailcow: fetching form %s: %w", path, err)
	}
	pageBytes, readErr := io.ReadAll(pageResp.Body)
	_ = pageResp.Body.Close()
	if readErr != nil {
		return "", fmt.Errorf("mailcow: reading form %s: %w", path, readErr)
	}
	if pageResp.StatusCode != http.StatusOK || looksLikeLoginPage(pageBytes) {
		return "", fmt.Errorf("mailcow: form %s unavailable or session expired", path)
	}
	page := string(pageBytes)
	token, ok := inputValue(page, "csrf")
	if !ok || token == "" {
		return "", fmt.Errorf("mailcow: form %s missing CSRF token", path)
	}
	form.Set("csrf", token)
	if action := form.Get("trigger_mailbox_action"); action == "editalias" || action == "deletealias" {
		address, ok := inputValue(page, "address")
		if !ok || address != form.Get("address") {
			return "", fmt.Errorf("mailcow: form address missing or does not match requested alias")
		}
	}
	if form.Get("trigger_mailbox_action") == "editalias" {
		for _, name := range []string{"active", "is_sogo_alias"} {
			if checkboxChecked(page, name) {
				form.Set(name, "on")
			}
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("mailcow: building POST request for %s: %w", path, err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("mailcow: POST %s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("mailcow: %s returned status %d", path, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("mailcow: reading response body for POST %s: %w", path, err)
	}

	if looksLikeLoginPage(body) {
		return "", fmt.Errorf("mailcow: session expired while submitting %s", path)
	}
	return string(body), nil
}

// inputValue extracts a named input independent of attribute order.
func inputValue(page, name string) (string, bool) {
	for _, input := range inputPattern.FindAllString(page, -1) {
		attrs := inputAttributes(input)
		if attrs["name"] == name {
			value, ok := attrs["value"]
			return value, ok
		}
	}
	return "", false
}
func inputAttributes(input string) map[string]string {
	attrs := map[string]string{}
	for _, match := range attributePattern.FindAllStringSubmatch(input, -1) {
		attrs[strings.ToLower(match[1])] = html.UnescapeString(match[2])
	}
	return attrs
}
func checkboxChecked(page, name string) bool {
	for _, input := range inputPattern.FindAllString(page, -1) {
		if inputAttributes(input)["name"] == name {
			return regexp.MustCompile(`(?i)\bchecked(?:\s|=|/|>)`).MatchString(input)
		}
	}
	return false
}
