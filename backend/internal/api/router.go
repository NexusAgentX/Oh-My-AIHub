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
	// accessGatewayKey 为外部模型协议入口，凭网关 API Key 认证，不走会话与同源校验。
	accessGatewayKey access = "gateway_key"
)

// route 是路由表的一行，契约测试用它与 OpenAPI 规范逐项对照。
type route struct {
	pattern string
	access  access
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

func (r *router) handle(pattern string, level access, handler http.Handler) {
	r.table = append(r.table, route{pattern: pattern, access: level})
	r.mux.Handle(pattern, handler)
}

func (r *router) public(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessPublic, handler)
}

func (r *router) session(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessSession, r.app.requireSession(handler))
}

func (r *router) ready(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessReady, r.app.requireReadyAccount(handler))
}

func (r *router) admin(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessAdmin, r.app.requireAdmin(handler))
}

func (r *router) gateway(pattern string, handler http.HandlerFunc) {
	r.handle(pattern, accessGatewayKey, handler)
}
