package caphub

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

type RiskLevel string
type ActionStatus string
type SkillType string
type ExecutionStatus string

const (
	RiskLow    RiskLevel = "low"
	RiskMedium RiskLevel = "medium"
	RiskHigh   RiskLevel = "high"

	ActionDraft    ActionStatus = "Draft"
	ActionVerified ActionStatus = "Verified"
	ActionActive   ActionStatus = "Active"

	SkillAtomic   SkillType = "atomic"
	SkillWorkflow SkillType = "workflow"

	ExecSuccess ExecutionStatus = "success"
	ExecFailed  ExecutionStatus = "failed"
)

type Tenant struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	QuotaQPS int    `json:"quota_qps"`
}

type User struct {
	ID          string          `json:"id"`
	TenantID    string          `json:"tenant_id"`
	Username    string          `json:"username"`
	Password    string          `json:"password,omitempty"`
	Permissions map[string]bool `json:"permissions"`
	IsActive    bool            `json:"is_active"`
}

type Action struct {
	ID           string            `json:"id"`
	TenantID     string            `json:"tenant_id"`
	OwnerID      string            `json:"owner_id"`
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	AuthConfig   map[string]any    `json:"auth_config"`
	InputSchema  map[string]any    `json:"input_schema"`
	OutputSchema map[string]any    `json:"output_schema"`
	Tags         []string          `json:"tags"`
	RiskLevel    RiskLevel         `json:"risk_level"`
	Status       ActionStatus      `json:"status"`
	Enabled      bool              `json:"is_enabled"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
}

type Skill struct {
	ID          string              `json:"id"`
	TenantID    string              `json:"tenant_id"`
	ActionID    string              `json:"action_id"`
	Name        string              `json:"name"`
	Description string              `json:"description"`
	SkillType   SkillType           `json:"skill_type"`
	RiskLevel   RiskLevel           `json:"risk_level"`
	Enabled     bool                `json:"is_enabled"`
	CreatedAt   time.Time           `json:"created_at"`
	Workflow    *WorkflowDefinition `json:"workflow,omitempty"`
}

type WorkflowStep struct {
	Name             string `json:"name"`
	ActionID         string `json:"action_id"`
	InputKey         string `json:"input_key"`
	Retry            int    `json:"retry"`
	RollbackActionID string `json:"rollback_action_id,omitempty"`
}

type WorkflowDefinition struct {
	Steps []WorkflowStep `json:"steps"`
}

type SkillExecution struct {
	ID         string          `json:"id"`
	TenantID   string          `json:"tenant_id"`
	SkillID    string          `json:"skill_id"`
	UserID     string          `json:"user_id"`
	TraceID    string          `json:"trace_id"`
	Input      map[string]any  `json:"input_payload"`
	Output     map[string]any  `json:"output_payload,omitempty"`
	Status     ExecutionStatus `json:"status"`
	DurationMS int64           `json:"duration_ms"`
	Error      string          `json:"error_message,omitempty"`
	ExecutedAt time.Time       `json:"created_at"`
}

type AuditLog struct {
	ID           string         `json:"id"`
	TenantID     string         `json:"tenant_id"`
	UserID       string         `json:"user_id"`
	Action       string         `json:"action"`
	ResourceType string         `json:"resource_type"`
	ResourceID   string         `json:"resource_id"`
	TraceID      string         `json:"trace_id"`
	Details      map[string]any `json:"details"`
	CreatedAt    time.Time      `json:"created_at"`
}

type snapshot struct {
	Tenants          map[string]*Tenant         `json:"tenants"`
	Users            map[string]*User           `json:"users"`
	Actions          map[string]*Action         `json:"actions"`
	Skills           map[string]*Skill          `json:"skills"`
	Executions       map[string]*SkillExecution `json:"executions"`
	AuditLogs        []*AuditLog                `json:"audit_logs"`
	AdminID          string                     `json:"admin_id"`
	IdempotentResult map[string]map[string]any  `json:"idempotent_result,omitempty"`
}

type Store struct {
	mu               sync.RWMutex
	tenants          map[string]*Tenant
	users            map[string]*User
	actions          map[string]*Action
	skills           map[string]*Skill
	executions       map[string]*SkillExecution
	auditLogs        []*AuditLog
	adminID          string
	dataFile         string
	idempotentResult map[string]map[string]any
	tenantRate       map[string]*tenantRateState
}

type tenantRateState struct {
	WindowStart int64
	Count       int
}

func NewStore() *Store {
	path := os.Getenv("DATA_FILE")
	if path == "" {
		path = "./caphub-data.json"
	}
	s := &Store{dataFile: path}
	if err := s.load(); err != nil {
		s.bootstrapEmpty()
		_ = s.persistLocked()
	}
	if os.Getenv("INIT_DB_ON_START") == "true" {
		_ = s.initialize(true)
	}
	return s
}

func (s *Store) bootstrapEmpty() {
	s.tenants = map[string]*Tenant{}
	s.users = map[string]*User{}
	s.actions = map[string]*Action{}
	s.skills = map[string]*Skill{}
	s.executions = map[string]*SkillExecution{}
	s.auditLogs = []*AuditLog{}
	s.idempotentResult = map[string]map[string]any{}
	s.tenantRate = map[string]*tenantRateState{}

	quota := 2000
	if raw := os.Getenv("TENANT_QUOTA_QPS"); raw != "" {
		if q, err := strconv.Atoi(raw); err == nil && q > 0 {
			quota = q
		}
	}
	tenant := &Tenant{ID: newID(), Name: "default-tenant", QuotaQPS: quota}
	s.tenants[tenant.ID] = tenant
	admin := &User{ID: newID(), TenantID: tenant.ID, Username: "admin", IsActive: true, Permissions: map[string]bool{
		"action:write":  true,
		"action:read":   true,
		"skill:execute": true,
		"audit:read":    true,
	}}
	s.users[admin.ID] = admin
	s.adminID = admin.ID
}

func (s *Store) initialize(force bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !force {
		if len(s.tenants) > 0 || len(s.actions) > 0 || len(s.skills) > 0 || len(s.executions) > 0 || len(s.auditLogs) > 0 {
			return errors.New("database already initialized; set force=true to reset")
		}
	}
	s.bootstrapEmpty()
	return s.persistLocked()
}

func (s *Store) load() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.dataFile)
	if err != nil {
		return err
	}
	var snap snapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return err
	}
	if snap.Tenants == nil || snap.Users == nil || snap.Actions == nil || snap.Skills == nil || snap.Executions == nil {
		return errors.New("invalid snapshot")
	}
	s.tenants = snap.Tenants
	s.users = snap.Users
	s.actions = snap.Actions
	s.skills = snap.Skills
	s.executions = snap.Executions
	s.auditLogs = snap.AuditLogs
	s.adminID = snap.AdminID
	s.idempotentResult = snap.IdempotentResult
	if s.auditLogs == nil {
		s.auditLogs = []*AuditLog{}
	}
	if s.adminID == "" {
		return errors.New("missing admin id")
	}
	if s.idempotentResult == nil {
		s.idempotentResult = map[string]map[string]any{}
	}
	if s.tenantRate == nil {
		s.tenantRate = map[string]*tenantRateState{}
	}
	return nil
}

func (s *Store) persistLocked() error {
	snap := snapshot{
		Tenants:          s.tenants,
		Users:            s.users,
		Actions:          s.actions,
		Skills:           s.skills,
		Executions:       s.executions,
		AuditLogs:        s.auditLogs,
		AdminID:          s.adminID,
		IdempotentResult: s.idempotentResult,
	}
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.dataFile + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.dataFile)
}

type Server struct {
	store     *Store
	client    *http.Client
	engine    *gin.Engine
	jwtSecret []byte
}

func NewServer(store *Store) *Server {
	secret := os.Getenv("JWT_SECRET")
	if secret == "" {
		secret = "dev-secret-change-me"
	}
	s := &Server{store: store, client: &http.Client{Timeout: 5 * time.Second}, engine: gin.Default(), jwtSecret: []byte(secret)}
	s.routes()
	return s
}

func (s *Server) Handler() http.Handler { return s.engine }

func (s *Server) routes() {
	registerGet := func(path string, h http.HandlerFunc) {
		s.engine.GET(path, gin.WrapF(s.withCORS(h)))
		if path != rHealthz {
			s.engine.GET(apiPrefixV1+path[4:], gin.WrapF(s.withCORS(h)))
		}
	}
	registerPost := func(path string, h http.HandlerFunc) {
		s.engine.POST(path, gin.WrapF(s.withCORS(h)))
		s.engine.POST(apiPrefixV1+path[4:], gin.WrapF(s.withCORS(h)))
	}
	registerAny := func(path string, h http.HandlerFunc) {
		s.engine.Any(path, gin.WrapF(s.withCORS(h)))
		s.engine.Any(apiPrefixV1+path[4:], gin.WrapF(s.withCORS(h)))
	}

	registerGet(rHealthz, s.healthz)
	registerGet(rBootstrapAdmin, s.bootstrapAdmin)
	registerPost(rBootstrapInitDB, s.withPerm("action:write", s.initDB))
	registerPost(rAuthRegister, s.registerUser)
	registerPost(rAuthLogin, s.loginUser)
	registerPost(rAuthToken, s.issueToken)
	registerPost(rActionsRegister, s.withPerm("action:write", s.registerAction))
	registerPost(rActionsImport, s.withPerm("action:write", s.importOpenAPI))
	registerAny(rActionsWildcard, s.actionRoutes)
	registerGet(rSkillsQuery, s.auth(s.querySkills))
	registerPost(rSkillsWorkflow, s.withPerm("action:write", s.createWorkflowSkill))
	registerAny(rSkillsWildcard, s.skillRoutes)
	registerGet(rAuditLogs, s.withPerm("audit:read", s.auditLogs))
}

func (s *Server) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "timestamp": time.Now().UTC()})
}

func (s *Server) bootstrapAdmin(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"admin_user_id": s.store.adminID})
}

func (s *Server) initDB(w http.ResponseWriter, r *http.Request, _ *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req initDBRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.initialize(req.Force); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"status": "initialized", "force": req.Force})
}

type authTokenRequest struct {
	UserID string `json:"user_id"`
}

type authRegisterRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type authLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type tokenClaims struct {
	Sub      string `json:"sub"`
	TenantID string `json:"tenant_id"`
	Exp      int64  `json:"exp"`
}

type initDBRequest struct {
	Force bool `json:"force"`
}

func (s *Server) issueToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req authTokenRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.UserID == "" {
		writeError(w, 400, "user_id is required")
		return
	}
	s.store.mu.RLock()
	user := s.store.users[req.UserID]
	s.store.mu.RUnlock()
	if user == nil || !user.IsActive {
		writeError(w, 401, "Invalid user")
		return
	}
	claims := tokenClaims{Sub: user.ID, TenantID: user.TenantID, Exp: time.Now().Add(8 * time.Hour).Unix()}
	token, err := s.signJWT(claims)
	if err != nil {
		writeError(w, 500, "failed to sign token")
		return
	}
	writeJSON(w, 200, map[string]any{"access_token": token, "token_type": "Bearer", "expires_in": 28800})
}

func (s *Server) registerUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req authRegisterRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, 400, "username and password are required")
		return
	}

	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for _, u := range s.store.users {
		if strings.EqualFold(u.Username, req.Username) {
			writeError(w, 409, "username already exists")
			return
		}
	}
	admin := s.store.users[s.store.adminID]
	if admin == nil {
		writeError(w, 500, "admin user not found")
		return
	}

	newUser := &User{
		ID:       newID(),
		TenantID: admin.TenantID,
		Username: req.Username,
		Password: req.Password,
		IsActive: true,
		Permissions: map[string]bool{
			"action:write":  true,
			"action:read":   true,
			"skill:execute": true,
			"audit:read":    true,
		},
	}
	s.store.users[newUser.ID] = newUser
	s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: newUser.TenantID, UserID: newUser.ID, Action: "auth.register", ResourceType: "user", ResourceID: newUser.ID, TraceID: newID(), Details: map[string]any{"username": newUser.Username}, CreatedAt: time.Now().UTC()})
	_ = s.store.persistLocked()

	claims := tokenClaims{Sub: newUser.ID, TenantID: newUser.TenantID, Exp: time.Now().Add(8 * time.Hour).Unix()}
	token, err := s.signJWT(claims)
	if err != nil {
		writeError(w, 500, "failed to sign token")
		return
	}
	writeJSON(w, 200, map[string]any{"user_id": newUser.ID, "username": newUser.Username, "access_token": token, "token_type": "Bearer", "expires_in": 28800})
}

func (s *Server) loginUser(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req authLoginRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	req.Username = strings.TrimSpace(req.Username)
	if req.Username == "" || req.Password == "" {
		writeError(w, 400, "username and password are required")
		return
	}

	s.store.mu.RLock()
	var user *User
	for _, u := range s.store.users {
		if strings.EqualFold(u.Username, req.Username) {
			user = u
			break
		}
	}
	s.store.mu.RUnlock()
	if user == nil || !user.IsActive || user.Password != req.Password {
		writeError(w, 401, "invalid username or password")
		return
	}
	claims := tokenClaims{Sub: user.ID, TenantID: user.TenantID, Exp: time.Now().Add(8 * time.Hour).Unix()}
	token, err := s.signJWT(claims)
	if err != nil {
		writeError(w, 500, "failed to sign token")
		return
	}
	writeJSON(w, 200, map[string]any{"user_id": user.ID, "username": user.Username, "access_token": token, "token_type": "Bearer", "expires_in": 28800})
}

func (s *Server) currentUserFromRequest(r *http.Request) (*User, error) {
	authz := r.Header.Get("Authorization")
	if strings.HasPrefix(authz, "Bearer ") {
		claims, err := s.verifyJWT(strings.TrimPrefix(authz, "Bearer "))
		if err != nil {
			return nil, errors.New("Invalid bearer token")
		}
		s.store.mu.RLock()
		user := s.store.users[claims.Sub]
		s.store.mu.RUnlock()
		if user == nil || !user.IsActive || user.TenantID != claims.TenantID {
			return nil, errors.New("Invalid user")
		}
		return user, nil
	}
	userID := r.Header.Get("X-User-Id")
	if userID == "" {
		return nil, errors.New("Missing Authorization bearer token or X-User-Id")
	}
	s.store.mu.RLock()
	user := s.store.users[userID]
	s.store.mu.RUnlock()
	if user == nil || !user.IsActive {
		return nil, errors.New("Invalid user")
	}
	return user, nil
}

func (s *Server) signJWT(claims tokenClaims) (string, error) {
	header := map[string]string{"alg": "HS256", "typ": "JWT"}
	h, err := json.Marshal(header)
	if err != nil {
		return "", err
	}
	p, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	hEnc := base64.RawURLEncoding.EncodeToString(h)
	pEnc := base64.RawURLEncoding.EncodeToString(p)
	msg := hEnc + "." + pEnc
	mac := hmac.New(sha256.New, s.jwtSecret)
	_, _ = mac.Write([]byte(msg))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return msg + "." + sig, nil
}

func (s *Server) verifyJWT(token string) (*tokenClaims, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return nil, errors.New("invalid token")
	}
	msg := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, s.jwtSecret)
	_, _ = mac.Write([]byte(msg))
	expected := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || !hmac.Equal(got, expected) {
		return nil, errors.New("invalid signature")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, err
	}
	var claims tokenClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return nil, err
	}
	if claims.Sub == "" || claims.TenantID == "" || claims.Exp < time.Now().Unix() {
		return nil, errors.New("token expired or invalid")
	}
	return &claims, nil
}

func (s *Server) auth(next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := s.currentUserFromRequest(r)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		next(w, r, user)
	}
}

func (s *Server) withPerm(perm string, next func(http.ResponseWriter, *http.Request, *User)) http.HandlerFunc {
	return s.auth(func(w http.ResponseWriter, r *http.Request, u *User) {
		if !u.Permissions[perm] {
			writeError(w, http.StatusForbidden, "Missing permission: "+perm)
			return
		}
		next(w, r, u)
	})
}

type openAPIImportRequest struct {
	Document  map[string]any `json:"document"`
	RiskLevel RiskLevel      `json:"risk_level"`
}

type openAPIImportResponse struct {
	Created []Action `json:"created"`
}

func (s *Server) importOpenAPI(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req openAPIImportRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Document == nil {
		writeError(w, 400, "document is required")
		return
	}
	paths, ok := req.Document["paths"].(map[string]any)
	if !ok || len(paths) == 0 {
		writeError(w, 400, "openapi document paths is required")
		return
	}
	defaultRisk := req.RiskLevel
	if defaultRisk == "" {
		defaultRisk = RiskMedium
	}
	created := []Action{}
	now := time.Now().UTC()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	for rawPath, v := range paths {
		pathItem, ok := v.(map[string]any)
		if !ok {
			continue
		}
		for _, method := range []string{"get", "post", "put", "patch", "delete"} {
			op, ok := pathItem[method].(map[string]any)
			if !ok {
				continue
			}
			aName := deriveActionName(method, rawPath, op)
			desc, _ := op["summary"].(string)
			if desc == "" {
				desc, _ = op["description"].(string)
			}
			inSchema := map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
			if schema := findOpenAPIRequestSchema(op); schema != nil {
				inSchema = schema
			}
			outSchema := map[string]any{"type": "object", "properties": map[string]any{}, "required": []any{}}
			if schema := findOpenAPIResponseSchema(op); schema != nil {
				outSchema = schema
			}
			if err := validateSchemaDefinition(inSchema); err != nil {
				continue
			}
			if err := validateSchemaDefinition(outSchema); err != nil {
				continue
			}
			a := &Action{ID: newID(), TenantID: user.TenantID, OwnerID: user.ID, Name: aName, Description: desc, Method: strings.ToUpper(method), URL: rawPath, Headers: map[string]string{}, AuthConfig: map[string]any{}, InputSchema: inSchema, OutputSchema: outSchema, Tags: []string{"openapi-import"}, RiskLevel: defaultRisk, Status: ActionDraft, Enabled: true, CreatedAt: now, UpdatedAt: now}
			s.store.actions[a.ID] = a
			created = append(created, *a)
			s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: user.TenantID, UserID: user.ID, Action: "action.import.openapi", ResourceType: "action", ResourceID: a.ID, TraceID: newID(), Details: map[string]any{"name": a.Name}, CreatedAt: now})
		}
	}
	_ = s.store.persistLocked()
	writeJSON(w, 200, openAPIImportResponse{Created: created})
}

func deriveActionName(method, path string, op map[string]any) string {
	if id, ok := op["operationId"].(string); ok && strings.TrimSpace(id) != "" {
		return id
	}
	n := strings.ToLower(method) + "_" + strings.Trim(path, "/")
	n = strings.ReplaceAll(n, "/", "_")
	n = strings.ReplaceAll(n, "{", "")
	n = strings.ReplaceAll(n, "}", "")
	n = strings.ReplaceAll(n, "-", "_")
	if n == strings.ToLower(method)+"_" {
		n += "root"
	}
	return n
}

func findOpenAPIRequestSchema(op map[string]any) map[string]any {
	rb, ok := op["requestBody"].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := rb["content"].(map[string]any)
	if !ok {
		return nil
	}
	for _, media := range []string{"application/json", "application/*+json"} {
		if mt, ok := content[media].(map[string]any); ok {
			if schema, ok := mt["schema"].(map[string]any); ok {
				return schema
			}
		}
	}
	for _, v := range content {
		if mt, ok := v.(map[string]any); ok {
			if schema, ok := mt["schema"].(map[string]any); ok {
				return schema
			}
		}
	}
	return nil
}

func findOpenAPIResponseSchema(op map[string]any) map[string]any {
	responses, ok := op["responses"].(map[string]any)
	if !ok {
		return nil
	}
	for _, code := range []string{"200", "201", "default"} {
		if rs, ok := responses[code].(map[string]any); ok {
			if content, ok := rs["content"].(map[string]any); ok {
				if mt, ok := content["application/json"].(map[string]any); ok {
					if schema, ok := mt["schema"].(map[string]any); ok {
						return schema
					}
				}
				for _, v := range content {
					if mt, ok := v.(map[string]any); ok {
						if schema, ok := mt["schema"].(map[string]any); ok {
							return schema
						}
					}
				}
			}
		}
	}
	return nil
}

type actionRegisterReq struct {
	Name         string            `json:"name"`
	Description  string            `json:"description"`
	Method       string            `json:"method"`
	URL          string            `json:"url"`
	Headers      map[string]string `json:"headers"`
	AuthConfig   map[string]any    `json:"auth_config"`
	InputSchema  map[string]any    `json:"input_schema"`
	OutputSchema map[string]any    `json:"output_schema"`
	Tags         []string          `json:"tags"`
	RiskLevel    RiskLevel         `json:"risk_level"`
}

func (s *Server) registerAction(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req actionRegisterReq
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := validateSchemaDefinition(req.InputSchema); err != nil {
		writeError(w, 400, "invalid input_schema: "+err.Error())
		return
	}
	if err := validateSchemaDefinition(req.OutputSchema); err != nil {
		writeError(w, 400, "invalid output_schema: "+err.Error())
		return
	}
	if !validMethod(req.Method) {
		writeError(w, 400, "invalid method")
		return
	}
	if req.Headers == nil {
		req.Headers = map[string]string{}
	}
	if req.AuthConfig == nil {
		req.AuthConfig = map[string]any{}
	}
	a := &Action{ID: newID(), TenantID: user.TenantID, OwnerID: user.ID, Name: req.Name, Description: req.Description, Method: strings.ToUpper(req.Method), URL: req.URL, Headers: req.Headers, AuthConfig: req.AuthConfig, InputSchema: req.InputSchema, OutputSchema: req.OutputSchema, Tags: req.Tags, RiskLevel: req.RiskLevel, Status: ActionDraft, Enabled: true, CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
	trace := newID()
	s.store.mu.Lock()
	s.store.actions[a.ID] = a
	s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: user.TenantID, UserID: user.ID, Action: "action.register", ResourceType: "action", ResourceID: a.ID, TraceID: trace, Details: map[string]any{"name": a.Name}, CreatedAt: time.Now().UTC()})
	_ = s.store.persistLocked()
	s.store.mu.Unlock()
	writeJSON(w, 200, a)
}

func (s *Server) actionRoutes(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/verify") {
		s.withPerm("action:write", s.verifyAction)(w, r)
		return
	}
	if strings.HasSuffix(r.URL.Path, "/activate") {
		s.withPerm("action:write", s.activateAction)(w, r)
		return
	}
	http.NotFound(w, r)
}

func normalizeAPIPath(path string) string {
	if strings.HasPrefix(path, apiPrefixV1+"/") {
		return "/api/" + strings.TrimPrefix(path, apiPrefixV1+"/")
	}
	return path
}

func actionIDFromPath(path, suffix string) string {
	normalized := normalizeAPIPath(path)
	trim := strings.TrimSuffix(strings.TrimPrefix(normalized, "/api/actions/"), suffix)
	return strings.Trim(trim, "/")
}

func (s *Server) verifyAction(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	actionID := actionIDFromPath(r.URL.Path, "/verify")
	s.store.mu.RLock()
	a := s.store.actions[actionID]
	s.store.mu.RUnlock()
	if a == nil || a.TenantID != user.TenantID {
		writeError(w, 404, "Action not found")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, a.Method, a.URL, nil)
	for k, v := range a.Headers {
		req.Header.Set(k, v)
	}
	if token, ok := a.AuthConfig["bearer_token"].(string); ok && token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		writeError(w, 400, "Sandbox verify failed: "+err.Error())
		return
	}
	_ = resp.Body.Close()
	s.store.mu.Lock()
	a.Status = ActionVerified
	a.UpdatedAt = time.Now().UTC()
	s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: user.TenantID, UserID: user.ID, Action: "action.verify", ResourceType: "action", ResourceID: a.ID, TraceID: newID(), Details: map[string]any{"status": a.Status}, CreatedAt: time.Now().UTC()})
	_ = s.store.persistLocked()
	s.store.mu.Unlock()
	writeJSON(w, 200, a)
}

func (s *Server) activateAction(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	actionID := actionIDFromPath(r.URL.Path, "/activate")
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	a := s.store.actions[actionID]
	if a == nil || a.TenantID != user.TenantID {
		writeError(w, 404, "Action not found")
		return
	}
	if a.Status != ActionVerified {
		writeError(w, 400, "Action must be verified first")
		return
	}
	a.Status = ActionActive
	a.UpdatedAt = time.Now().UTC()
	sk := &Skill{ID: newID(), TenantID: user.TenantID, ActionID: a.ID, Name: "atomic::" + a.Name, Description: a.Description, SkillType: SkillAtomic, RiskLevel: a.RiskLevel, Enabled: true, CreatedAt: time.Now().UTC()}
	s.store.skills[sk.ID] = sk
	s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: user.TenantID, UserID: user.ID, Action: "skill.generate.atomic", ResourceType: "skill", ResourceID: sk.ID, TraceID: newID(), Details: map[string]any{"action_id": a.ID}, CreatedAt: time.Now().UTC()})
	_ = s.store.persistLocked()
	writeJSON(w, 200, a)
}

func (s *Server) querySkills(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	nameQ := strings.ToLower(r.URL.Query().Get("name"))
	tagQ := r.URL.Query().Get("tag")
	s.store.mu.RLock()
	out := []*Skill{}
	for _, sk := range s.store.skills {
		if sk.TenantID != user.TenantID || !sk.Enabled {
			continue
		}
		if nameQ != "" && !strings.Contains(strings.ToLower(sk.Name), nameQ) {
			continue
		}
		if tagQ != "" {
			a := s.store.actions[sk.ActionID]
			if a == nil || !contains(a.Tags, tagQ) {
				continue
			}
		}
		out = append(out, sk)
	}
	s.store.mu.RUnlock()
	writeJSON(w, 200, out)
}

func (s *Server) skillRoutes(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/execute") {
		s.withPerm("skill:execute", s.executeSkill)(w, r)
		return
	}
	http.NotFound(w, r)
}

type createWorkflowSkillRequest struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	RiskLevel   RiskLevel      `json:"risk_level"`
	Steps       []WorkflowStep `json:"steps"`
}

func (s *Server) createWorkflowSkill(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req createWorkflowSkillRequest
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if req.Name == "" || len(req.Steps) == 0 {
		writeError(w, 400, "name and steps are required")
		return
	}
	for i, st := range req.Steps {
		if st.ActionID == "" {
			writeError(w, 400, fmt.Sprintf("steps[%d].action_id is required", i))
			return
		}
		if st.Retry < 0 {
			writeError(w, 400, fmt.Sprintf("steps[%d].retry must be >= 0", i))
			return
		}
		s.store.mu.RLock()
		a := s.store.actions[st.ActionID]
		var rb *Action
		if st.RollbackActionID != "" {
			rb = s.store.actions[st.RollbackActionID]
		}
		s.store.mu.RUnlock()
		if a == nil || a.TenantID != user.TenantID {
			writeError(w, 400, fmt.Sprintf("steps[%d].action_id not found", i))
			return
		}
		if st.RollbackActionID != "" && (rb == nil || rb.TenantID != user.TenantID) {
			writeError(w, 400, fmt.Sprintf("steps[%d].rollback_action_id not found", i))
			return
		}
	}
	risk := req.RiskLevel
	if risk == "" {
		risk = RiskMedium
	}
	skill := &Skill{ID: newID(), TenantID: user.TenantID, Name: req.Name, Description: req.Description, SkillType: SkillWorkflow, RiskLevel: risk, Enabled: true, CreatedAt: time.Now().UTC(), Workflow: &WorkflowDefinition{Steps: req.Steps}}
	s.store.mu.Lock()
	s.store.skills[skill.ID] = skill
	s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: user.TenantID, UserID: user.ID, Action: "skill.generate.workflow", ResourceType: "skill", ResourceID: skill.ID, TraceID: newID(), Details: map[string]any{"steps": len(req.Steps)}, CreatedAt: time.Now().UTC()})
	_ = s.store.persistLocked()
	s.store.mu.Unlock()
	writeJSON(w, 200, skill)
}

func (s *Server) executeAtomicAction(ctx context.Context, action *Action, input map[string]any) (map[string]any, error) {
	if err := validatePayload(input, action.InputSchema); err != nil {
		return nil, fmt.Errorf("input schema validation failed: %w", err)
	}
	body, _ := json.Marshal(input)
	hreq, _ := http.NewRequestWithContext(ctx, action.Method, action.URL, strings.NewReader(string(body)))
	hreq.Header.Set("Content-Type", "application/json")
	for k, v := range action.Headers {
		hreq.Header.Set(k, v)
	}
	if token, ok := action.AuthConfig["bearer_token"].(string); ok && token != "" {
		hreq.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := s.client.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("upstream status %d", resp.StatusCode)
	}
	data, _ := io.ReadAll(resp.Body)
	out := map[string]any{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &out); err != nil {
			return nil, errors.New("invalid upstream json")
		}
	}
	if err := validatePayload(out, action.OutputSchema); err != nil {
		return nil, fmt.Errorf("output schema validation failed: %w", err)
	}
	return out, nil
}

func (s *Server) executeWorkflowSkill(r *http.Request, user *User, skill *Skill, req executeReq) (ExecutionStatus, map[string]any, string) {
	if skill.Workflow == nil || len(skill.Workflow.Steps) == 0 {
		return ExecFailed, nil, "workflow definition is empty"
	}
	results := map[string]any{}
	executed := []WorkflowStep{}
	stepInputs := map[string]map[string]any{}
	for idx, step := range skill.Workflow.Steps {
		s.store.mu.RLock()
		action := s.store.actions[step.ActionID]
		s.store.mu.RUnlock()
		if action == nil || action.TenantID != user.TenantID {
			return ExecFailed, map[string]any{"results": results, "failed_step": idx}, fmt.Sprintf("workflow step action not found: %s", step.ActionID)
		}
		key := step.InputKey
		if key == "" {
			key = step.Name
		}
		if key == "" {
			key = action.Name
		}
		input := req.Input
		if raw, ok := req.Input[key].(map[string]any); ok {
			input = raw
		}
		attempts := step.Retry + 1
		var out map[string]any
		var err error
		for i := 0; i < attempts; i++ {
			out, err = s.executeAtomicAction(r.Context(), action, input)
			if err == nil {
				break
			}
		}
		if err != nil {
			rollbackLog := []string{}
			for i := len(executed) - 1; i >= 0; i-- {
				es := executed[i]
				if es.RollbackActionID == "" {
					continue
				}
				s.store.mu.RLock()
				rb := s.store.actions[es.RollbackActionID]
				s.store.mu.RUnlock()
				if rb == nil || rb.TenantID != user.TenantID {
					rollbackLog = append(rollbackLog, "missing rollback action: "+es.RollbackActionID)
					continue
				}
				_, rbErr := s.executeAtomicAction(r.Context(), rb, stepInputs[es.ActionID])
				if rbErr != nil {
					rollbackLog = append(rollbackLog, rbErr.Error())
				}
			}
			return ExecFailed, map[string]any{"results": results, "rollback_errors": rollbackLog, "failed_step": idx}, err.Error()
		}
		results[key] = out
		executed = append(executed, step)
		stepInputs[step.ActionID] = input
	}
	return ExecSuccess, map[string]any{"results": results}, ""
}

func (s *Server) checkRateLimit(tenantID string) bool {
	nowSec := time.Now().Unix()
	s.store.mu.Lock()
	defer s.store.mu.Unlock()
	tenant := s.store.tenants[tenantID]
	if tenant == nil || tenant.QuotaQPS <= 0 {
		return true
	}
	st := s.store.tenantRate[tenantID]
	if st == nil || st.WindowStart != nowSec {
		s.store.tenantRate[tenantID] = &tenantRateState{WindowStart: nowSec, Count: 1}
		return true
	}
	if st.Count >= tenant.QuotaQPS {
		return false
	}
	st.Count++
	return true
}

func buildIdempotencyKey(tenantID, userID, skillID, raw string) string {
	if raw == "" {
		return ""
	}
	return tenantID + "::" + userID + "::" + skillID + "::" + raw
}

type executeReq struct {
	Input           map[string]any `json:"input"`
	ConfirmHighRisk bool           `json:"confirm_high_risk"`
}

func (s *Server) executeSkill(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	normalizedPath := normalizeAPIPath(r.URL.Path)
	skillID := strings.Trim(strings.TrimSuffix(strings.TrimPrefix(normalizedPath, "/api/skills/"), "/execute"), "/")
	var req executeReq
	if err := parseJSON(r, &req); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.store.mu.RLock()
	sk := s.store.skills[skillID]
	if sk == nil || sk.TenantID != user.TenantID {
		s.store.mu.RUnlock()
		writeError(w, 404, "Skill not found")
		return
	}
	a := s.store.actions[sk.ActionID]
	s.store.mu.RUnlock()
	if sk.SkillType != SkillWorkflow && a == nil {
		writeError(w, 400, "No bound action")
		return
	}
	if sk.RiskLevel == RiskHigh && !req.ConfirmHighRisk {
		writeError(w, 403, "High risk skill requires confirmation")
		return
	}
	if !s.checkRateLimit(user.TenantID) {
		writeError(w, 429, "Rate limit exceeded")
		return
	}
	idemRaw := r.Header.Get("Idempotency-Key")
	idemKey := buildIdempotencyKey(user.TenantID, user.ID, sk.ID, idemRaw)
	if idemKey != "" {
		s.store.mu.RLock()
		if cached, ok := s.store.idempotentResult[idemKey]; ok {
			s.store.mu.RUnlock()
			writeJSON(w, 200, cached)
			return
		}
		s.store.mu.RUnlock()
	}

	start := time.Now()
	traceID := newID()
	status := ExecSuccess
	var out map[string]any
	errMsg := ""

	if sk.SkillType == SkillWorkflow {
		status, out, errMsg = s.executeWorkflowSkill(r, user, sk, req)
	} else {
		res, err := s.executeAtomicAction(r.Context(), a, req.Input)
		if err != nil {
			status = ExecFailed
			errMsg = err.Error()
		} else {
			out = res
		}
	}

	duration := time.Since(start).Milliseconds()
	exe := &SkillExecution{ID: newID(), TenantID: user.TenantID, SkillID: sk.ID, UserID: user.ID, TraceID: traceID, Input: req.Input, Output: out, Status: status, DurationMS: duration, Error: errMsg, ExecutedAt: time.Now().UTC()}
	s.store.mu.Lock()
	s.store.executions[exe.ID] = exe
	s.appendAuditLocked(&AuditLog{ID: newID(), TenantID: user.TenantID, UserID: user.ID, Action: "skill.execute", ResourceType: "skill", ResourceID: sk.ID, TraceID: traceID, Details: map[string]any{"status": status, "duration_ms": duration}, CreatedAt: time.Now().UTC()})
	_ = s.store.persistLocked()
	s.store.mu.Unlock()

	respBody := map[string]any{"trace_id": traceID, "status": status, "output": out, "error_message": emptyToNil(errMsg)}
	if idemKey != "" {
		s.store.mu.Lock()
		s.store.idempotentResult[idemKey] = respBody
		_ = s.store.persistLocked()
		s.store.mu.Unlock()
	}
	writeJSON(w, 200, respBody)
}

func (s *Server) auditLogs(w http.ResponseWriter, r *http.Request, user *User) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	tenantID := r.URL.Query().Get("tenant_id")
	if tenantID != "" && tenantID != user.TenantID {
		writeError(w, 403, "Cross tenant query is forbidden")
		return
	}
	skillID := r.URL.Query().Get("skill_id")
	s.store.mu.RLock()
	rows := []*AuditLog{}
	for _, l := range s.store.auditLogs {
		if l.TenantID != user.TenantID {
			continue
		}
		if skillID != "" && l.ResourceID != skillID {
			continue
		}
		rows = append(rows, l)
	}
	s.store.mu.RUnlock()
	sort.Slice(rows, func(i, j int) bool { return rows[i].CreatedAt.After(rows[j].CreatedAt) })
	if len(rows) > 200 {
		rows = rows[:200]
	}
	writeJSON(w, 200, rows)
}

func (s *Server) appendAuditLocked(l *AuditLog) { s.store.auditLogs = append(s.store.auditLogs, l) }

func parseJSON(r *http.Request, v any) error {
	defer r.Body.Close()
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		return errors.New("invalid json")
	}
	if dec.More() {
		return errors.New("invalid json")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"detail": msg})
}

func methodNotAllowed(w http.ResponseWriter) { writeError(w, 405, "method not allowed") }

func contains(vs []string, v string) bool {
	for _, x := range vs {
		if x == v {
			return true
		}
	}
	return false
}

func emptyToNil(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func validMethod(m string) bool {
	switch strings.ToUpper(m) {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
		return true
	default:
		return false
	}
}

func validateSchemaDefinition(schema map[string]any) error {
	if schema == nil {
		return errors.New("schema cannot be nil")
	}
	t, ok := schema["type"].(string)
	if !ok || t == "" {
		return errors.New("schema.type is required")
	}
	if t != "object" {
		return errors.New("only object schema is supported in MVP")
	}
	return nil
}

func validatePayload(payload map[string]any, schema map[string]any) error {
	if err := validateSchemaDefinition(schema); err != nil {
		return err
	}
	required, _ := schema["required"].([]any)
	props, _ := schema["properties"].(map[string]any)
	for _, field := range required {
		name, ok := field.(string)
		if !ok {
			continue
		}
		if _, exists := payload[name]; !exists {
			return fmt.Errorf("missing required field %s", name)
		}
	}
	for key, cfg := range props {
		v, exists := payload[key]
		if !exists {
			continue
		}
		ptype, _ := cfg.(map[string]any)["type"].(string)
		switch ptype {
		case "string":
			if _, ok := v.(string); !ok {
				return fmt.Errorf("field %s must be string", key)
			}
		case "boolean":
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("field %s must be boolean", key)
			}
		case "number":
			switch v.(type) {
			case float64, float32, int, int64, int32:
			default:
				return fmt.Errorf("field %s must be number", key)
			}
		case "object":
			if _, ok := v.(map[string]any); !ok {
				return fmt.Errorf("field %s must be object", key)
			}
		}
	}
	return nil
}

func newID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}
