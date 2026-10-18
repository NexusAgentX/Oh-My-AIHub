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
	endpoints := map[string]gatewaycontract.Endpoint{}
	for _, endpoint := range manifest.Endpoints {
		endpoints[endpoint.ID] = endpoint
	}
	serve := func(format channel.Format) http.HandlerFunc {
		return func(w http.ResponseWriter, req *http.Request) { a.gateway.Serve(w, req, format, "", false) }
	}
	r.implement("B", "POST /v1/chat/completions", accessGatewayKey, serve(channel.FormatOpenAIChat))
	r.implement("B", "POST /v1/responses", accessGatewayKey, serve(channel.FormatOpenAIResponses))
	r.implement("B", "GET /v1/responses/{responseID}", accessGatewayKey, func(w http.ResponseWriter, r *http.Request) {
		if a.gateway.Resources != nil {
			a.gateway.ServeAuxiliary(w, r, endpoints["responses.get"])
		} else {
			a.gateway.ServeResponse(w, r, false)
		}
	})
	r.implement("B", "POST /v1/responses/{responseID}/cancel", accessGatewayKey, func(w http.ResponseWriter, r *http.Request) {
		if a.gateway.Resources != nil {
			a.gateway.ServeAuxiliary(w, r, endpoints["responses.cancel"])
		} else {
			a.gateway.ServeResponse(w, r, true)
		}
	})
	r.implement("B", "POST /v1/messages", accessGatewayKey, serve(channel.FormatAnthropic))
	r.implement("B", "POST /v1beta/models/{model}", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) {
		if strings.HasSuffix(req.PathValue("model"), ":countTokens") {
			a.gateway.ServeAuxiliary(w, req, endpoints["gemini.countTokens"])
			return
		}
		a.serveGemini(w, req)
	})
	r.implement("B", "GET /v1/models", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) { a.gateway.ListModels(w, req, false) })
	r.implement("B", "GET /v1beta/models", accessGatewayKey, func(w http.ResponseWriter, req *http.Request) { a.gateway.ListModels(w, req, true) })
	existing := map[string]bool{"chat.create": true, "responses.create": true, "responses.get": true, "responses.cancel": true, "messages.create": true, "gemini.generateContent": true, "gemini.streamGenerateContent": true, "gemini.countTokens": true}
	for _, endpoint := range manifest.Endpoints {
		if endpoint.Feature == "F8" {
			handler := a.gateway.CodexHandler(endpoint)
			if handler != nil {
				r.implement("497", endpoint.Method+" "+endpoint.Path, accessGatewayKey, handler)
			}
			continue
		}
		if endpoint.Feature != "F7" || existing[endpoint.ID] {
			continue
		}
		path := strings.NewReplacer("{completion_id}", "{completionID}", "{response_id}", "{responseID}", "{conversation_id}", "{conversationID}", "{item_id}", "{itemID}", "{cache_id}", "{cacheID}").Replace(endpoint.Path)
		var handler http.HandlerFunc
		if endpoint.Replay == "inference" {
			handler = serve(protocolFormatForAPI(endpoint.Protocol))
		} else {
			handler = func(w http.ResponseWriter, req *http.Request) { a.gateway.ServeAuxiliary(w, req, endpoint) }
		}
		r.implement("495", endpoint.Method+" "+path, accessGatewayKey, handler)
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
