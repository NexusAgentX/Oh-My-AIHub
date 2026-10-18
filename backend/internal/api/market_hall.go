package api

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/marketbrowse"
)

type marketHallChannel struct {
	ID           string           `json:"id"`
	Name         string           `json:"name"`
	Summary      string           `json:"summary"`
	Description  string           `json:"description"`
	GroupID      string           `json:"group_id"`
	GroupName    string           `json:"group_name"`
	SupplierID   string           `json:"supplier_id"`
	SupplierName string           `json:"supplier_name"`
	IsMine       bool             `json:"is_mine"`
	Models       []string         `json:"models"`
	Formats      []channel.Format `json:"formats"`
}
type marketHallPage struct {
	Items      []marketHallChannel `json:"items"`
	Total      int                 `json:"total"`
	NextCursor *string             `json:"next_cursor"`
}

func marketHall(s *marketbrowse.Snapshot, state *groupchannel.State, actor string, q marketbrowse.Query) ([]marketHallChannel, error) {
	matched, err := marketbrowse.FilterAll(s.Offers, marketbrowse.Scope{Version: s.Version, ActorID: actor}, q)
	if err != nil {
		return nil, err
	}
	byID := map[string]*marketHallChannel{}
	for _, m := range matched.Items {
		for _, w := range m.Offers {
			for _, id := range w.ChannelIDs {
				c := byID[id]
				if c == nil {
					v := s.Channels[marketbrowse.ChannelKey(id, w.GroupModelID, w.Protocol)]
					g := s.Groups[w.GroupID]
					c = &marketHallChannel{ID: id, Name: v.Name, Summary: v.Summary, Description: v.Description, GroupID: w.GroupID, GroupName: g.Name, SupplierID: g.SupplierID, SupplierName: s.Suppliers[g.SupplierID].Name, IsMine: state.Views[id].Owner.ID == actor, Models: []string{}, Formats: []channel.Format{}}
					byID[id] = c
				}
				c.Models = append(c.Models, m.ID)
				c.Formats = append(c.Formats, w.Format)
			}
		}
	}
	out := make([]marketHallChannel, 0, len(byID))
	for _, v := range byID {
		slices.Sort(v.Models)
		v.Models = slices.Compact(v.Models)
		slices.Sort(v.Formats)
		v.Formats = slices.Compact(v.Formats)
		out = append(out, *v)
	}
	slices.SortFunc(out, func(a, b marketHallChannel) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}
func (a *app) listMarketChannels(w http.ResponseWriter, r *http.Request) {
	s, state, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	q, limit, err := marketQuery(r)
	if err != nil || limit < 1 || limit > 100 {
		marketError(w, marketbrowse.ErrFilter)
		return
	}
	actor := accountFromContext(r.Context()).ID
	rows, err := marketHall(s, state, actor, q)
	if err != nil {
		marketError(w, err)
		return
	}
	search := strings.TrimSpace(r.URL.Query().Get("channel_q"))
	if len(search) > 256 {
		marketError(w, marketbrowse.ErrFilter)
		return
	}
	rows = slices.DeleteFunc(rows, func(c marketHallChannel) bool {
		return !strings.Contains(strings.ToLower(c.Name+" "+c.Summary), strings.ToLower(search))
	})
	encoded, _ := json.Marshal(q)
	sum := sha256.Sum256([]byte(s.Version + "\x00" + actor + "\x00" + search + "\x00" + strconv.Itoa(limit) + "\x00" + string(encoded)))
	binding := hex.EncodeToString(sum[:])
	start := 0
	if token := r.URL.Query().Get("cursor"); token != "" {
		if len(token) > 4096 {
			marketError(w, marketbrowse.ErrCursor)
			return
		}
		raw, err := base64.RawURLEncoding.DecodeString(token)
		var c struct{ Scope, After string }
		if err != nil || json.Unmarshal(raw, &c) != nil {
			marketError(w, marketbrowse.ErrCursor)
			return
		}
		if c.Scope != binding {
			marketError(w, marketbrowse.ErrPageChanged)
			return
		}
		index := slices.IndexFunc(rows, func(v marketHallChannel) bool { return v.ID == c.After })
		if index < 0 {
			marketError(w, marketbrowse.ErrCursor)
			return
		}
		start = index + 1
	}
	end := min(start+limit, len(rows))
	out := marketHallPage{Items: rows[start:end], Total: len(rows)}
	if end < len(rows) {
		raw, _ := json.Marshal(struct{ Scope, After string }{binding, rows[end-1].ID})
		next := base64.RawURLEncoding.EncodeToString(raw)
		out.NextCursor = &next
	}
	writeJSON(w, 200, out)
}
func (a *app) getMarketChannel(w http.ResponseWriter, r *http.Request) {
	s, state, ok := a.marketSnapshot(w, r)
	if !ok {
		return
	}
	rows, err := marketHall(s, state, accountFromContext(r.Context()).ID, marketbrowse.Query{})
	if err != nil {
		marketError(w, err)
		return
	}
	for _, v := range rows {
		if v.ID == r.PathValue("channelID") {
			writeJSON(w, 200, v)
			return
		}
	}
	writeError(w, 404, "channel_not_found", "渠道不存在或已下架")
}
