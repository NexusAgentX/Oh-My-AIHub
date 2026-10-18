package marketbrowse

import (
	"slices"
	"strconv"
	"strings"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/detection"
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
type Capabilities struct {
	Supported    []string `json:"supported"`
	ContextLower int64    `json:"context_lower"`
	OutputLower  int64    `json:"output_lower"`
}
type PublicChannel struct {
	Price        *Price       `json:"price"`
	ID           string       `json:"id"`
	Name         string       `json:"name"`
	Summary      string       `json:"summary"`
	Description  string       `json:"description"`
	GroupID      string       `json:"group_id"`
	GroupModelID string       `json:"group_model_id"`
	ModelID      string       `json:"model_id"`
	Protocol     string       `json:"protocol"`
	Discount     string       `json:"discount"`
	Capabilities Capabilities `json:"capabilities"`
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
func publicFacts(f Facts) Capabilities {
	v := Capabilities{Supported: []string{}, ContextLower: f.ContextLower, OutputLower: f.OutputLower}
	for k, yes := range f.Supported {
		if yes {
			v.Supported = append(v.Supported, k)
		}
	}
	slices.Sort(v.Supported)
	return v
}
func EvidenceFacts(protocol string, evidence map[string]bool, capacity map[string]int64) Facts {
	f := Facts{Supported: map[string]bool{}}
	has := func(family, label string) bool { return evidence["upstream_protocol/"+protocol+"."+family+"/"+label] }
	if protocol == "codex-special" {
		f.Supported["input:text"] = has("RL01", "actual_protocol_marker")
		f.Supported["output:text"] = f.Supported["input:text"]
		f.Supported["tools"] = has("RL02", "additional_tools_declaration") && has("RL02", "tool_call_association") && has("RL02", "tool_result_continuation")
		f.Supported["reasoning"] = has("RL09", "encrypted_reasoning_produced")
		f.Supported["self_contained_replay"] = has("RL09", "encrypted_reasoning_continued")
		f.Supported["output:image"] = has("RL25", "native_generation") && has("RL25", "image_model_permission")
		f.Supported["input:image"] = has("RL25", "native_edit") && has("RL25", "image_model_permission")
		f.Supported["codex_cli_basic"] = evidence["official_cli/codex-special.RL01/actual_protocol_marker"] && evidence["gateway_proxy/codex-special.RL01/actual_protocol_marker"] && f.Supported["input:text"]
		return f
	}
	if protocol == "openai-images" {
		f.Supported["output:image"] = evidence["upstream_protocol/images.generations/actual_image_model"]
		f.Supported["input:image"] = evidence["upstream_protocol/images.edits/actual_image_model"]
		return f
	}
	f.Supported["input:text"] = has("base", "accepted_native_message_shapes")
	f.Supported["output:text"] = f.Supported["input:text"]
	f.Supported["tools"] = has("tools", "native_declarations") && has("tools", "call_result_association")
	f.Supported["strict_json"] = has("output", "json_schema_strict")
	if protocol == "native-messages" {
		f.Supported["strict_json"] = has("output", "output_config.format") && has("output", "schema_nested_enum_required")
	}
	if protocol == "native-generate-content" {
		f.Supported["strict_json"] = has("output", "responseJsonSchema") && has("output", "schema_nested_enum_required")
	}
	f.Supported["json_object"] = has("output", "json_object")
	f.Supported["vision_url"] = has("vision", "url_or_uri_content_recognition")
	f.Supported["vision_inline"] = has("vision", "inline_content_recognition")
	f.Supported["input:image"] = f.Supported["vision_url"] || f.Supported["vision_inline"]
	f.Supported["input:document"] = has("document", "native_inline_content_recognition")
	f.Supported["input:audio"] = has("audio", "native_inline_input_when_applicable") || has("audio", "vendor_extension.input_audio")
	f.Supported["output:audio"] = has("audio", "native_output_when_applicable")
	f.Supported["output:image"] = has("image_tool", "native_image_output")
	f.Supported["input:video"] = has("video", "inline_video_content_recognition") || has("video", "url_video_content_recognition")
	f.Supported["reasoning"] = has("reasoning", "native_current_configuration") || has("output", "reasoning.effort") || has("output", "reasoning_effort")
	f.Supported["self_contained_replay"] = has("reasoning", "complete_opaque_signature_or_encrypted_replay") && has("reasoning", "self_contained_replay_without_resource_reference")
	// Capacity keys start with the exact protocol scenario. Never use the
	// maximum of unrelated protocol/group declarations as verified capacity.
	for k, n := range capacity {
		if !strings.HasPrefix(k, protocol+".") || !strings.Contains(k, "@upstream_protocol/") {
			continue
		}
		if strings.HasSuffix(k, "/context") {
			f.ContextLower = max(f.ContextLower, n)
		}
		if strings.HasSuffix(k, "/max_output") {
			f.OutputLower = max(f.OutputLower, n)
		}
	}
	return f
}
func projectionFacts(protocol string, projections ...detection.Projection) Facts {
	evidence := map[string]bool{}
	capacity := map[string]int64{}
	for _, p := range projections {
		if !p.Valid {
			continue
		}
		for _, c := range p.Capabilities {
			if c.State == "pass" {
				evidence[c.Layer+"/"+c.ID] = true
			}
		}
		for _, c := range p.Capacity {
			capacity[c.ScenarioID+"/"+c.Kind] = c.VerifiedLower
		}
	}
	return EvidenceFacts(protocol, evidence, capacity)
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
			for _, protocol := range []string{Protocol(format), "codex-special"} {
				if protocol == "codex-special" && ((m.ModelType == "image") != (format == channel.FormatOpenAIImages)) {
					continue
				}
				if !validProtocol(protocol, format) {
					continue
				}
				facts := projectionFacts(protocol, state.Reports[m.ID])
				o := Offering{Protocol: protocol, ModelID: m.ModelID, GroupModelID: m.ID, GroupID: m.GroupID, SupplierID: g.SupplierID, ModelType: m.ModelType, Visible: true, Enabled: true, Format: format, Facts: facts}
				for _, row := range byOffering[m.ID] {
					if !slices.Contains(row.Formats, format) {
						continue
					}
					c := row.Candidate
					cf := EvidenceFacts(protocol, c.Evidence, c.VerifiedCapacity)
					// A key's native Codex probe cannot become a public Responses
					// callable witness (or vice versa) just because wire formats match.
					endpoint := c.KeyProbeEndpoints[format]
					if protocol == "codex-special" && (format == channel.FormatOpenAIResponses && !c.Evidence["upstream_protocol/codex-special.RL14/uncompressed_request"] || format == channel.FormatOpenAIImages && !cf.Supported["output:image"]) {
						continue
					}
					if protocol != "codex-special" && strings.HasPrefix(endpoint, "codex.") && !(format == channel.FormatOpenAIResponses && cf.Supported["input:text"] || format == channel.FormatOpenAIImages && c.Evidence["upstream_protocol/images.generations/native_json_or_multipart"]) {
						continue
					}
					ch := Channel{ID: c.ChannelID, OwnerID: c.OwnerID, Visible: true, Enabled: true, Routable: true, Facts: cf}
					if amount, unit, quantity, ok := prices.LookupComparison(c.ChannelID, m.ModelID, m.GroupID, c.PricingVersion, c.MultiplierNano, fx); ok {
						ch.Price = &Price{Amount: amount, Unit: unit, Quantity: quantity}
					}
					o.Channels = append(o.Channels, ch)
					v := state.Views[c.ChannelID]
					s.Channels[ChannelKey(c.ChannelID, m.ID, protocol)] = PublicChannel{Price: ch.Price, ID: c.ChannelID, Name: v.Name, Summary: v.Summary, Description: v.Description, GroupID: g.ID, GroupModelID: m.ID, ModelID: m.ModelID, Protocol: protocol, Discount: discountText(c.MultiplierNano), Capabilities: publicFacts(cf)}
				}
				if len(o.Channels) > 0 || hasFacts(facts) {
					s.Offers = append(s.Offers, o)
					emitted = true
				}
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
func hasFacts(f Facts) bool {
	for _, yes := range f.Supported {
		if yes {
			return true
		}
	}
	return f.ContextLower > 0 || f.OutputLower > 0
}
func discountText(n int64) string {
	whole, fraction := n/1_000_000_000, n%1_000_000_000
	if fraction == 0 {
		return strconv.FormatInt(whole, 10)
	}
	return strconv.FormatInt(whole, 10) + "." + strings.TrimRight(strconv.FormatInt(fraction+1_000_000_000, 10)[1:], "0")
}
