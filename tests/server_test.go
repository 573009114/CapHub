package tests

import (
	"bytes"
	"caphub/internal/caphub"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestActionSkillAuditFlow(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))

	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	adminID := getAdminID(t, ts.URL)
	headers := map[string]string{"X-User-Id": adminID}

	actionPayload := map[string]any{
		"name":        "create_ticket",
		"description": "创建工单",
		"method":      "POST",
		"url":         upstream.URL,
		"headers":     map[string]string{},
		"auth_config": map[string]any{},
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"title": map[string]any{"type": "string"}},
			"required":   []any{"title"},
		},
		"output_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
			"required":   []any{"ok"},
		},
		"risk_level": "medium",
		"tags":       []string{"工单"},
	}
	register := doJSON(t, http.MethodPost, ts.URL+"/api/actions/register", headers, actionPayload)
	if register.StatusCode != 200 {
		t.Fatalf("register status=%d", register.StatusCode)
	}
	var action map[string]any
	decodeJSON(t, register, &action)
	actionID := action["id"].(string)

	verify := doJSON(t, http.MethodPost, ts.URL+"/api/actions/"+actionID+"/verify", headers, nil)
	if verify.StatusCode != 200 {
		t.Fatalf("verify status=%d", verify.StatusCode)
	}
	activate := doJSON(t, http.MethodPost, ts.URL+"/api/actions/"+actionID+"/activate", headers, nil)
	if activate.StatusCode != 200 {
		t.Fatalf("activate status=%d", activate.StatusCode)
	}

	skills := doJSON(t, http.MethodGet, ts.URL+"/api/skills?name=create_ticket&tag=工单", headers, nil)
	if skills.StatusCode != 200 {
		t.Fatalf("skills status=%d", skills.StatusCode)
	}
	var skillList []map[string]any
	decodeJSON(t, skills, &skillList)
	if len(skillList) == 0 {
		t.Fatal("expected skills")
	}
	skillID := skillList[0]["id"].(string)

	execResp := doJSON(t, http.MethodPost, ts.URL+"/api/skills/"+skillID+"/execute", headers, map[string]any{
		"input": map[string]any{"title": "bug"},
	})
	if execResp.StatusCode != 200 {
		t.Fatalf("execute status=%d", execResp.StatusCode)
	}
	var execBody map[string]any
	decodeJSON(t, execResp, &execBody)
	if execBody["status"] != "success" {
		t.Fatalf("unexpected status %+v", execBody)
	}

	logs := doJSON(t, http.MethodGet, ts.URL+"/api/audit_logs", headers, nil)
	if logs.StatusCode != 200 {
		t.Fatalf("audit status=%d", logs.StatusCode)
	}
	var audit []map[string]any
	decodeJSON(t, logs, &audit)
	found := false
	for _, row := range audit {
		if row["action"] == "skill.execute" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("skill.execute log not found")
	}
}

func getAdminID(t *testing.T, base string) string {
	resp, err := http.Get(base + "/api/bootstrap/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	return body["admin_user_id"]
}

func doJSON(t *testing.T, method, url string, headers map[string]string, payload any) *http.Response {
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

func decodeJSON(t *testing.T, resp *http.Response, out any) {
	defer resp.Body.Close()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatal(err)
	}
}

func TestStorePersistsAcrossRestart(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store1 := caphub.NewStore()
	srv1 := caphub.NewServer(store1)
	ts1 := httptest.NewServer(srv1.Handler())
	adminID := getAdminID(t, ts1.URL)
	headers := map[string]string{"X-User-Id": adminID}

	register := doJSON(t, http.MethodPost, ts1.URL+"/api/actions/register", headers, map[string]any{
		"name":        "persist_action",
		"description": "persist",
		"method":      "POST",
		"url":         upstream.URL,
		"headers":     map[string]string{},
		"auth_config": map[string]any{},
		"input_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"title": map[string]any{"type": "string"}},
			"required":   []any{"title"},
		},
		"output_schema": map[string]any{
			"type":       "object",
			"properties": map[string]any{"ok": map[string]any{"type": "boolean"}},
			"required":   []any{"ok"},
		},
		"risk_level": "medium",
		"tags":       []string{"persist"},
	})
	if register.StatusCode != 200 {
		t.Fatalf("register status=%d", register.StatusCode)
	}
	var action map[string]any
	decodeJSON(t, register, &action)
	actionID := action["id"].(string)
	ts1.Close()

	store2 := caphub.NewStore()
	srv2 := caphub.NewServer(store2)
	ts2 := httptest.NewServer(srv2.Handler())
	defer ts2.Close()

	verify := doJSON(t, http.MethodPost, ts2.URL+"/api/actions/"+actionID+"/verify", headers, nil)
	if verify.StatusCode != 200 {
		t.Fatalf("verify after restart status=%d", verify.StatusCode)
	}
}

func TestBearerTokenAuthFlow(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	adminID := getAdminID(t, ts.URL)
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/auth/token", map[string]string{}, map[string]any{"user_id": adminID})
	if resp.StatusCode != 200 {
		t.Fatalf("issue token status=%d", resp.StatusCode)
	}
	var tokenBody map[string]any
	decodeJSON(t, resp, &tokenBody)
	token, _ := tokenBody["access_token"].(string)
	if token == "" {
		t.Fatal("missing access_token")
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
	if res.StatusCode != 200 {
		t.Fatalf("skills with bearer status=%d", res.StatusCode)
	}
}

func TestOpenAPIImportCreatesActions(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	adminID := getAdminID(t, ts.URL)
	headers := map[string]string{"X-User-Id": adminID}
	openapi := map[string]any{
		"openapi": "3.0.0",
		"paths": map[string]any{
			"/tickets": map[string]any{
				"post": map[string]any{
					"operationId": "create_ticket_from_openapi",
					"summary":     "create ticket",
					"requestBody": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}, "required": []any{"title"}}}}},
					"responses":   map[string]any{"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}}}}}},
				},
			},
		},
	}
	resp := doJSON(t, http.MethodPost, ts.URL+"/api/actions/import/openapi", headers, map[string]any{"document": openapi, "risk_level": "medium"})
	if resp.StatusCode != 200 {
		t.Fatalf("import status=%d", resp.StatusCode)
	}

	skills := doJSON(t, http.MethodGet, ts.URL+"/api/skills?name=create_ticket_from_openapi", headers, nil)
	if skills.StatusCode != 200 {
		t.Fatalf("skills status=%d", skills.StatusCode)
	}
	var skillList []map[string]any
	decodeJSON(t, skills, &skillList)
	if len(skillList) != 0 {
		t.Fatal("import should create actions only before activation")
	}

	logs := doJSON(t, http.MethodGet, ts.URL+"/api/audit_logs", headers, nil)
	if logs.StatusCode != 200 {
		t.Fatalf("audit status=%d", logs.StatusCode)
	}
	var audit []map[string]any
	decodeJSON(t, logs, &audit)
	found := false
	for _, row := range audit {
		if row["action"] == "action.import.openapi" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("missing openapi import audit log")
	}
}

func TestWorkflowSkillExecuteWithRetry(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	adminID := getAdminID(t, ts.URL)
	headers := map[string]string{"X-User-Id": adminID}

	register := doJSON(t, http.MethodPost, ts.URL+"/api/actions/register", headers, map[string]any{
		"name":          "step_action",
		"description":   "step",
		"method":        "POST",
		"url":           upstream.URL,
		"headers":       map[string]string{},
		"auth_config":   map[string]any{},
		"input_schema":  map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}, "required": []any{"title"}},
		"output_schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}},
		"risk_level":    "medium",
		"tags":          []string{"wf"},
	})
	if register.StatusCode != 200 {
		t.Fatalf("register status=%d", register.StatusCode)
	}
	var action map[string]any
	decodeJSON(t, register, &action)
	actionID := action["id"].(string)

	wf := doJSON(t, http.MethodPost, ts.URL+"/api/skills/workflow", headers, map[string]any{
		"name":        "workflow_ticket",
		"description": "wf",
		"risk_level":  "medium",
		"steps":       []any{map[string]any{"name": "create", "action_id": actionID, "input_key": "create", "retry": 1}},
	})
	if wf.StatusCode != 200 {
		t.Fatalf("workflow create status=%d", wf.StatusCode)
	}
	var wfSkill map[string]any
	decodeJSON(t, wf, &wfSkill)
	skillID := wfSkill["id"].(string)

	execResp := doJSON(t, http.MethodPost, ts.URL+"/api/skills/"+skillID+"/execute", headers, map[string]any{
		"input": map[string]any{"create": map[string]any{"title": "bug"}},
	})
	if execResp.StatusCode != 200 {
		t.Fatalf("execute workflow status=%d", execResp.StatusCode)
	}
	var body map[string]any
	decodeJSON(t, execResp, &body)
	if body["status"] != "success" {
		t.Fatalf("unexpected workflow status: %+v", body)
	}
	if calls < 2 {
		t.Fatalf("expected retry call, got %d", calls)
	}
}

func TestExecuteSkillIdempotencyKey(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	calls := 0
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	adminID := getAdminID(t, ts.URL)
	headers := map[string]string{"X-User-Id": adminID}

	skillID := createActiveAtomicSkill(t, ts.URL, headers, upstream.URL)

	baselineCalls := calls
	req1, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/skills/"+skillID+"/execute", bytes.NewBufferString(`{"input":{"title":"bug"}}`))
	req1.Header.Set("Content-Type", "application/json")
	req1.Header.Set("X-User-Id", adminID)
	req1.Header.Set("Idempotency-Key", "idem-1")
	res1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	defer res1.Body.Close()
	if res1.StatusCode != 200 {
		t.Fatalf("first exec status=%d", res1.StatusCode)
	}

	req2, _ := http.NewRequest(http.MethodPost, ts.URL+"/api/skills/"+skillID+"/execute", bytes.NewBufferString(`{"input":{"title":"bug"}}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("X-User-Id", adminID)
	req2.Header.Set("Idempotency-Key", "idem-1")
	res2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	defer res2.Body.Close()
	if res2.StatusCode != 200 {
		t.Fatalf("second exec status=%d", res2.StatusCode)
	}
	if calls != baselineCalls+1 {
		t.Fatalf("expected one additional upstream call due to idempotency, got total=%d baseline=%d", calls, baselineCalls)
	}
}

func TestExecuteSkillRateLimit(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	t.Setenv("TENANT_QUOTA_QPS", "1")
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	adminID := getAdminID(t, ts.URL)
	headers := map[string]string{"X-User-Id": adminID}

	skillID := createActiveAtomicSkill(t, ts.URL, headers, upstream.URL)

	first := doJSON(t, http.MethodPost, ts.URL+"/api/skills/"+skillID+"/execute", headers, map[string]any{"input": map[string]any{"title": "bug"}})
	if first.StatusCode != 200 {
		t.Fatalf("first status=%d", first.StatusCode)
	}
	second := doJSON(t, http.MethodPost, ts.URL+"/api/skills/"+skillID+"/execute", headers, map[string]any{"input": map[string]any{"title": "bug"}})
	if second.StatusCode != 429 {
		t.Fatalf("second status=%d", second.StatusCode)
	}
}

func createActiveAtomicSkill(t *testing.T, base string, headers map[string]string, url string) string {
	register := doJSON(t, http.MethodPost, base+"/api/actions/register", headers, map[string]any{
		"name":          "rate_action",
		"description":   "desc",
		"method":        "POST",
		"url":           url,
		"headers":       map[string]string{},
		"auth_config":   map[string]any{},
		"input_schema":  map[string]any{"type": "object", "properties": map[string]any{"title": map[string]any{"type": "string"}}, "required": []any{"title"}},
		"output_schema": map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}},
		"risk_level":    "medium",
		"tags":          []string{"x"},
	})
	if register.StatusCode != 200 {
		t.Fatalf("register status=%d", register.StatusCode)
	}
	var action map[string]any
	decodeJSON(t, register, &action)
	actionID := action["id"].(string)
	verify := doJSON(t, http.MethodPost, base+"/api/actions/"+actionID+"/verify", headers, nil)
	if verify.StatusCode != 200 {
		t.Fatalf("verify status=%d", verify.StatusCode)
	}
	activate := doJSON(t, http.MethodPost, base+"/api/actions/"+actionID+"/activate", headers, nil)
	if activate.StatusCode != 200 {
		t.Fatalf("activate status=%d", activate.StatusCode)
	}
	skills := doJSON(t, http.MethodGet, base+"/api/skills?name=rate_action", headers, nil)
	if skills.StatusCode != 200 {
		t.Fatalf("skills status=%d", skills.StatusCode)
	}
	var list []map[string]any
	decodeJSON(t, skills, &list)
	if len(list) == 0 {
		t.Fatal("no skills")
	}
	return list[0]["id"].(string)
}

func TestInitDBEndpoint(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	adminID := getAdminID(t, ts.URL)
	headers := map[string]string{"X-User-Id": adminID}

	noForce := doJSON(t, http.MethodPost, ts.URL+"/api/bootstrap/init_db", headers, map[string]any{"force": false})
	if noForce.StatusCode != 400 {
		t.Fatalf("expected 400 for non-force init, got %d", noForce.StatusCode)
	}

	force := doJSON(t, http.MethodPost, ts.URL+"/api/bootstrap/init_db", headers, map[string]any{"force": true})
	if force.StatusCode != 200 {
		t.Fatalf("expected 200 for force init, got %d", force.StatusCode)
	}

	newAdminID := getAdminID(t, ts.URL)
	if newAdminID == "" || newAdminID == adminID {
		t.Fatal("expected admin to be reinitialized with new id")
	}
}

func TestAPIV1AliasAndCORS(t *testing.T) {
	t.Setenv("DATA_FILE", filepath.Join(t.TempDir(), "caphub-data.json"))
	store := caphub.NewStore()
	srv := caphub.NewServer(store)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/api/v1/bootstrap/admin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("v1 bootstrap status=%d", resp.StatusCode)
	}

	req, err := http.NewRequest(http.MethodOptions, ts.URL+"/api/v1/skills", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Origin", "http://localhost:5173")
	corsResp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer corsResp.Body.Close()
	if corsResp.StatusCode != http.StatusNoContent {
		t.Fatalf("cors preflight status=%d", corsResp.StatusCode)
	}
	if corsResp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatalf("unexpected cors origin header: %q", corsResp.Header.Get("Access-Control-Allow-Origin"))
	}
}
