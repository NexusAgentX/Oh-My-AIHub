package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

func (a *app) registerTypicalRoutes(r *router) {
	bounded := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if a.typical == nil {
				writeError(w, 503, "typical_unavailable", "典型费用暂不可用")
				return
			}
			next(w, r)
		}
	}
	r.implement("496", "GET /api/typical-costs", accessReady, bounded(a.getTypicalCosts))
	r.implement("496", "GET /api/admin/typical-costs/settings", accessAdmin, bounded(a.getTypicalSettings))
	r.implement("496", "PUT /api/admin/typical-costs/settings", accessAdmin, bounded(a.setTypicalSettings))
	r.implement("496", "POST /api/admin/typical-costs/refresh", accessAdmin, bounded(a.refreshTypicalCosts))
}
func typicalError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, typical.ErrInvalid):
		writeError(w, 422, "invalid_typical_settings", "统计参数无效，请检查范围和正整数")
	case errors.Is(err, typical.ErrConflict), errors.Is(err, typical.ErrStale):
		writeError(w, 409, "typical_inputs_changed", "统计参数已变化，请重新载入")
	case errors.Is(err, typical.ErrBusy):
		writeError(w, 409, "typical_busy", "更新已排队，请稍后查看")
	default:
		writeDomainError(w, err)
	}
}
func (a *app) getTypicalSettings(w http.ResponseWriter, r *http.Request) {
	c, err := a.typical.Config(r.Context())
	if err != nil {
		typicalError(w, err)
		return
	}
	writeJSON(w, 200, c)
}
func (a *app) setTypicalSettings(w http.ResponseWriter, r *http.Request) {
	var c typical.Config
	if decodeJSON(w, r, &c) != nil {
		writeError(w, 422, "invalid_typical_settings", "统计参数无效")
		return
	}
	c, err := a.typical.Update(r.Context(), accountFromContext(r.Context()).ID, c)
	if err != nil {
		typicalError(w, err)
		return
	}
	writeJSON(w, 200, c)
}
func (a *app) refreshTypicalCosts(w http.ResponseWriter, r *http.Request) {
	if err := a.typical.Trigger(); err != nil {
		typicalError(w, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"queued": true})
}

type typicalMeta struct {
	USDCNYNano   int64          `json:"usd_cny_rate_nano"`
	FeeRateNano  int64          `json:"fee_rate_nano"`
	Version      string         `json:"version"`
	Config       typical.Config `json:"config"`
	InputVersion string         `json:"input_version"`
	UpdatedAt    time.Time      `json:"updated_at"`
	WindowStart  time.Time      `json:"window_start"`
	Watermark    time.Time      `json:"watermark"`
	ReconciledAt time.Time      `json:"reconciled_at"`
}
type typicalPage struct {
	Snapshot   *typicalMeta       `json:"snapshot"`
	Status     typical.Status     `json:"status"`
	Items      []typical.Estimate `json:"items"`
	NextCursor *string            `json:"next_cursor"`
}

func typicalKey(row typical.Estimate) string {
	return row.ModelID + "\x00" + row.Level + "\x00" + row.GroupID + "\x00" + row.ChannelID
}
func (a *app) getTypicalCosts(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if value := r.URL.Query().Get("limit"); value != "" {
		v, err := strconv.Atoi(value)
		if err != nil || v < 1 || v > 100 {
			writeBadCursor(w)
			return
		}
		limit = v
	}
	model, level := r.URL.Query().Get("model_id"), r.URL.Query().Get("level")
	if model != "" && !gatewaycontract.ValidModelID(model) || level != "" && level != "model" && level != "group" && level != "channel" {
		writeBadCursor(w)
		return
	}
	snap, status := a.typical.Snapshot()
	page := typicalPage{Status: status, Items: []typical.Estimate{}}
	if snap == nil {
		if r.URL.Query().Get("cursor") != "" {
			writeBadCursor(w)
			return
		}
		writeJSON(w, 200, page)
		return
	}
	page.Snapshot = &typicalMeta{snap.USDCNYNano, snap.FeeRateNano, snap.Version, snap.Config, snap.InputVersion, snap.UpdatedAt, snap.WindowStart, snap.Watermark, snap.ReconciledAt}
	sum := sha256.Sum256([]byte(snap.InputVersion + snap.UpdatedAt.Format(time.RFC3339Nano) + "\x00" + model + "\x00" + level + "\x00" + strconv.Itoa(limit)))
	scope := hex.EncodeToString(sum[:])
	after := ""
	if token := r.URL.Query().Get("cursor"); token != "" {
		raw, err := base64.RawURLEncoding.DecodeString(token)
		var c struct{ Scope, After string }
		if len(token) > 4096 || err != nil || json.Unmarshal(raw, &c) != nil || c.Scope != scope {
			writeError(w, 409, "typical_page_changed", "统计已更新，请从第一页重新载入")
			return
		}
		after = c.After
	}
	for _, row := range snap.Items {
		if model != "" && row.ModelID != model || level != "" && row.Level != level || typicalKey(row) <= after {
			continue
		}
		if len(page.Items) == limit {
			last := page.Items[len(page.Items)-1]
			raw, _ := json.Marshal(struct{ Scope, After string }{scope, typicalKey(last)})
			token := base64.RawURLEncoding.EncodeToString(raw)
			page.NextCursor = &token
			break
		}
		page.Items = append(page.Items, row)
	}
	writeJSON(w, 200, page)
}
