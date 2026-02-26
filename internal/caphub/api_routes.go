package caphub

const (
	apiPrefixV1 = "/api/v1"

	rHealthz         = "/healthz"
	rBootstrapAdmin  = "/api/bootstrap/admin"
	rBootstrapInitDB = "/api/bootstrap/init_db"
	rAuthRegister    = "/api/auth/register"
	rAuthLogin       = "/api/auth/login"
	rAuthToken       = "/api/auth/token"
	rActionsRegister = "/api/actions/register"
	rActionsImport   = "/api/actions/import/openapi"
	rActionsWildcard = "/api/actions/"
	rSkillsQuery     = "/api/skills"
	rSkillsWorkflow  = "/api/skills/workflow"
	rSkillsWildcard  = "/api/skills/"
	rAuditLogs       = "/api/audit_logs"
)
