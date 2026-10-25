package api

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"io"
	"io/fs"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/yumi233/maa-control-center/internal/assistant"
	"github.com/yumi233/maa-control-center/internal/model"
	"github.com/yumi233/maa-control-center/internal/notify"
	"github.com/yumi233/maa-control-center/internal/storage"
	"github.com/yumi233/maa-control-center/internal/vault"
	"golang.org/x/crypto/bcrypt"
)

type Config struct {
	PublicURL, AgentID, AgentToken string
	Assets                         fs.FS
	Vault                          *vault.Vault
	AllowedOrigins                 []string
}
type Server struct {
	Store         *storage.Store
	cfg           Config
	mu            sync.Mutex
	attempts      map[string]attempt
	Integrations  assistant.SettingsStore
	Notifications *notify.Service
	LLM           assistant.ClientAPI
	planMu        sync.Mutex
}
type attempt struct {
	count int
	reset time.Time
}

func New(store *storage.Store, cfg Config) *Server {
	s := &Server{Store: store, cfg: cfg, attempts: map[string]attempt{}, Integrations: assistant.SettingsStore{DB: store.DB, Vault: cfg.Vault}}
	s.Notifications = &notify.Service{Settings: s.Integrations}
	return s
}
func token() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
func digest(v string) string { s := sha256.Sum256([]byte(v)); return hex.EncodeToString(s[:]) }
func equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(digest(a)), []byte(digest(b))) == 1
}
func send(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}
func problem(w http.ResponseWriter, status int, code, message string) {
	send(w, status, map[string]any{"error": code, "message": message})
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 2<<20)
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		problem(w, 400, "invalid_json", "请求格式无效或超过大小限制")
		return false
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		problem(w, 400, "invalid_json", "请求只允许一个 JSON 对象")
		return false
	}
	return true
}
func (s *Server) rate(r *http.Request, limit int) bool { return s.rateBucket(r, limit, "") }
func (s *Server) rateBucket(r *http.Request, limit int, bucket string) bool {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	key := r.URL.Path + "/" + host + "/" + bucket
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	a := s.attempts[key]
	if now.After(a.reset) {
		a = attempt{reset: now.Add(15 * time.Minute)}
	}
	a.count++
	s.attempts[key] = a
	if len(s.attempts) > 10000 {
		for k, v := range s.attempts {
			if now.After(v.reset) {
				delete(s.attempts, k)
			}
		}
	}
	return a.count <= limit
}
func (s *Server) session(r *http.Request) (string, string, bool) {
	c, err := r.Cookie("maacc_session")
	if err != nil {
		return "", "", false
	}
	var user, csrf string
	err = s.Store.DB.QueryRow("SELECT s.user_id,s.csrf FROM sessions s JOIN users u ON u.id=s.user_id WHERE u.disabled=0 AND s.token_hash=? AND s.expires_at>?", digest(c.Value), time.Now().Unix()).Scan(&user, &csrf)
	return user, csrf, err == nil
}
func (s *Server) originOK(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		return r.Header.Get("Sec-Fetch-Site") != "cross-site"
	}
	if origin == strings.TrimRight(s.cfg.PublicURL, "/") {
		return true
	}
	origins, err := ParseOrigins(s.cfg.AllowedOrigins)
	if err != nil {
		return false
	}
	for _, allowed := range origins {
		if origin == allowed {
			return true
		}
	}
	return false
}
func (s *Server) protect(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		_, csrf, ok := s.session(r)
		if !ok {
			problem(w, 401, "unauthorized", "请先登录管理中心")
			return
		}
		if r.Method != "GET" && (!s.originOK(r) || !equal(r.Header.Get("X-CSRF-Token"), csrf)) {
			problem(w, 403, "csrf", "页面凭证已失效，请刷新后重试")
			return
		}
		next(w, r)
	}
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	s.sklandRoutes(m)
	s.integrationRoutes(m)
	s.userRoutes(m)
	s.executionRoutes(m)
	s.botRoutes(m)
	m.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		if err := s.Store.DB.PingContext(r.Context()); err != nil {
			problem(w, 503, "database_unavailable", "数据库不可用")
			return
		}
		send(w, 200, map[string]any{"status": "ok", "skland_binding": sklandEnabled(), "version": "0.5.3", "mode": "reviewed_configuration"})
	})
	m.HandleFunc("POST /api/v1/auth/login", s.login)
	m.HandleFunc("GET /api/v1/auth/session", s.protect(func(w http.ResponseWriter, r *http.Request) {
		u := s.currentUser(r)
		_, csrf, _ := s.session(r)
		send(w, 200, map[string]string{"id": u.ID, "username": u.Username, "role": u.Role, "csrf_token": csrf})
	}))
	m.HandleFunc("POST /api/v1/auth/logout", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("maacc_session"); err == nil {
			_, _ = s.Store.DB.Exec("DELETE FROM sessions WHERE token_hash=?", digest(c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: "maacc_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://")})
		send(w, 200, map[string]bool{"ok": true})
	}))
	m.HandleFunc("GET /api/v1/accounts", s.protect(func(w http.ResponseWriter, r *http.Request) { a, err := s.visibleAccounts(r); s.result(w, a, err) }))
	m.HandleFunc("GET /api/v1/overview", s.protect(s.overview))
	m.HandleFunc("GET /api/v1/events", s.protect(func(w http.ResponseWriter, r *http.Request) { e, err := s.visibleEvents(r); s.result(w, e, err) }))
	m.HandleFunc("GET /api/v1/agents", s.protect(func(w http.ResponseWriter, r *http.Request) { a, err := s.visibleAgents(r); s.result(w, a, err) }))
	m.HandleFunc("GET /api/v1/accounts/{id}/status", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if a := s.account(w, r); a != nil {
			send(w, 200, a)
		}
	}))
	m.HandleFunc("GET /api/v1/accounts/{id}/policies", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if a := s.account(w, r); a != nil {
			send(w, 200, map[string]any{"observed": a.Policy, "required": required(a.Role), "writable": false})
		}
	}))
	m.HandleFunc("GET /api/v1/accounts/{id}/inventory", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if a := s.account(w, r); a != nil {
			items := []model.InventoryItem{}
			if a.Profile != nil && a.Profile.Inventory != nil {
				items = a.Profile.Inventory
			}
			send(w, 200, map[string]any{"items": items, "source": "maa_plan", "stale": true, "message": "目标来自已有计划；缓存尚未验证账号归属，当前库存留空"})
		}
	}))
	m.HandleFunc("GET /api/v1/accounts/{id}/schedule", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if a := s.account(w, r); a != nil {
			var schedule any
			if a.Profile != nil && len(a.Profile.Schedule) > 0 {
				_ = json.Unmarshal(a.Profile.Schedule, &schedule)
			}
			send(w, 200, map[string]any{"schedule": schedule, "read_only": true, "source": "agent", "message": "原有排班只读保留"})
		}
	}))
	m.HandleFunc("GET /api/v1/accounts/{id}/logs", s.protect(func(w http.ResponseWriter, r *http.Request) {
		if a := s.account(w, r); a != nil {
			logs := []string{}
			if a.Instance != nil && a.Instance.Logs != nil {
				logs = a.Instance.Logs
			}
			send(w, 200, map[string]any{"lines": logs, "redacted": true})
		}
	}))
	m.HandleFunc("POST /api/v1/accounts/{id}/binding", s.adminOnly(func(w http.ResponseWriter, r *http.Request) {
		if a := s.account(w, r); a != nil {
			if !s.accountIdle(a.ID) {
				problem(w, 409, "account_busy", "账号有待执行方案，请先取消或等待完成")
				return
			}
			var b model.Binding
			if !decode(w, r, &b) {
				return
			}
			b.AccountID = a.ID
			if err := s.Store.Bind(b); err != nil {
				problem(w, 422, "binding_invalid", "配置未发现或已绑定其他账号，无法重复绑定")
				return
			}
			_ = s.Store.Event("info", a.ID, "管理员已确认只读账号绑定")
			send(w, 200, map[string]bool{"ok": true})
		}
	}))
	m.HandleFunc("POST /api/v1/accounts/{id}/policies/preview", s.protect(s.preview))
	m.HandleFunc("PUT /api/v1/accounts/{id}/policies", s.protect(s.readOnly))
	m.HandleFunc("GET /api/v1/settings", s.protect(func(w http.ResponseWriter, r *http.Request) {
		send(w, 200, map[string]any{"mode": "reviewed_configuration", "version": "0.5.3", "database": "SQLite WAL", "agent_paired": len(s.cfg.AgentToken) >= 32, "scheduler_owner": "web_and_existing_maa", "remote_control": true, "configuration_sync": true})
	}))
	m.HandleFunc("POST /api/v1/agent/heartbeat", s.heartbeat)
	// Production dispatch is intentionally unavailable until installed-version capability checks pass.
	m.HandleFunc("POST /maa/remote/getTask", func(w http.ResponseWriter, r *http.Request) {
		problem(w, 423, "not_paired", "MAA 远程控制未启用；设备标识不作为身份凭证")
	})
	m.HandleFunc("POST /maa/remote/reportStatus", func(w http.ResponseWriter, r *http.Request) {
		problem(w, 423, "not_paired", "MAA 远程控制尚未完成配对")
	})
	m.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) { problem(w, 404, "not_found", "接口不存在") })
	m.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(405)
			return
		}
		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}
		if !fs.ValidPath(path) || strings.HasPrefix(path, ".") {
			w.WriteHeader(404)
			return
		}
		if _, err := fs.Stat(s.cfg.Assets, path); err != nil {
			if strings.Contains(path, ".") {
				w.WriteHeader(404)
				return
			}
			path = "index.html"
		}
		data, err := fs.ReadFile(s.cfg.Assets, path)
		if err != nil {
			http.Error(w, "Build the web frontend before starting the server.", 503)
			return
		}
		if strings.HasSuffix(path, ".js") {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
		}
		if strings.HasSuffix(path, ".css") {
			w.Header().Set("Content-Type", "text/css; charset=utf-8")
		}
		if strings.HasSuffix(path, ".svg") {
			w.Header().Set("Content-Type", "image/svg+xml")
		}
		if strings.HasSuffix(path, ".ttf") {
			w.Header().Set("Content-Type", "font/ttf")
		}
		if path == "index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		if strings.HasPrefix(path, "assets/") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		w.Write(data)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		m.ServeHTTP(w, r)
	})
}
func (s *Server) result(w http.ResponseWriter, v any, err error) {
	if err != nil {
		problem(w, 500, "storage_error", "读取数据失败")
		return
	}
	send(w, 200, v)
}
func (s *Server) account(w http.ResponseWriter, r *http.Request) *model.Account {
	a, err := s.visibleAccounts(r)
	if err != nil {
		problem(w, 500, "storage_error", "读取账号失败")
		return nil
	}
	for _, v := range a {
		if v.ID == r.PathValue("id") {
			return &v
		}
	}
	problem(w, 404, "not_found", "账号不存在")
	return nil
}
func (s *Server) readOnly(w http.ResponseWriter, r *http.Request) {
	problem(w, 423, "read_only", "当前为只读接入；尚未验证的任务控制与配置写入保持关闭")
}
func required(role string) any {
	if role == "secondary" {
		return map[string]any{"medicine_budget": 0, "stone_budget": 0, "expiring_hours": 48}
	}
	return map[string]any{"source": "inherit_verified_configuration"}
}
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	a, err := s.visibleAccounts(r)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	agents, err := s.visibleAgents(r)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	events, err := s.visibleEvents(r)
	if err != nil {
		s.result(w, nil, err)
		return
	}
	send(w, 200, map[string]any{"accounts": a, "agents": agents, "events": events, "jobs": func() []Job { v, _ := s.jobsFor(r); return v }(), "server_time": time.Now().UTC().Format(time.RFC3339), "mode": "reviewed_configuration", "version": "0.5.3"})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if !s.originOK(r) {
		problem(w, 403, "origin", "来源校验失败")
		return
	}
	if !s.rate(r, 120) {
		problem(w, 429, "rate_limited", "尝试次数过多，请稍后重试")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !decode(w, r, &in) {
		return
	}
	if len(in.Username) > 48 || !s.rateBucket(r, 12, "login/"+strings.ToLower(in.Username)) {
		problem(w, 429, "rate_limited", "该账号尝试次数过多，请稍后重试")
		return
	}
	var hash, id string
	err := s.Store.DB.QueryRow("SELECT id,password_hash FROM users WHERE disabled=0 AND username=?", in.Username).Scan(&id, &hash)
	if err != nil {
		hash = "$2a$10$9KqLxMGfrY5xjKws3g.N4OqW5BBEFXkuB8Vq8vWCOTgRj5ED1mzGS"
	}
	match := bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password))
	if err != nil || match != nil {
		problem(w, 401, "invalid_credentials", "用户名或密码不正确")
		return
	}
	value, csrf := token(), token()
	tx, err := s.Store.DB.Begin()
	if err != nil {
		s.result(w, nil, err)
		return
	}
	defer tx.Rollback()
	var fresh string
	if tx.QueryRow("SELECT password_hash FROM users WHERE id=? AND disabled=0", id).Scan(&fresh) != nil || fresh != hash {
		problem(w, 401, "invalid_credentials", "用户名或密码不正确")
		return
	}
	if _, err = tx.Exec("DELETE FROM sessions WHERE expires_at<? OR user_id=?", time.Now().Unix(), id); err != nil {
		s.result(w, nil, err)
		return
	}
	if _, err = tx.Exec("INSERT INTO sessions(token_hash,user_id,csrf,expires_at) VALUES(?,?,?,?)", digest(value), id, csrf, time.Now().Add(12*time.Hour).Unix()); err != nil {
		s.result(w, nil, err)
		return
	}
	if err = tx.Commit(); err != nil {
		s.result(w, nil, err)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: "maacc_session", Value: value, Path: "/", MaxAge: 43200, HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: strings.HasPrefix(s.cfg.PublicURL, "https://")})
	_ = s.Store.Event("info", "", "用户已登录管理中心")
	var role string
	_ = s.Store.DB.QueryRow("SELECT role FROM users WHERE id=?", id).Scan(&role)
	send(w, 200, map[string]string{"id": id, "username": in.Username, "role": role, "csrf_token": csrf})
}
func (s *Server) heartbeat(w http.ResponseWriter, r *http.Request) {
	bearer := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	if len(s.cfg.AgentToken) < 32 || !equal(bearer, s.cfg.AgentToken) {
		problem(w, 401, "agent_unauthorized", "Agent 尚未配对")
		return
	}
	if !s.rate(r, 1200) {
		problem(w, 429, "rate_limited", "心跳频率过高")
		return
	}
	var h model.Heartbeat
	if !decode(w, r, &h) {
		return
	}
	if h.AgentID != s.cfg.AgentID || len(h.Instances) > 16 || len(h.Hostname) > 128 {
		problem(w, 422, "agent_invalid", "Agent 标识或实例列表无效")
		return
	}
	for _, i := range h.Instances {
		if len(i.ID) > 128 || i.ID == "" || len(i.Profiles) > 32 || len(i.Logs) > 100 {
			problem(w, 422, "snapshot_invalid", "实例数据无效")
			return
		}
		for _, line := range i.Logs {
			if len(line) > 4096 || containsSecret(line) {
				problem(w, 422, "unredacted_log", "日志中可能含凭据，已拒绝接收")
				return
			}
		}
	}
	if err := s.Store.Heartbeat(h); err != nil {
		s.result(w, nil, err)
		return
	}
	if err := s.observedCompletions(h); err != nil {
		problem(w, 500, "completion_store_failed", "任务结束事件暂未保存，将由下次心跳重试")
		return
	}
	send(w, 200, map[string]any{"accepted": true, "mode": "read_only", "server_time": time.Now().UTC().Format(time.RFC3339)})
}
func containsSecret(v string) bool {
	l := strings.ToLower(v)
	for _, s := range []string{"bearer ", "authorization:", "api_key=", "access_token=", "password="} {
		if strings.Contains(l, s) {
			return true
		}
	}
	return false
}
func (s *Server) preview(w http.ResponseWriter, r *http.Request) {
	a := s.account(w, r)
	if a == nil {
		return
	}
	var p model.Policy
	if !decode(w, r, &p) {
		return
	}
	errors := []string{}
	if a.Role == "secondary" {
		if p.MedicineBudget == nil || *p.MedicineBudget != 0 {
			errors = append(errors, "副账号普通理智药预算必须为 0")
		}
		if p.StoneBudget == nil || *p.StoneBudget != 0 {
			errors = append(errors, "副账号源石预算必须为 0")
		}
		if p.ExpiringHours == nil || *p.ExpiringHours != 48 {
			errors = append(errors, "副账号临期药窗口必须为 48 小时")
		}
	}
	for _, n := range []*int{p.MedicineBudget, p.StoneBudget, p.ExpiringHours} {
		if n != nil && (*n < 0 || *n > 10000) {
			errors = append(errors, "预算不在有效范围")
		}
	}
	send(w, 200, map[string]any{"original": a.Policy, "proposed": p, "errors": errors, "valid": len(errors) == 0, "applicable": false, "message": "此预算摘要仅预览；应用请使用 Agent 配置的逐字段方案"})
}
