package api

// registerPlannedRoutes 登记契约中由后续 Feature 实现的路由（Epic #170）。
// 它们保留门禁并返回 501 not_implemented；实现时改为在对应领域文件中注册。
func registerPlannedRoutes(r *router) {
	// Feature G：调用与积分可观测性。
	for _, entry := range []struct {
		pattern string
		level   access
	}{
		{"GET /api/calls", accessReady},
		{"GET /api/calls/stream", accessReady},
		{"GET /api/calls/{callID}", accessReady},
		{"GET /api/usage", accessReady},
		{"GET /api/channels/{channelID}/stats", accessReady},
		{"GET /api/channels/{channelID}/calls", accessReady},
		{"GET /api/channels/{channelID}/calls/stream", accessReady},
		{"GET /api/admin/overview", accessAdmin},
		{"GET /api/admin/calls", accessAdmin},
		{"GET /api/admin/calls/stream", accessAdmin},
		{"GET /api/admin/points", accessAdmin},
		{"GET /api/admin/ledger/transactions", accessAdmin},
		{"GET /api/admin/ledger/transactions/{transactionID}", accessAdmin},
		{"POST /api/admin/ledger/repair-call/{callID}", accessAdmin},
	} {
		r.planned("G", entry.pattern, entry.level)
	}
}
