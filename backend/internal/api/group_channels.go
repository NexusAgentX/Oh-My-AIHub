package api

import (
	"errors"
	"net/http"
	"sort"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

func (a *app) registerGroupChannelRoutes(r *router) {
	bounded := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := groupchannel.WriteContext(r.Context())
			defer cancel()
			if a.groupChannels == nil {
				writeError(w, 503, "group_channels_unavailable", "分组渠道服务暂不可用")
				return
			}
			next(w, r.WithContext(ctx))
		}
	}
	r.implement("494", "GET /api/group-channels/sources", accessReady, bounded(a.groupChannelSources))
	r.implement("494", "GET /api/group-channels", accessReady, bounded(a.listGroupChannels(false)))
	r.implement("494", "POST /api/group-channels", accessReady, bounded(a.createGroupChannel))
	r.implement("494", "GET /api/group-channels/{channelID}", accessReady, bounded(a.getGroupChannel(false)))
	r.implement("494", "PATCH /api/group-channels/{channelID}", accessReady, bounded(a.updateGroupChannel))
	r.implement("494", "DELETE /api/group-channels/{channelID}", accessReady, bounded(a.deleteGroupChannel))
	r.implement("494", "POST /api/group-channels/{channelID}/test", accessReady, bounded(a.retestGroupChannel))
	r.implement("494", "GET /api/group-channels/{channelID}/events", accessReady, bounded(a.groupChannelEvents))
	r.implement("494", "GET /api/admin/group-channels", accessAdmin, bounded(a.listGroupChannels(true)))
	r.implement("494", "GET /api/admin/group-channels/{channelID}", accessAdmin, bounded(a.getGroupChannel(true)))
	r.implement("494", "POST /api/admin/group-channels/{channelID}/moderate", accessAdmin, bounded(a.moderateGroupChannel))
}

func (a *app) groupChannelEvents(w http.ResponseWriter, r *http.Request) {
	v, err := a.groupChannels.Events(r.Context(), groupChannelActor(r, true), r.PathValue("channelID"))
	if err != nil {
		writeGroupChannelError(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"items": newChannelEventsJSON(v)})
}
func groupChannelActor(r *http.Request, admin bool) groupchannel.Actor {
	a := accountFromContext(r.Context())
	return groupchannel.Actor{ID: a.ID, Admin: admin && a.IsAdmin}
}
func writeGroupChannelError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, groupchannel.ErrNotFound):
		writeError(w, 404, "not_found", "渠道不存在")
	case errors.Is(err, groupchannel.ErrConflict):
		writeError(w, 409, "group_channel_conflict", "配置已变化，请重新载入后修改")
	case errors.Is(err, groupchannel.ErrSuspended):
		writeError(w, 409, "channel_suspended", "渠道已被管理员下架，不能自行上架")
	case errors.Is(err, groupchannel.ErrInvalid):
		writeError(w, 422, "invalid_group_channel", "请检查分组、地址选择、折扣和高级设置")
	case errors.Is(err, groupchannel.ErrUnavailable):
		writeError(w, 503, "group_channels_unavailable", "分组渠道配置正在同步，请稍后重试")
	default:
		writeDomainError(w, err)
	}
}

type groupChannelChoice struct {
	GroupModelID string `json:"group_model_id"`
	Excluded     bool   `json:"excluded"`
	Discount     string `json:"discount"`
}

func groupChannelChoices(in []groupChannelChoice) ([]groupchannel.Choice, error) {
	out := make([]groupchannel.Choice, 0, len(in))
	for _, c := range in {
		discount := c.Discount
		if discount == "" {
			discount = "1"
		}
		n, err := groupchannel.ParseDiscount(discount)
		if err != nil {
			return nil, err
		}
		out = append(out, groupchannel.Choice{GroupModelID: c.GroupModelID, Excluded: c.Excluded, DiscountNano: n})
	}
	return out, nil
}

type groupChannelInput struct {
	Revision    int64                 `json:"revision"`
	GroupID     *string               `json:"group_id"`
	APIKey      *string               `json:"api_key"`
	Name        *string               `json:"name"`
	Summary     *string               `json:"summary"`
	Description *string               `json:"description"`
	Status      *channel.Status       `json:"status"`
	AddressIDs  *[]string             `json:"address_ids"`
	Overrides   *market.Defaults      `json:"overrides"`
	Models      *[]groupChannelChoice `json:"models"`
}

func (a *app) listGroupChannels(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		items, err := a.groupChannels.List(r.Context(), groupChannelActor(r, admin))
		if err != nil {
			writeGroupChannelError(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{"items": items})
	}
}
func (a *app) getGroupChannel(admin bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		v, err := a.groupChannels.Get(r.Context(), groupChannelActor(r, admin), r.PathValue("channelID"))
		if err != nil {
			writeGroupChannelError(w, err)
			return
		}
		writeJSON(w, 200, v)
	}
}
func (a *app) createGroupChannel(w http.ResponseWriter, r *http.Request) {
	var in groupChannelInput
	if decodeJSON(w, r, &in) != nil || in.GroupID == nil || in.APIKey == nil || in.Revision != 0 || in.Status != nil {
		writeGroupChannelError(w, groupchannel.ErrInvalid)
		return
	}
	input := groupchannel.CreateInput{GroupID: *in.GroupID, APIKey: *in.APIKey}
	if in.Name != nil {
		input.Name = *in.Name
	}
	if in.Summary != nil {
		input.Summary = *in.Summary
	}
	if in.Description != nil {
		input.Description = *in.Description
	}
	if in.AddressIDs != nil {
		input.AddressIDs = *in.AddressIDs
	}
	if in.Overrides != nil {
		input.Overrides = *in.Overrides
	}
	if in.Models != nil {
		var err error
		input.Models, err = groupChannelChoices(*in.Models)
		if err != nil {
			writeGroupChannelError(w, err)
			return
		}
	}
	v, err := a.groupChannels.Create(r.Context(), groupChannelActor(r, false), input)
	if err != nil {
		writeGroupChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, v)
}
func (a *app) updateGroupChannel(w http.ResponseWriter, r *http.Request) {
	var in groupChannelInput
	if decodeJSON(w, r, &in) != nil || in.Revision < 1 {
		writeGroupChannelError(w, groupchannel.ErrInvalid)
		return
	}
	input := groupchannel.UpdateInput{Revision: in.Revision, GroupID: in.GroupID, APIKey: in.APIKey, Name: in.Name, Summary: in.Summary, Description: in.Description, Status: in.Status, AddressIDs: in.AddressIDs, Overrides: in.Overrides}
	if in.Models != nil {
		c, err := groupChannelChoices(*in.Models)
		if err != nil {
			writeGroupChannelError(w, err)
			return
		}
		input.Models = &c
	}
	v, err := a.groupChannels.Update(r.Context(), groupChannelActor(r, false), r.PathValue("channelID"), input)
	if err != nil {
		writeGroupChannelError(w, err)
		return
	}
	writeJSON(w, 200, v)
}

type groupChannelRevision struct {
	Revision int64 `json:"revision"`
}

func (a *app) deleteGroupChannel(w http.ResponseWriter, r *http.Request) {
	var in groupChannelRevision
	if decodeJSON(w, r, &in) != nil || in.Revision < 1 {
		writeGroupChannelError(w, groupchannel.ErrInvalid)
		return
	}
	if err := a.groupChannels.Delete(r.Context(), groupChannelActor(r, false), r.PathValue("channelID"), in.Revision); err != nil {
		writeGroupChannelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (a *app) retestGroupChannel(w http.ResponseWriter, r *http.Request) {
	var in groupChannelRevision
	if decodeJSON(w, r, &in) != nil || in.Revision < 1 {
		writeGroupChannelError(w, groupchannel.ErrInvalid)
		return
	}
	v, err := a.groupChannels.Retest(r.Context(), groupChannelActor(r, false), r.PathValue("channelID"), in.Revision)
	if err != nil {
		writeGroupChannelError(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, v)
}
func (a *app) moderateGroupChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Reason string `json:"reason"`
	}
	if decodeJSON(w, r, &in) != nil {
		writeGroupChannelError(w, groupchannel.ErrInvalid)
		return
	}
	if err := a.groupChannels.Moderate(r.Context(), groupChannelActor(r, true), r.PathValue("channelID"), in.Reason); err != nil {
		writeGroupChannelError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type groupChannelSourceModel struct {
	ID          string `json:"id"`
	ModelID     string `json:"model_id"`
	DisplayName string `json:"display_name"`
	ModelType   string `json:"model_type"`
}
type groupChannelSource struct {
	ID           string                     `json:"id"`
	Name         string                     `json:"name"`
	SupplierName string                     `json:"supplier_name"`
	Revision     int64                      `json:"revision"`
	Defaults     market.Defaults            `json:"defaults"`
	Addresses    []groupchannel.AddressView `json:"addresses"`
	Models       []groupChannelSourceModel  `json:"models"`
}

func (a *app) groupChannelSources(w http.ResponseWriter, r *http.Request) {
	state, err := a.groupChannels.Runtime.Current()
	if err != nil {
		writeGroupChannelError(w, err)
		return
	}
	suppliers := map[string]string{}
	for _, s := range state.Directory.Suppliers {
		if s.Enabled && s.DeletedAt == nil {
			suppliers[s.ID] = s.Name
		}
	}
	out := []groupChannelSource{}
	for _, g := range state.Directory.Groups {
		if !g.Enabled || g.DeletedAt != nil || suppliers[g.SupplierID] == "" {
			continue
		}
		v := groupChannelSource{ID: g.ID, Name: g.Name, SupplierName: suppliers[g.SupplierID], Revision: g.Revision, Defaults: g.Defaults, Addresses: []groupchannel.AddressView{}, Models: []groupChannelSourceModel{}}
		links := append([]market.GroupAddress{}, state.Directory.GroupAddresses...)
		sort.SliceStable(links, func(i, j int) bool { return links[i].Position < links[j].Position })
		for _, l := range links {
			if l.GroupID != g.ID || l.DeletedAt != nil {
				continue
			}
			for _, addr := range state.Directory.Addresses {
				if addr.ID == l.AddressID && addr.DeletedAt == nil {
					v.Addresses = append(v.Addresses, groupchannel.AddressView{ID: l.ID, Label: addr.Label, URL: addr.BaseURL})
				}
			}
		}
		for _, m := range state.Directory.Models {
			if m.GroupID == g.ID && m.Enabled && m.DeletedAt == nil {
				v.Models = append(v.Models, groupChannelSourceModel{ID: m.ID, ModelID: m.ModelID, DisplayName: m.DisplayName, ModelType: m.ModelType})
			}
		}
		sort.Slice(v.Models, func(i, j int) bool { return v.Models[i].ModelID < v.Models[j].ModelID })
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SupplierName != out[j].SupplierName {
			return out[i].SupplierName < out[j].SupplierName
		}
		return out[i].Name < out[j].Name
	})
	writeJSON(w, 200, map[string]any{"items": out})
}
