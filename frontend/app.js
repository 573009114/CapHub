const $ = (id) => document.getElementById(id);

function getAuthHeaders() {
  const token = $("token").value.trim();
  const userId = $("userId").value.trim();
  const headers = { "Content-Type": "application/json" };
  if (token) headers.Authorization = `Bearer ${token}`;
  else if (userId) headers["X-User-Id"] = userId;
  return headers;
}

function base(path) {
  return `${$("baseUrl").value.replace(/\/$/, "")}${path}`;
}

function log(title, data) {
  $("output").textContent = `${title}\n${JSON.stringify(data, null, 2)}`;
}

async function api(path, { method = "GET", body } = {}) {
  const res = await fetch(base(path), {
    method,
    headers: getAuthHeaders(),
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let data;
  try { data = text ? JSON.parse(text) : {}; } catch { data = { raw: text }; }
  if (!res.ok) throw { status: res.status, data };
  return data;
}

function safeJSON(text, fieldName) {
  try { return JSON.parse(text); }
  catch { throw new Error(`${fieldName} 不是合法 JSON`); }
}

$("btnBootstrap").onclick = async () => {
  try {
    const data = await api("/api/bootstrap/admin");
    $("userId").value = data.admin_user_id || "";
    log("管理员ID", data);
  } catch (e) { log("错误", e); }
};

$("btnToken").onclick = async () => {
  try {
    const userId = $("userId").value.trim();
    const data = await api("/api/auth/token", { method: "POST", body: { user_id: userId } });
    $("token").value = data.access_token || "";
    log("Token", data);
  } catch (e) { log("错误", e); }
};

$("btnHealth").onclick = async () => {
  try { log("健康检查", await api("/healthz")); }
  catch (e) { log("错误", e); }
};

$("btnRegisterAction").onclick = async () => {
  try {
    const body = {
      name: $("actionName").value.trim(),
      description: $("actionDesc").value.trim(),
      method: $("actionMethod").value.trim().toUpperCase(),
      url: $("actionUrl").value.trim(),
      headers: {},
      auth_config: {},
      input_schema: safeJSON($("inputSchema").value, "Input Schema"),
      output_schema: safeJSON($("outputSchema").value, "Output Schema"),
      risk_level: $("riskLevel").value,
      tags: $("actionTags").value.split(",").map((x) => x.trim()).filter(Boolean),
    };
    const data = await api("/api/actions/register", { method: "POST", body });
    $("actionId").value = data.id || "";
    log("Action 注册成功", data);
  } catch (e) { log("错误", e); }
};

$("btnVerify").onclick = async () => {
  try {
    const id = $("actionId").value.trim();
    log("Action 验证", await api(`/api/actions/${id}/verify`, { method: "POST" }));
  } catch (e) { log("错误", e); }
};

$("btnActivate").onclick = async () => {
  try {
    const id = $("actionId").value.trim();
    log("Action 激活", await api(`/api/actions/${id}/activate`, { method: "POST" }));
  } catch (e) { log("错误", e); }
};

$("btnQuerySkills").onclick = async () => {
  try {
    const name = encodeURIComponent($("skillName").value.trim());
    const tag = encodeURIComponent($("skillTag").value.trim());
    const data = await api(`/api/skills?name=${name}&tag=${tag}`);
    const tbody = $("skillsTable").querySelector("tbody");
    tbody.innerHTML = "";
    data.forEach((s) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td>${s.id}</td><td>${s.name}</td><td>${s.skill_type}</td><td>${s.risk_level}</td>`;
      tr.onclick = () => { $("skillId").value = s.id; };
      tbody.appendChild(tr);
    });
    log("Skill 查询", data);
  } catch (e) { log("错误", e); }
};

$("btnExecuteSkill").onclick = async () => {
  try {
    const id = $("skillId").value.trim();
    const input = safeJSON($("execInput").value, "执行输入");
    const body = { input, confirm_high_risk: $("confirmRisk").value === "true" };
    log("Skill 执行", await api(`/api/skills/${id}/execute`, { method: "POST", body }));
  } catch (e) { log("错误", e); }
};

$("btnAudit").onclick = async () => {
  try {
    const data = await api("/api/audit_logs");
    const tbody = $("auditTable").querySelector("tbody");
    tbody.innerHTML = "";
    data.forEach((a) => {
      const tr = document.createElement("tr");
      tr.innerHTML = `<td>${a.created_at || ""}</td><td>${a.action || ""}</td><td>${a.resource_id || ""}</td><td>${a.trace_id || ""}</td>`;
      tbody.appendChild(tr);
    });
    log("审计日志", data);
  } catch (e) { log("错误", e); }
};
