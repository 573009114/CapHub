# CapHub Enterprise Edition (MVP, Go)

基于 Go 实现的企业级 AI 能力注册与调度平台 Phase 1 MVP，覆盖：

- 多租户数据隔离（tenant_id）
- RBAC 权限控制（支持 Bearer Token 与兼容 X-User-Id）
- Action 注册/校验/验证/激活生命周期
- Atomic Skill 自动生成
- Workflow Skill（基础线性编排）
- Skill 执行（输入输出 Schema 校验、高风险确认、超时控制、租户级 QPS 限流、幂等键）
- 审计日志与 Trace ID
- 本地文件持久化（重启后数据保留）

## 技术栈

- Go 1.22+
- Gin（采用 gin-vue-admin 常见的 Gin 路由架构）
- 本地 JSON 文件存储（默认 `./caphub-data.json`）

## 启动

```bash
go run .
# 或 go run ./cmd/server
```

默认监听 `:8080`，可通过 `ADDR` 覆盖。  
默认数据文件为 `./caphub-data.json`，可通过 `DATA_FILE` 覆盖。  
可通过 `TENANT_QUOTA_QPS` 配置默认租户 QPS（默认 2000）。


## 架构说明（Gin-Vue-Admin 风格）

当前项目已按 gin-vue-admin 常见的分层启动方式改造：

- `main.go` / `cmd/server/main.go`：入口
- `core`：服务启动与基础运行
- `initialize`：应用初始化
- `router`：路由装配
- `internal/caphub`：领域核心实现

> 受当前环境网络限制，无法直接拉取 gin-vue-admin 源码模板，因此采用同风格分层并保留现有业务实现。

## 关键接口

- `GET /healthz`
- `GET /api/bootstrap/admin`（获取默认管理员 ID）
- `POST /api/auth/token`（用 user_id 换取 Bearer Token）
- `POST /api/actions/register`
- `POST /api/actions/import/openapi`（导入 OpenAPI 文档并批量生成 Draft Actions）
- `POST /api/actions/{id}/verify`
- `POST /api/actions/{id}/activate`
- `GET /api/skills?name=...&tag=...`
- `POST /api/skills/workflow`（创建 Workflow Skill，支持 step 级 retry 与 rollback action）
- `POST /api/skills/{id}/execute`
- `GET /api/audit_logs`

> 受保护接口支持两种认证方式：
> 1) `Authorization: Bearer <token>`（推荐）
> 2) `X-User-Id: <admin_user_id>`（兼容模式）
>
> Skill 执行可选 Header：`Idempotency-Key`（同租户/同用户/同技能重复请求返回首次结果）。

## PRD v1.0 到 MVP 实现映射

### 已覆盖（Phase 1）

1. 多租户支持
   - 用户、Action、Skill、执行记录、审计日志均绑定 `tenant_id`。
   - 查询接口按当前用户租户过滤。

2. RBAC
   - 权限码：`action:write`、`action:read`、`skill:execute`、`audit:read`。
   - 受保护接口支持 Bearer Token（JWT-HS256）及兼容 `X-User-Id` 方式。

3. Action 注册与验证
   - `POST /api/actions/register`
   - `POST /api/actions/import/openapi`（基础 OpenAPI 导入）
   - 注册时校验 Schema 定义
   - `POST /api/actions/{id}/verify` 执行沙箱调用
   - 生命周期：`Draft -> Verified -> Active`

4. Atomic Skill 自动生成
   - Action 激活时自动生成 `atomic::<action_name>`。

5. Workflow Skill（基础）
   - `POST /api/skills/workflow` 创建 Workflow Skill。
   - 支持 step 级重试（retry）与回滚 action（rollback_action_id）。

6. Skill 执行与审计
   - 执行前按 Schema 校验输入
   - 高风险 Skill 需要 `confirm_high_risk=true`
   - 执行超时控制与失败捕获
   - 租户级 QPS 限流（基于 `quota_qps`）
   - 幂等键支持（`Idempotency-Key`）
   - 审计日志记录 trace_id、状态、耗时

7. 持久化能力（本阶段增强）
   - 运行数据写入本地 JSON 文件（`DATA_FILE`），服务重启后可恢复。

### 待扩展（下一阶段）

- PostgreSQL 持久化与迁移管理
- Redis 限流与幂等键（当前实现为进程内版本）
- OpenAPI/Swagger 导入 Action（已提供基础 OpenAPI 导入接口，暂不支持 `$ref` 深度解析）
- Workflow Skill 高级编排（分支/并行/条件/DAG）
- OAuth2/KMS/Vault 完整安全链路（当前仅实现基础 JWT）

## 测试

```bash
go test ./...
```
