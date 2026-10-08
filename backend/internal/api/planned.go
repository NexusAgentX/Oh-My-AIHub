package api

// registerPlannedRoutes 登记契约中由后续 Feature 实现的路由（Epic #170）。
// 它们保留门禁并返回 501 not_implemented；实现时改为在对应领域文件中注册。
func registerPlannedRoutes(r *router) {
	// Feature B：首页、模型与路由、API Key、渠道、管理员渠道与网关入口。
	for _, entry := range []struct {
		pattern string
		level   access
	}{
		{"GET /api/home", accessReady},
		{"GET /api/models", accessReady},
		{"GET /api/models/{modelID}", accessReady},
		{"PUT /api/routing/{modelID}", accessReady},
		{"GET /api/keys", accessReady},
		{"POST /api/keys", accessReady},
		{"GET /api/keys/{keyID}", accessReady},
		{"PATCH /api/keys/{keyID}", accessReady},
		{"DELETE /api/keys/{keyID}", accessReady},
		{"GET /api/keys/{keyID}/secret", accessReady},
		{"PUT /api/keys/{keyID}/routing/{modelID}", accessReady},
		{"DELETE /api/keys/{keyID}/routing/{modelID}", accessReady},
		{"GET /api/channels", accessReady},
		{"POST /api/channels/discover", accessReady},
		{"POST /api/channels", accessReady},
		{"GET /api/channels/{channelID}", accessReady},
		{"PATCH /api/channels/{channelID}", accessReady},
		{"DELETE /api/channels/{channelID}", accessReady},
		{"POST /api/channels/{channelID}/test", accessReady},
		{"GET /api/admin/channels", accessAdmin},
		{"GET /api/admin/channels/{channelID}", accessAdmin},
		{"POST /api/admin/channels/{channelID}/suspend", accessAdmin},
		{"POST /api/admin/channels/{channelID}/unsuspend", accessAdmin},
		{"POST /v1/chat/completions", accessGatewayKey},
		{"POST /v1/responses", accessGatewayKey},
		{"POST /v1/messages", accessGatewayKey},
		{"POST /v1beta/models/{model}", accessGatewayKey},
		{"GET /v1/models", accessGatewayKey},
		{"GET /v1beta/models", accessGatewayKey},
	} {
		r.planned("B", entry.pattern, entry.level)
	}

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
