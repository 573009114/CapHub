const { createApp } = Vue;

createApp({
  data() {
    return {
      baseUrl: localStorage.getItem('caphub.baseUrl') || 'http://localhost:8080',
      username: localStorage.getItem('caphub.username') || '',
      password: '',
      newPassword: '',
      token: localStorage.getItem('caphub.token') || '',
      userId: localStorage.getItem('caphub.userId') || '',
      output: '欢迎使用 CapHub Vue 控制台',
      actionForm: {
        name: 'create_ticket',
        method: 'POST',
        url: 'http://127.0.0.1:8080/healthz',
        risk_level: 'medium',
        tags: '工单,ops',
        description: '创建工单',
        input_schema: '{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}',
        output_schema: '{"type":"object","properties":{"ok":{"type":"boolean"}},"required":["ok"]}',
      },
      actionId: '',
      skillName: 'create_ticket',
      skillTag: '工单',
      skillId: '',
      confirmRisk: false,
      execInput: '{"title":"bug"}',
      skills: [],
      audits: [],
    };
  },
  watch: {
    baseUrl(v) { localStorage.setItem('caphub.baseUrl', v); },
    token(v) { localStorage.setItem('caphub.token', v || ''); },
    userId(v) { localStorage.setItem('caphub.userId', v || ''); },
    username(v) { localStorage.setItem('caphub.username', v || ''); },
  },
  methods: {
    headers() {
      const h = { 'Content-Type': 'application/json' };
      if (this.token) h.Authorization = `Bearer ${this.token}`;
      else if (this.userId) h['X-User-Id'] = this.userId;
      return h;
    },
    async request(path, method = 'GET', body) {
      const res = await fetch(`${this.baseUrl.replace(/\/$/, '')}${path}`, {
        method,
        headers: this.headers(),
        body: body ? JSON.stringify(body) : undefined,
      });
      const text = await res.text();
      let data;
      try { data = text ? JSON.parse(text) : {}; } catch { data = { raw: text }; }
      if (!res.ok) throw { status: res.status, data };
      return data;
    },
    parseJSON(v, field) {
      try { return JSON.parse(v); } catch { throw new Error(`${field} 不是合法 JSON`); }
    },
    print(title, data) {
      this.output = `${title}\n${JSON.stringify(data, null, 2)}`;
    },
    async bootstrapAdmin() {
      try {
        const data = await this.request('/api/bootstrap/admin');
        this.userId = data.admin_user_id || '';
        this.print('管理员ID', data);
      } catch (e) { this.print('错误', e); }
    },
    async health() {
      try { this.print('健康检查', await this.request('/healthz')); }
      catch (e) { this.print('错误', e); }
    },
    async register() {
      try {
        const data = await this.request('/api/auth/register', 'POST', {
          username: this.username,
          password: this.password,
        });
        this.userId = data.user_id;
        this.token = data.access_token;
        this.print('注册成功', data);
      } catch (e) { this.print('注册失败', e); }
    },
    async login() {
      try {
        const data = await this.request('/api/auth/login', 'POST', {
          username: this.username,
          password: this.password,
        });
        this.userId = data.user_id;
        this.token = data.access_token;
        this.print('登录成功', data);
      } catch (e) { this.print('登录失败', e); }
    },
    async changePassword() {
      try {
        if (!this.token && !this.userId) throw new Error('请先登录');
        if (!this.password || !this.newPassword) throw new Error('请输入旧密码和新密码');
        const data = await this.request('/api/auth/change_password', 'POST', {
          old_password: this.password,
          new_password: this.newPassword,
        });
        this.password = this.newPassword;
        this.newPassword = '';
        this.print('修改密码成功', data);
      } catch (e) { this.print('修改密码失败', e); }
    },
    logout() {
      this.token = '';
      this.password = '';
      this.print('已退出登录', { ok: true });
    },
    async registerAction() {
      try {
        const data = await this.request('/api/actions/register', 'POST', {
          name: this.actionForm.name,
          description: this.actionForm.description,
          method: this.actionForm.method.toUpperCase(),
          url: this.actionForm.url,
          headers: {},
          auth_config: {},
          input_schema: this.parseJSON(this.actionForm.input_schema, 'Input Schema'),
          output_schema: this.parseJSON(this.actionForm.output_schema, 'Output Schema'),
          risk_level: this.actionForm.risk_level,
          tags: this.actionForm.tags.split(',').map((v) => v.trim()).filter(Boolean),
        });
        this.actionId = data.id;
        this.print('Action 注册成功', data);
      } catch (e) { this.print('错误', e); }
    },
    async verifyAction() {
      try { this.print('Action 验证', await this.request(`/api/actions/${this.actionId}/verify`, 'POST')); }
      catch (e) { this.print('错误', e); }
    },
    async activateAction() {
      try { this.print('Action 激活', await this.request(`/api/actions/${this.actionId}/activate`, 'POST')); }
      catch (e) { this.print('错误', e); }
    },
    async querySkills() {
      try {
        const data = await this.request(`/api/skills?name=${encodeURIComponent(this.skillName)}&tag=${encodeURIComponent(this.skillTag)}`);
        this.skills = data;
        this.print('Skill 查询', data);
      } catch (e) { this.print('错误', e); }
    },
    async executeSkill() {
      try {
        const data = await this.request(`/api/skills/${this.skillId}/execute`, 'POST', {
          input: this.parseJSON(this.execInput, '执行输入'),
          confirm_high_risk: !!this.confirmRisk,
        });
        this.print('Skill 执行', data);
      } catch (e) { this.print('错误', e); }
    },
    async queryAudit() {
      try {
        const data = await this.request('/api/audit_logs');
        this.audits = data;
        this.print('审计日志', data);
      } catch (e) { this.print('错误', e); }
    },
  },
}).mount('#app');
