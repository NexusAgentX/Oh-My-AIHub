package api

import (
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/supplier"
	"strings"
	"testing"
)

func TestSupplierIconErrorMessages(t *testing.T) {
	for _, tc := range []struct {
		err          error
		code, phrase string
	}{
		{supplier.ErrIconFormat, "icon_unsupported", "格式"},
		{supplier.ErrIconTimeout, "icon_timeout", "超时"},
		{supplier.ErrIconDownload, "icon_download_failed", "下载失败"},
		{supplier.ErrIconRedirect, "icon_redirect", "跳转"},
		{supplier.ErrIconTooLarge, "icon_too_large", "大小限制"},
		{&supplier.IconHTTPError{Status: 403}, "icon_http_error", "HTTP 403"},
		{supplier.ErrIconNotFound, "icon_unavailable", "未找到"},
		{supplier.ErrInvalid, "icon_invalid_url", "HTTPS"},
	} {
		code, message := supplierIconError(tc.err)
		if code != tc.code || !strings.Contains(message, tc.phrase) {
			t.Fatalf("%v: %s %s", tc.err, code, message)
		}
	}
}
