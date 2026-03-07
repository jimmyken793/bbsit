package web

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/kingyoung/bbsit/internal/db"
	"github.com/kingyoung/bbsit/internal/deployer"
	"github.com/kingyoung/bbsit/internal/scheduler"
)

func testServer(t *testing.T) (*Server, *httptest.Server) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	database, err := db.Open(dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { database.Close() })

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	dep := deployer.New(database, logger)
	sched := scheduler.New(database, dep, logger)
	stackRoot := t.TempDir()

	srv := NewServer(database, dep, sched, logger, stackRoot)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	return srv, ts
}

// setupAuth creates a password and returns a session cookie.
func setupAuth(t *testing.T, ts *httptest.Server) *http.Cookie {
	t.Helper()
	// Setup password
	body := `{"password":"testpassword123"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatalf("setup request: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("setup: want 200, got %d", resp.StatusCode)
	}

	// Extract session cookie
	for _, c := range resp.Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	t.Fatal("no session cookie after setup")
	return nil
}

func authedRequest(t *testing.T, ts *httptest.Server, cookie *http.Cookie, method, path string, body string) *http.Response {
	t.Helper()
	var bodyReader io.Reader
	if body != "" {
		bodyReader = bytes.NewBufferString(body)
	}
	req, err := http.NewRequest(method, ts.URL+path, bodyReader)
	if err != nil {
		t.Fatalf("create request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	req.AddCookie(cookie)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	return resp
}

func readJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer resp.Body.Close()
	var result map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return result
}

// --- Auth tests ---

func TestAuthStatus_InitialState(t *testing.T) {
	_, ts := testServer(t)
	resp, err := ts.Client().Get(ts.URL + "/api/auth/status")
	if err != nil {
		t.Fatal(err)
	}
	result := readJSON(t, resp)

	if result["setup_required"] != true {
		t.Errorf("expected setup_required=true, got %v", result["setup_required"])
	}
	if result["logged_in"] != false {
		t.Errorf("expected logged_in=false, got %v", result["logged_in"])
	}
}

func TestSetup_Success(t *testing.T) {
	_, ts := testServer(t)
	body := `{"password":"testpassword123"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	// Should have session cookie
	hasCookie := false
	for _, c := range resp.Cookies() {
		if c.Name == "session" {
			hasCookie = true
		}
	}
	if !hasCookie {
		t.Error("expected session cookie after setup")
	}
}

func TestSetup_TooShort(t *testing.T) {
	_, ts := testServer(t)
	body := `{"password":"short"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestSetup_AlreadyDone(t *testing.T) {
	_, ts := testServer(t)
	setupAuth(t, ts)

	body := `{"password":"anotherpassword"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/setup", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
}

func TestLogin_Success(t *testing.T) {
	_, ts := testServer(t)
	setupAuth(t, ts)

	body := `{"password":"testpassword123"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/login", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
}

func TestLogin_WrongPassword(t *testing.T) {
	_, ts := testServer(t)
	setupAuth(t, ts)

	body := `{"password":"wrongpassword"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/login", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

// --- Auth middleware ---

func TestProtectedRoute_NoAuth(t *testing.T) {
	_, ts := testServer(t)
	resp, err := ts.Client().Get(ts.URL + "/api/projects")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", resp.StatusCode)
	}
}

// --- Project CRUD ---

func TestCreateProject(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	project := `{"id":"test-app","display_name":"Test App","config_mode":"form","registry_image":"reg/app","image_tag":"latest","health_type":"none","poll_interval":300,"enabled":true}`
	resp := authedRequest(t, ts, cookie, "POST", "/api/projects", project)

	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()
}

func TestCreateProject_InvalidID(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	project := `{"id":"INVALID","config_mode":"form"}`
	resp := authedRequest(t, ts, cookie, "POST", "/api/projects", project)

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCreateProject_DuplicateID(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	project := `{"id":"dup-app","config_mode":"form","registry_image":"reg/app"}`
	resp := authedRequest(t, ts, cookie, "POST", "/api/projects", project)
	resp.Body.Close()

	resp = authedRequest(t, ts, cookie, "POST", "/api/projects", project)
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("want 409, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestListProjects(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	// Empty list
	resp := authedRequest(t, ts, cookie, "GET", "/api/projects", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var projects []any
	json.NewDecoder(resp.Body).Decode(&projects)
	resp.Body.Close()
	if len(projects) != 0 {
		t.Fatalf("expected empty list, got %d", len(projects))
	}

	// Create one
	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"list-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	// Should have one
	resp = authedRequest(t, ts, cookie, "GET", "/api/projects", "")
	json.NewDecoder(resp.Body).Decode(&projects)
	resp.Body.Close()
	if len(projects) != 1 {
		t.Fatalf("expected 1 project, got %d", len(projects))
	}
}

func TestGetProject(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"get-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "GET", "/api/projects/get-app", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	result := readJSON(t, resp)
	project := result["project"].(map[string]any)
	if project["id"] != "get-app" {
		t.Fatalf("expected id=get-app, got %v", project["id"])
	}
}

func TestGetProject_NotFound(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "GET", "/api/projects/nonexistent", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUpdateProject(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"upd-app","config_mode":"form","registry_image":"reg/app","display_name":"Old"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "PUT", "/api/projects/upd-app", `{"display_name":"New","config_mode":"form","registry_image":"reg/app"}`)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	result := readJSON(t, resp)
	if result["display_name"] != "New" {
		t.Fatalf("expected display_name=New, got %v", result["display_name"])
	}
}

func TestDeleteProject(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"del-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "DELETE", "/api/projects/del-app", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Verify deleted
	resp = authedRequest(t, ts, cookie, "GET", "/api/projects/del-app", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404 after delete, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestLogout(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	// Authed request works
	resp := authedRequest(t, ts, cookie, "GET", "/api/projects", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 before logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Logout
	resp = authedRequest(t, ts, cookie, "POST", "/api/auth/logout", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 for logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Same cookie should no longer work
	resp = authedRequest(t, ts, cookie, "GET", "/api/projects", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 after logout, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Action endpoints ---

func TestDeploy_Accepted(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"deploy-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects/deploy-app/deploy", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRollback_Accepted(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"rb-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects/rb-app/rollback", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestRollback_NotFound(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects/nope/rollback", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStop_Accepted(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"stop-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects/stop-app/stop", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStart_Accepted(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"start-app","config_mode":"form","registry_image":"reg/app"}`).Body.Close()

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects/start-app/start", "")
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("want 202, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestStart_NotFound(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects/nope/start", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Import ---

func TestImportProject_YAML(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	yamlBody := `id: imported-app
display_name: Imported App
config_mode: form
registry_image: reg/imported
image_tag: latest
health_type: none
poll_interval: 300
enabled: true`

	req, _ := http.NewRequest("POST", ts.URL+"/api/projects/import", bytes.NewBufferString(yamlBody))
	req.Header.Set("Content-Type", "application/x-yaml")
	req.AddCookie(cookie)
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 200, got %d: %s", resp.StatusCode, body)
	}
	result := readJSON(t, resp)
	if result["id"] != "imported-app" {
		t.Fatalf("expected id=imported-app, got %v", result["id"])
	}
}

func TestImportProject_InvalidYAML(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	req, _ := http.NewRequest("POST", ts.URL+"/api/projects/import", bytes.NewBufferString(":::"))
	req.Header.Set("Content-Type", "application/x-yaml")
	req.AddCookie(cookie)
	resp, _ := ts.Client().Do(req)

	// Missing ID triggers validation error
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestImportProject_Upsert(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	yaml1 := `id: upsert-app
displayname: Version 1
config_mode: form
registry_image: reg/app`

	yaml2 := `id: upsert-app
displayname: Version 2
config_mode: form
registry_image: reg/app`

	// First import creates
	req, _ := http.NewRequest("POST", ts.URL+"/api/projects/import", bytes.NewBufferString(yaml1))
	req.Header.Set("Content-Type", "application/x-yaml")
	req.AddCookie(cookie)
	resp, _ := ts.Client().Do(req)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("first import: want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// Second import updates (upsert)
	req, _ = http.NewRequest("POST", ts.URL+"/api/projects/import", bytes.NewBufferString(yaml2))
	req.Header.Set("Content-Type", "application/x-yaml")
	req.AddCookie(cookie)
	resp, _ = ts.Client().Do(req)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("second import: want 200, got %d: %s", resp.StatusCode, body)
	}
	resp.Body.Close()

	// Verify project still exists (upsert succeeded)
	resp = authedRequest(t, ts, cookie, "GET", "/api/projects/upsert-app", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

// --- Validation edge cases ---

func TestCreateProject_EmptyID(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"","config_mode":"form"}`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCreateProject_InvalidJSON(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects", `not json`)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestCreateProject_DefaultValues(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "POST", "/api/projects", `{"id":"defaults-app","config_mode":"form","registry_image":"reg/app"}`)
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("want 201, got %d: %s", resp.StatusCode, body)
	}
	result := readJSON(t, resp)

	// Defaults should be applied
	if result["image_tag"] != "latest" {
		t.Errorf("expected default image_tag=latest, got %v", result["image_tag"])
	}
	poll, _ := result["poll_interval"].(float64)
	if poll != 300 {
		t.Errorf("expected default poll_interval=300, got %v", result["poll_interval"])
	}
	if result["stack_path"] == "" {
		t.Error("expected stack_path to be auto-set")
	}
}

func TestLogin_NotSetup(t *testing.T) {
	_, ts := testServer(t)

	body := `{"password":"testpassword123"}`
	resp, err := ts.Client().Post(ts.URL+"/api/auth/login", "application/json", bytes.NewBufferString(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusPreconditionFailed {
		t.Fatalf("want 412, got %d", resp.StatusCode)
	}
}

func TestLogin_InvalidJSON(t *testing.T) {
	_, ts := testServer(t)
	setupAuth(t, ts)

	resp, _ := ts.Client().Post(ts.URL+"/api/auth/login", "application/json", bytes.NewBufferString("not json"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

func TestAuthStatus_AfterSetup(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	req, _ := http.NewRequest("GET", ts.URL+"/api/auth/status", nil)
	req.AddCookie(cookie)
	resp, _ := ts.Client().Do(req)
	result := readJSON(t, resp)

	if result["setup_required"] != false {
		t.Errorf("expected setup_required=false, got %v", result["setup_required"])
	}
	if result["logged_in"] != true {
		t.Errorf("expected logged_in=true, got %v", result["logged_in"])
	}
}

func TestDeleteProject_NotFound(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "DELETE", "/api/projects/nonexistent", "")
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("want 404, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestUpdateProject_InvalidJSON(t *testing.T) {
	_, ts := testServer(t)
	cookie := setupAuth(t, ts)

	resp := authedRequest(t, ts, cookie, "PUT", "/api/projects/some-app", "bad json")
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}

func TestProtectedRoute_ExpiredSession(t *testing.T) {
	srv, ts := testServer(t)
	cookie := setupAuth(t, ts)

	// Manually expire the session
	srv.sessions.Range(func(key, _ any) bool {
		srv.sessions.Delete(key)
		return true
	})

	resp := authedRequest(t, ts, cookie, "GET", "/api/projects", "")
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 for expired session, got %d", resp.StatusCode)
	}
	resp.Body.Close()
}
