package api

import "net/http"

// access 声明一条路由在 handler 之前必须通过的门禁。
type access string

const (
	// accessPublic 不要求会话。
	accessPublic access = "public"
	// accessSession 要求有效会话，但允许仍需首次改密的账户（仅改密、查会话使用）。
	accessSession access = "session"
	// accessReady 要求有效会话且已完成首次改密。
	accessReady access = "ready"
	// accessAdmin 在 accessReady 基础上要求管理员。
	accessAdmin access = "admin"
	// accessGatewayKey 为外部模型 API 入口，凭平台 API Key 认证，不走会话与同源校验。
	accessGatewayKey access = "gateway_key"
)

// route 是路由表的一行，契约测试用它与 OpenAPI 规范逐项对照。
// feature 为负责实现的 Feature（Epic #170）；implemented 为 false 时 handler 返回 501。
type route struct {
	pattern     string
	access      access
	feature     string
	implemented bool
}

// router 按领域注册路由，并按声明的 access 套上会话与权限门禁。
type router struct {
	app   *app
	mux   *http.ServeMux
	table []route
}

func newRouter(application *app) *router {
	return &router{app: application, mux: http.NewServeMux()}
}

func (r *router) wrap(level access, handler http.HandlerFunc) http.Handler {
	switch level {
	case accessSession:
		return r.app.requireSession(handler)
	case accessReady:
		return r.app.requireReadyAccount(handler)
	case accessAdmin:
		return r.app.requireAdmin(handler)
	}
	return handler
}

func (r *router) handle(pattern string, level access, handler http.HandlerFunc) {
	r.table = append(r.table, route{pattern: pattern, access: level, feature: "A", implemented: true})
	r.mux.Handle(pattern, r.wrap(level, handler))
}

// implement registers a route owned by a later feature once it is built.
func (r *router) implement(feature string, pattern string, level access, handler http.HandlerFunc) {
	r.table = append(r.table, route{pattern: pattern, access: level, feature: feature, implemented: true})
	r.mux.Handle(pattern, r.wrap(level, handler))
}

func (r *router) public(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessPublic, handler)
}

func (r *router) session(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessSession, handler)
}

func (r *router) ready(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessReady, handler)
}

func (r *router) admin(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessAdmin, handler)
}

// planned registers a contract route owned by a later feature: it keeps the
// access gate and answers 501 not_implemented until that feature lands.
func (r *router) planned(feature string, pattern string, level access) {
	r.table = append(r.table, route{pattern: pattern, access: level, feature: feature})
	r.mux.Handle(pattern, r.wrap(level, notImplemented))
}
