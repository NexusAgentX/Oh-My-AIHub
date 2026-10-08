package api

import (
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
)

func unsupportedEndpoint(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusNotFound, "unsupported_endpoint", "不支持的接口")
}

// serveGemini splits "{model}:generateContent" / "{model}:streamGenerateContent".
func (a *app) serveGemini(w http.ResponseWriter, r *http.Request) {
	segment := r.PathValue("model")
	index := strings.LastIndex(segment, ":")
	if index < 1 {
		unsupportedEndpoint(w, r)
		return
	}
	model, operation := segment[:index], segment[index+1:]
	if operation != "generateContent" && operation != "streamGenerateContent" {
		unsupportedEndpoint(w, r)
		return
	}
	a.gateway.Serve(w, r, channel.FormatGemini, model, operation == "streamGenerateContent")
}

// registerGatewayRoutes 注册外部模型 API 入口（Feature B）。其他 /v1、/v1beta 路径回答“不支持的接口”。
func (a *app) registerGatewayRoutes(r *router) {
	serve := func(format channel.Format) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) { a.gateway.Serve(w, req, format, "", false) }
	}
	r.feature("B", "POST /v1/chat/completions", accessGatewayKey, serve(channel.FormatOpenAIChat))
	r.feature("B", "POST /v1/responses", accessGatewayKey, serve(channel.FormatOpenAIResponses))
	r.feature("B", "POST /v1/messages", accessGatewayKey, serve(channel.FormatAnthropic))
	r.feature("B", "POST /v1beta/models/{model}", accessGatewayKey, a.serveGemini)
	r.feature("B", "GET /v1/models", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) { a.gateway.ListModels(w, req, false) })
	r.feature("B", "GET /v1beta/models", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) { a.gateway.ListModels(w, req, true) })
	r.mux.HandleFunc("/v1/", unsupportedEndpoint)
	r.mux.HandleFunc("/v1beta/", unsupportedEndpoint)
}
