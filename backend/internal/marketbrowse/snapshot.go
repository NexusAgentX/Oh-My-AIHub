package marketbrowse

import (
	"slices"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gateway"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/groupchannel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/typical"
)

// Supplier and Group deliberately whitelist publication fields. Neither the
// contributor identity nor effective connection configuration is public.
type Supplier struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        string   `json:"kind"`
	WebsiteURL  string   `json:"website_url"`
	IconData    string   `json:"icon_data"`
	IconSource  string   `json:"icon_source"`
	Summary     string   `json:"summary"`
	Description string   `json:"description"`
	DocsURL     string   `json:"docs_url"`
	Groups      []string `json:"groups"`
}
type Group struct {
	ID          string   `json:"id"`
	SupplierID  string   `json:"supplier_id"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	StatusURL   string   `json:"status_url"`
	ModelIDs    []string `json:"model_ids"`
}
type PublicChannel struct {
	Price        *Price `json:"price"`
	ID           string `json:"id"`
	Name         string `json:"name"`
	Summary      string `json:"summary"`
	Description  string `json:"description"`
	GroupID      string `json:"group_id"`
	GroupModelID string `json:"group_model_id"`
	ModelID      string `json:"model_id"`
	Protocol     string `json:"protocol"`
	Discount     string `json:"discount"`
}
type PublicOffer struct {
	ID              string        `json:"id"`
	ModelID         string        `json:"model_id"`
	DisplayName     string        `json:"display_name"`
	GroupID         string        `json:"group_id"`
	ModelType       string        `json:"model_type"`
	DeclaredContext *int64        `json:"declared_context"`
	DeclaredOutput  *int64        `json:"declared_output"`
	Reference       bool          `json:"reference"`
	Quote           *market.Quote `json:"-"` // API maps integer nano amounts to contract decimal strings.
}
type Snapshot struct {
	Version   string
	Offers    []Offering
	Suppliers map[string]Supplier
	Groups    map[string]Group
	Models    map[string]PublicOffer
	Channels  map[string]PublicChannel // channel + offering + protocol
}

func Protocol(format channel.Format) string {
	switch format {
	case channel.FormatOpenAIChat:
		return "openai-chat-completions"
	case channel.FormatOpenAIResponses:
		return "openai-responses"
	case channel.FormatAnthropic:
		return "native-messages"
	case channel.FormatGemini:
		return "native-generate-content"
	case channel.FormatOpenAIImages:
		return "openai-images"
	}
	return ""
}
func ChannelKey(channel, offering, protocol string) string {
	return channel + "\x00" + offering + "\x00" + protocol
}

// Build uses the same approved immutable candidates as inference. Report
// projection happened during publication, so browsing does not hash detector
// sources, re-resolve credentials, inspect upstream lists or query SQL.
func Build(state *groupchannel.State, prices *typical.PriceSnapshot, fx int64) (*Snapshot, error) {
	s := &Snapshot{Version: state.Version + "/" + prices.Version() + "/" + strconv.FormatInt(fx, 10), Offers: []Offering{}, Suppliers: map[string]Supplier{}, Groups: map[string]Group{}, Models: map[string]PublicOffer{}, Channels: map[string]PublicChannel{}}
	for _, v := range state.Directory.Suppliers {
		if v.Enabled && v.DeletedAt == nil {
			s.Suppliers[v.ID] = Supplier{ID: v.ID, Name: v.Name, Kind: v.Kind, WebsiteURL: v.WebsiteURL, IconData: v.IconData, IconSource: v.IconSource, Summary: v.Summary, Description: v.Description, DocsURL: v.DocsURL, Groups: []string{}}
		}
	}
	for _, v := range state.Directory.Groups {
		parent, ok := s.Suppliers[v.SupplierID]
		if !ok || !v.Enabled || v.DeletedAt != nil {
			continue
		}
		s.Groups[v.ID] = Group{ID: v.ID, SupplierID: v.SupplierID, Name: v.Name, Description: v.Description, StatusURL: v.StatusURL, ModelIDs: []string{}}
		parent.Groups = append(parent.Groups, v.ID)
		s.Suppliers[v.SupplierID] = parent
	}
	quotes := map[string]market.Quote{}
	for _, q := range state.Directory.Quotes {
		quotes[q.ID] = q
	}
	blocked := map[string]bool{}
	for _, v := range state.Directory.Governance {
		blocked[v.ModelID] = v.Disabled
	}
	byOffering := map[string][]gateway.CandidateRecord{}
	for _, r := range state.Candidates {
		byOffering[r.Candidate.GroupModelID] = append(byOffering[r.Candidate.GroupModelID], r)
	}
	for _, m := range state.Directory.Models {
		g, ok := s.Groups[m.GroupID]
		if !ok || !m.Enabled || m.DeletedAt != nil || blocked[m.ModelID] {
			continue
		}
		g.ModelIDs = append(g.ModelIDs, m.ModelID)
		s.Groups[g.ID] = g
		public := PublicOffer{ID: m.ID, ModelID: m.ModelID, DisplayName: m.DisplayName, GroupID: m.GroupID, ModelType: m.ModelType, DeclaredContext: m.ContextWindow, DeclaredOutput: m.MaxOutputTokens, Reference: m.IsReference}
		if m.QuoteID != nil {
			if q, ok := quotes[*m.QuoteID]; ok && q.GroupModelID == m.ID {
				public.Quote = &q
			}
		}
		s.Models[m.ID] = public
		emitted := false
		for _, format := range []channel.Format{channel.FormatOpenAIChat, channel.FormatOpenAIResponses, channel.FormatAnthropic, channel.FormatGemini, channel.FormatOpenAIImages} {
			if m.ModelType != "image" && format == channel.FormatOpenAIImages {
				continue
			}
			protocol := Protocol(format)
			if !validProtocol(protocol, format) {
				continue
			}
			o := Offering{Protocol: protocol, ModelID: m.ModelID, GroupModelID: m.ID, GroupID: m.GroupID, SupplierID: g.SupplierID, ModelType: m.ModelType, Visible: true, Enabled: true, Format: format}
			for _, row := range byOffering[m.ID] {
				if !slices.Contains(row.Formats, format) {
					continue
				}
				c := row.Candidate
				ch := Channel{ID: c.ChannelID, OwnerID: c.OwnerID, Visible: true, Enabled: true, Routable: true}
				if amount, unit, quantity, ok := prices.LookupComparison(c.ChannelID, m.ModelID, m.GroupID, c.PricingVersion, c.MultiplierNano, fx); ok {
					ch.Price = &Price{Amount: amount, Unit: unit, Quantity: quantity}
				}
				o.Channels = append(o.Channels, ch)
				v := state.Views[c.ChannelID]
				s.Channels[ChannelKey(c.ChannelID, m.ID, protocol)] = PublicChannel{Price: ch.Price, ID: c.ChannelID, Name: v.Name, Summary: v.Summary, Description: v.Description, GroupID: g.ID, GroupModelID: m.ID, ModelID: m.ModelID, Protocol: protocol, Discount: discountText(c.MultiplierNano)}
			}
			if len(o.Channels) > 0 {
				s.Offers = append(s.Offers, o)
				emitted = true
			}
		}
		if !emitted {
			s.Offers = append(s.Offers, Offering{ModelID: m.ModelID, GroupModelID: m.ID, GroupID: g.ID, SupplierID: g.SupplierID, ModelType: m.ModelType, Visible: true, Enabled: true})
		}
	}
	for id, g := range s.Groups {
		slices.Sort(g.ModelIDs)
		g.ModelIDs = slices.Compact(g.ModelIDs)
		s.Groups[id] = g
	}
	for id, p := range s.Suppliers {
		slices.Sort(p.Groups)
		s.Suppliers[id] = p
	}
	return s, nil
}
func discountText(n int64) string {
	whole, fraction := n/1_000_000_000, n%1_000_000_000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + strings.TrimRight(strconv.FormatInt(fraction+1_000_000_000, 10)[1:], "0")
}
