package api

import (
	"net/http"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
)

func unsupportedEndpoint(w http.ResponseWriter, r *http.Request) {
	gateway.WriteError(w, gateway.DialectOf(r), http.StatusNotFound, "unsupported_endpoint", "不支持的接口", "")
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
	manifest, err := gatewaycontract.Load()
	if err != nil {
		panic(err)
	}
	serve := func(format channel.Format) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) { a.gateway.Serve(w, req, format, "", false) }
	}
	r.implement("B", "POST /v1/chat/completions", accessGatewayKey, serve(channel.FormatOpenAIChat))
	r.implement("B", "POST /v1/responses", accessGatewayKey, serve(channel.FormatOpenAIResponses))
	r.implement("B", "POST /v1/messages", accessGatewayKey, serve(channel.FormatAnthropic))
	r.implement("B", "POST /v1beta/models/{model}", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) {
		a.serveGemini(w, req)
	})
	r.implement("B", "GET /v1/models", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) { a.gateway.ListModels(w, req, false) })
	r.implement("B", "GET /v1beta/models", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) { a.gateway.ListModels(w, req, true) })
	r.implement("499", "GET /v1/models/{model}", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) {
		a.gateway.GetModel(w, req, req.PathValue("model"), false)
	})
	r.implement("499", "GET /v1beta/models/{model}", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) {
		a.gateway.GetModel(w, req, req.PathValue("model"), true)
	})
	existing := map[string]bool{"chat.create": true, "responses.create": true, "messages.create": true, "gemini.generateContent": true, "gemini.streamGenerateContent": true}
	a.gatewayEndpoints = map[string]bool{"models.openai.list": true, "models.openai.get": true, "models.anthropic.list": true, "models.anthropic.get": true, "models.gemini.list": true, "models.gemini.get": true, "models.gemini.v1.list": true}
	for id := range existing {
		a.gatewayEndpoints[id] = true
	}
	for _, endpoint := range manifest.Endpoints {
		if endpoint.Feature != "F7" || existing[endpoint.ID] {
			continue
		}
		// Only stateless inference is proxied now: the endpoint table keeps no
		// resource, cache or token counting entrance to register.
		if endpoint.Replay != "inference" {
			continue
		}
		path := strings.NewReplacer("{completion_id}", "{completionID}", "{response_id}", "{responseID}", "{conversation_id}", "{conversationID}", "{item_id}", "{itemID}", "{cache_id}", "{cacheID}").Replace(endpoint.Path)
		r.implement("495", endpoint.Method+" "+path, accessGatewayKey, serve(protocolFormatForAPI(endpoint.Protocol)))
		a.gatewayEndpoints[endpoint.ID] = true
	}
	r.mux.HandleFunc("/v1/", unsupportedEndpoint)
	r.mux.HandleFunc("/v1beta/", unsupportedEndpoint)
	r.mux.HandleFunc("/backend-api/", unsupportedEndpoint)
}

func protocolFormatForAPI(protocol string) channel.Format {
	if protocol == "openai-images" {
		return channel.FormatOpenAIImages
	}
	return channel.FormatOpenAIResponses
}
