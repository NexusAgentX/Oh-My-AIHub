package api

import (
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
)

func unsupportedEndpoint(w http.ResponseWriter, r *http.Request) {
	gateway.WriteError(w, gateway.DialectOpenAI, http.StatusNotFound, "unsupported_endpoint", "不支持的接口", "")
}

// registerGatewayRoutes 注册外部模型 API 入口（Feature B）。其他 /v1、/v1beta 路径回答“不支持的接口”。
func (a *app) registerGatewayRoutes(r *router) {
	serve := func(format channel.Format) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) { a.gateway.Serve(w, req, format, "", false) }
	}
	r.implement("B", "POST /v1/responses", accessGatewayKey, serve(channel.FormatOpenAIResponses))
	r.implement("B", "GET /v1/responses/{responseID}", accessGatewayKey, func(w http.ResponseWriter, r *http.Request) { a.gateway.ServeOpenAIResponse(w, r) })
	r.implement("B", "POST /v1/responses/{responseID}/cancel", accessGatewayKey, func(w http.ResponseWriter, r *http.Request) { a.gateway.ServeResponse(w, r, true) })
	r.implement("B", "GET /v1/models/{model}", accessGatewayKey, a.gateway.GetModel)
	r.implement("B", "GET /v1/models", accessGatewayKey, a.gateway.ListModels)

	for path, format := range map[string]channel.Format{
		"/v1/responses/compact":      channel.FormatOpenAIResponses,
		"/v1/responses/input_tokens": channel.FormatOpenAIResponses,
		"/v1/conversations":          channel.FormatOpenAIResponses,
	} {
		r.implement("B", "POST "+path, accessGatewayKey, serve(format))
	}
	resource := func(kind string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) { a.gateway.ServeResource(w, r, kind) }
	}
	for _, method := range []string{"GET", "POST", "DELETE"} {
		r.implement("B", method+" /v1/conversations/{resourceID}", accessGatewayKey, resource("conversation"))
	}
	r.implement("B", "DELETE /v1/responses/{responseID}", accessGatewayKey, resource("response"))
	r.implement("B", "GET /v1/responses/{responseID}/input_items", accessGatewayKey, resource("response"))
	for _, method := range []string{"GET", "POST"} {
		r.implement("B", method+" /v1/conversations/{resourceID}/items", accessGatewayKey, resource("conversation"))
	}
	for _, method := range []string{"GET", "DELETE"} {
		r.implement("B", method+" /v1/conversations/{resourceID}/items/{itemID}", accessGatewayKey, resource("conversation"))
	}
	for _, path := range []string{"/v1/user-auth-credential/whoami", "/backend-api/wham/accounts/check", "/backend-api/codex/models"} {
		r.implement("B", "GET "+path, accessGatewayKey, a.gateway.ServeCodexConfig)
	}
	for path, format := range map[string]channel.Format{
		"/backend-api/codex/responses":              channel.FormatOpenAIResponses,
		"/backend-api/codex/responses/input_tokens": channel.FormatOpenAIResponses,
		"/backend-api/codex/responses/compact":      channel.FormatOpenAIResponses,
		"/backend-api/codex/alpha/search":           channel.FormatOpenAIResponses,
		"/backend-api/codex/images/generations":     channel.FormatOpenAIImages,
		"/backend-api/codex/images/edits":           channel.FormatOpenAIImages,
	} {
		r.implement("B", "POST "+path, accessGatewayKey, serve(format))
	}
	r.implement("B", "GET /backend-api/codex/responses/{responseID}", accessGatewayKey, a.gateway.ServeOpenAIResponse)
	r.implement("B", "POST /backend-api/codex/responses/{responseID}/cancel", accessGatewayKey, func(w http.ResponseWriter, r *http.Request) { a.gateway.ServeResponse(w, r, true) })
	r.implement("B", "DELETE /backend-api/codex/responses/{responseID}", accessGatewayKey, resource("response"))
	r.implement("B", "GET /backend-api/codex/responses/{responseID}/input_items", accessGatewayKey, resource("response"))
	r.mux.HandleFunc("/backend-api/", unsupportedEndpoint)
	r.mux.HandleFunc("/v1/", unsupportedEndpoint)
	r.mux.HandleFunc("/v1beta/", unsupportedEndpoint)
}
