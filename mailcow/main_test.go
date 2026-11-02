package main

import (
	"html/template"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestMockSettingsAndAliasForms(t *testing.T) {
	state := &appState{settings: &settings{Username: "admin", Password: "secret"}, client: newMockClient(), env: envDevelopment, mock: true}
	settingsHandler := handleSettings(template.Must(template.New("settings").Parse(settingsHTML)), state, filepath.Join(t.TempDir(), "settings.json"), "")
	post := func(h http.HandlerFunc, path string, v url.Values) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(v.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		w := httptest.NewRecorder()
		h(w, req)
		return w
	}
	w := post(settingsHandler, "/settings", url.Values{"username": {"admin"}, "targets": {"alice@example.com\nbob@example.com"}})
	if w.Code != http.StatusSeeOther || state.settings.Password != "secret" || len(state.settings.Targets) != 2 {
		t.Fatalf("settings: status=%d state=%+v", w.Code, state.settings)
	}
	create := handleCreate(template.Must(template.New("create").Parse(createHTML)), state)
	w = httptest.NewRecorder()
	create(w, httptest.NewRequest(http.MethodGet, "/create", nil))
	if !strings.Contains(w.Body.String(), "alice@example.com") {
		t.Fatal("suggestion missing from create form")
	}
	w = post(create, "/create", url.Values{"address": {"new@example.com"}, "goto": {"alice@example.com, custom@example.com"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("create: %d %s", w.Code, w.Body.String())
	}
	edit := handleEdit(template.Must(template.New("edit").Parse(editHTML)), state)
	w = httptest.NewRecorder()
	edit(w, httptest.NewRequest(http.MethodGet, "/edit?address=new%40example.com", nil))
	if !strings.Contains(w.Body.String(), "custom@example.com") || !strings.Contains(w.Body.String(), "bob@example.com") {
		t.Fatal("existing targets or suggestions missing from edit form")
	}
	w = post(edit, "/edit", url.Values{"address": {"new@example.com"}, "goto": {"bob@example.com"}})
	if w.Code != http.StatusSeeOther {
		t.Fatalf("edit: %d", w.Code)
	}
	alias, err := state.client.GetAlias(t.Context(), "new@example.com")
	if err != nil || alias.Goto != "bob@example.com" {
		t.Fatalf("edited alias: %+v %v", alias, err)
	}
}
