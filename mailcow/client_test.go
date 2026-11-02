package main

import (
	"context"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestParseTableColumns(t *testing.T) {
	page := `
<table id="aliastable">
  <thead>
    <tr><th>Alias address</th><th>Goto</th><th>Active</th></tr>
  </thead>
  <tbody>
    <tr>
      <td>alias@example.com</td>
      <td>user1@example.com<br>user2@example.com</td>
      <td>yes</td>
    </tr>
    <tr>
      <td>single@example.com</td>
      <td>user@example.com</td>
      <td>yes</td>
    </tr>
    <tr>
      <td>-</td>
      <td></td>
      <td>no</td>
    </tr>
  </tbody>
</table>
`

	rows, err := parseTableColumns(page, "aliastable", 2)
	if err != nil {
		t.Fatalf("parseTableColumns: %v", err)
	}

	want := [][]string{
		{"alias@example.com", "user1@example.com user2@example.com"},
		{"single@example.com", "user@example.com"},
	}

	if !reflect.DeepEqual(rows, want) {
		t.Errorf("parseTableColumns = %v, want %v", rows, want)
	}
}

func TestParseTableColumnsEmpty(t *testing.T) {
	page := `<table id="aliastable"><tbody></tbody></table>`

	rows, err := parseTableColumns(page, "aliastable", 2)
	if err != nil {
		t.Fatalf("parseTableColumns: %v", err)
	}

	if len(rows) != 0 {
		t.Errorf("expected 0 rows, got %d", len(rows))
	}
}

func TestFormFields(t *testing.T) {
	page := `<form><input value="abc&amp;123" type="hidden" name="csrf"><input checked name="is_sogo_alias" type="checkbox"><input name="active" type="checkbox"></form>`
	if v, ok := inputValue(page, "csrf"); !ok || v != "abc&123" {
		t.Fatalf("csrf = %q, %v", v, ok)
	}
	if !checkboxChecked(page, "is_sogo_alias") || checkboxChecked(page, "active") {
		t.Fatal("checkbox state not preserved")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestAliasFormRequests(t *testing.T) {
	for _, tc := range []struct {
		name, action, formPath, postPath, formHTML string
		run                                        func(*Client) error
	}{
		{"create", "addalias", "/add/alias", "/add/alias", `<input name="csrf" value="token">`, func(c *Client) error {
			return c.CreateAlias(context.Background(), []string{"alias@example.com"}, []string{"user@example.com"}, true)
		}},
		{"edit", "editalias", "/edit/alias/alias@example.com", "/edit/alias/alias@example.com", `<input name="csrf" value="token"><input name="address" value="alias@example.com"><input name="active" type="checkbox"><input name="is_sogo_alias" checked>`, func(c *Client) error {
			return c.EditAlias(context.Background(), "alias@example.com", []string{"user@example.com"})
		}},
		{"delete", "deletealias", "/delete/alias/alias@example.com", "/manager", `<input name="csrf" value="token"><input name="address" value="alias@example.com">`, func(c *Client) error { return c.DeleteAlias(context.Background(), "alias@example.com") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			c := &Client{loggedInAt: time.Now(), httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.formHTML
				if calls == 1 {
					if r.Method != "GET" || r.URL.Path != tc.formPath {
						t.Errorf("form request: %s %s", r.Method, r.URL.Path)
					}
				} else {
					if r.Method != "POST" || r.URL.Path != tc.postPath {
						t.Errorf("submit request: %s %s", r.Method, r.URL.Path)
					}
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					if r.Form.Get("csrf") != "token" || r.Form.Get("trigger_mailbox_action") != tc.action {
						t.Errorf("unexpected submitted form: %v", r.Form)
					}
					if tc.name == "edit" && (r.Form.Has("active") || r.Form.Get("is_sogo_alias") != "on") {
						t.Errorf("flags changed: %v", r.Form)
					}
					body = `<div id="alert-fade" class="alert alert-success">Saved</div>`
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
			})}}
			if err := tc.run(c); err != nil {
				t.Fatal(err)
			}
			if calls != 2 {
				t.Errorf("got %d requests", calls)
			}
		})
	}
}

func TestCreateRejectsMissingSuccess(t *testing.T) {
	calls := 0
	c := &Client{loggedInAt: time.Now(), httpClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		body := `<input name="csrf" value="token">`
		if calls == 2 {
			body = "unexpected response"
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header), Request: r}, nil
	})}}
	if err := c.CreateAlias(context.Background(), []string{"alias@example.com"}, []string{"user@example.com"}, true); err == nil {
		t.Fatal("missing success must fail")
	}
}
