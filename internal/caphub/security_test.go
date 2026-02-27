package caphub

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
)

func TestRBACForbiddenOnActionRegister(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := NewStore()

	store.mu.Lock()
	ten := store.tenants[store.users[store.adminID].TenantID]
	limitedUser := &User{ID: newID(), TenantID: ten.ID, Username: "reader", IsActive: true, Permissions: map[string]bool{"action:read": true}}
	store.users[limitedUser.ID] = limitedUser
	_ = store.persistLocked()
	store.mu.Unlock()

	srv := NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	payload := map[string]any{
		"name":         "forbidden_register",
		"description":  "forbidden",
		"method":       "POST",
		"url":          ts.URL,
		"headers":      map[string]string{},
		"auth_config":  map[string]any{},
		"input_schema": map[string]any{"type": "object"},
		"output_schema": map[string]any{
			"type": "object",
		},
		"risk_level": "low",
	}
	resp := doJSONRequest(t, http.MethodPost, ts.URL+"/api/actions/register", map[string]string{"X-User-Id": limitedUser.ID}, payload)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403, got %d", resp.StatusCode)
	}
}

func TestCrossTenantIsolationForSkillExecuteAndAudit(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := NewStore()

	store.mu.Lock()
	admin := store.users[store.adminID]
	tenantA := store.tenants[admin.TenantID]
	tenantB := &Tenant{ID: newID(), Name: "tenant-b", QuotaQPS: 100}
	store.tenants[tenantB.ID] = tenantB
	userB := &User{ID: newID(), TenantID: tenantB.ID, Username: "publisher-b", IsActive: true, Permissions: map[string]bool{"action:write": true, "action:read": true, "skill:execute": true, "audit:read": true}}
	store.users[userB.ID] = userB

	actionB := &Action{ID: newID(), TenantID: tenantB.ID, OwnerID: userB.ID, Name: "tenant_b_action", Description: "b action", Method: "POST", URL: "http://example.invalid", Headers: map[string]string{}, AuthConfig: map[string]any{}, InputSchema: map[string]any{"type": "object"}, OutputSchema: map[string]any{"type": "object"}, Tags: []string{"b"}, RiskLevel: RiskLow, Status: ActionActive, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	store.actions[actionB.ID] = actionB
	skillB := &Skill{ID: newID(), TenantID: tenantB.ID, ActionID: actionB.ID, Name: "atomic::tenant_b_action", Description: "b skill", SkillType: SkillAtomic, RiskLevel: RiskLow, Enabled: true, CreatedAt: time.Now().UTC()}
	store.skills[skillB.ID] = skillB
	store.auditLogs = append(store.auditLogs, &AuditLog{ID: newID(), TenantID: tenantB.ID, UserID: userB.ID, Action: "skill.execute", ResourceType: "skill", ResourceID: skillB.ID, TraceID: newID(), Details: map[string]any{"status": "success"}, CreatedAt: time.Now().UTC()})
	_ = store.persistLocked()
	store.mu.Unlock()

	srv := NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	// Tenant A user cannot see tenant B skills.
	skillsResp := doJSONRequest(t, http.MethodGet, ts.URL+"/api/skills?name=tenant_b_action", map[string]string{"X-User-Id": admin.ID}, nil)
	if skillsResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", skillsResp.StatusCode)
	}
	var skills []map[string]any
	decodeResponseJSON(t, skillsResp, &skills)
	if len(skills) != 0 {
		t.Fatalf("expected tenant A to see 0 skills from tenant B, got %d", len(skills))
	}

	// Tenant A user cannot execute tenant B skill directly by id.
	execResp := doJSONRequest(t, http.MethodPost, ts.URL+"/api/skills/"+skillB.ID+"/execute", map[string]string{"X-User-Id": admin.ID}, map[string]any{"input": map[string]any{}})
	if execResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for cross-tenant execute, got %d", execResp.StatusCode)
	}

	// Tenant A user cannot query tenant B logs.
	auditResp := doJSONRequest(t, http.MethodGet, ts.URL+"/api/audit_logs?tenant_id="+tenantB.ID, map[string]string{"X-User-Id": admin.ID}, nil)
	if auditResp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected 403 for cross-tenant audit query, got %d", auditResp.StatusCode)
	}

	// tenant A can still query own tenant logs endpoint.
	ownAuditResp := doJSONRequest(t, http.MethodGet, ts.URL+"/api/audit_logs?tenant_id="+tenantA.ID, map[string]string{"X-User-Id": admin.ID}, nil)
	if ownAuditResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 for own tenant audit query, got %d", ownAuditResp.StatusCode)
	}
}

func TestAuthRegisterAndLoginFlow(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := NewStore()
	srv := NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	regResp := doJSONRequest(t, http.MethodPost, ts.URL+"/api/auth/register", nil, map[string]any{"username": "alice", "password": "pass123"})
	if regResp.StatusCode != http.StatusOK {
		t.Fatalf("register status=%d", regResp.StatusCode)
	}
	var regBody map[string]any
	decodeResponseJSON(t, regResp, &regBody)
	if regBody["access_token"] == "" {
		t.Fatal("expected access_token on register")
	}

	loginResp := doJSONRequest(t, http.MethodPost, ts.URL+"/api/auth/login", nil, map[string]any{"username": "alice", "password": "pass123"})
	if loginResp.StatusCode != http.StatusOK {
		t.Fatalf("login status=%d", loginResp.StatusCode)
	}
	var loginBody map[string]any
	decodeResponseJSON(t, loginResp, &loginBody)
	token, _ := loginBody["access_token"].(string)
	if token == "" {
		t.Fatal("expected access_token on login")
	}

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/api/skills", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 using login token, got %d", res.StatusCode)
	}

	changeReq, err := http.NewRequest(http.MethodPost, ts.URL+"/api/auth/change_password", bytes.NewBufferString(`{"old_password":"pass123","new_password":"pass456"}`))
	if err != nil {
		t.Fatal(err)
	}
	changeReq.Header.Set("Content-Type", "application/json")
	changeReq.Header.Set("Authorization", "Bearer "+token)
	changeRes, err := http.DefaultClient.Do(changeReq)
	if err != nil {
		t.Fatal(err)
	}
	defer changeRes.Body.Close()
	if changeRes.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on change password, got %d", changeRes.StatusCode)
	}

	oldLogin := doJSONRequest(t, http.MethodPost, ts.URL+"/api/auth/login", nil, map[string]any{"username": "alice", "password": "pass123"})
	if oldLogin.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 using old password, got %d", oldLogin.StatusCode)
	}
	_ = oldLogin.Body.Close()

	newLogin := doJSONRequest(t, http.MethodPost, ts.URL+"/api/auth/login", nil, map[string]any{"username": "alice", "password": "pass456"})
	if newLogin.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 using new password, got %d", newLogin.StatusCode)
	}
	_ = newLogin.Body.Close()
}

func doJSONRequest(t *testing.T, method, url string, headers map[string]string, payload any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if payload != nil {
		if err := json.NewEncoder(&buf).Encode(payload); err != nil {
			t.Fatal(err)
		}
	}
	req, err := http.NewRequest(method, url, &buf)
	if err != nil {
		t.Fatal(err)
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func decodeResponseJSON(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}
