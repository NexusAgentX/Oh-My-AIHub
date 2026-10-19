package api

import (
	"errors"
	"fmt"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
	"net/http"
)

type supplierIconJSON struct {
	IconData   string `json:"icon_data"`
	IconSource string `json:"icon_source"`
}

func (a *app) fetchSupplierIcon(w http.ResponseWriter, r *http.Request) {
	if !a.channelProbes.take(accountFromContext(r.Context()).ID) {
		writeError(w, 429, "rate_limited", "请求过于频繁，请稍后重试")
		return
	}
	var input struct {
		WebsiteURL string `json:"website_url"`
	}
	if decodeJSON(w, r, &input) != nil {
		writeInvalidJSON(w)
		return
	}
	// Apply the current administrator-maintained egress block list too.
	settings, err := a.settings.Get(r.Context())
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if a.gateway == nil || a.gateway.Outbound == nil {
		writeError(w, 503, "outbound_unavailable", "供应商图标服务暂不可用")
		return
	}
	policy, err := a.gateway.Outbound.WithExtraBlockedHosts(settings.ExtraBlockedHosts)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	service := supplier.Service{Outbound: policy}
	data, source, err := service.FetchIcon(r.Context(), input.WebsiteURL)
	if err != nil {
		code, message := supplierIconError(err)
		writeError(w, 422, code, message)
		return
	}
	writeJSON(w, 200, supplierIconJSON{IconData: data, IconSource: source})
}

// Only expose classified failures, never upstream bodies or transport errors.
func supplierIconError(err error) (string, string) {
	var httpError *supplier.IconHTTPError
	switch {
	case errors.Is(err, supplier.ErrInvalid):
		return "icon_invalid_url", "请填写有效的 HTTPS 官网地址"
	case errors.Is(err, supplier.ErrIconTimeout):
		return "icon_timeout", "获取图标超时，请重试或上传图片"
	case errors.Is(err, supplier.ErrIconFormat):
		return "icon_unsupported", "图标格式不支持或文件损坏，请上传 PNG、JPEG 或 GIF"
	case errors.Is(err, supplier.ErrIconTooLarge):
		return "icon_too_large", "网页或图标超过大小限制，请上传 256 KB、1024 × 1024 以内的图片"
	case errors.As(err, &httpError):
		return "icon_http_error", fmt.Sprintf("官网或图标服务器返回 HTTP %d，可上传图片", httpError.Status)
	case errors.Is(err, supplier.ErrIconRedirect):
		return "icon_redirect", "官网或图标地址发生跳转，请填写跳转后的官网地址或上传图片"
	case errors.Is(err, supplier.ErrIconDownload):
		return "icon_download_failed", "图标下载失败，请检查官网地址或上传图片"
	default:
		return "icon_unavailable", "未找到可用图标，可上传图片或保留默认图标"
	}
}
