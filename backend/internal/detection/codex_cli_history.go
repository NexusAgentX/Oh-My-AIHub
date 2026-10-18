package detection

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexprobe"
)

func (b *codexBridge) serveHistoryWindow(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+b.token)) != 1 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	b.mu.Lock()
	window := b.historyWindow
	alternate := b.historyAlternate
	if b.historyChosen != "" {
		window, alternate = b.historyChosen, ""
	}
	b.mu.Unlock()
	if window == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"window": window, "alternate": alternate})
}

func codexDeniedPhases(phases []codexprobe.Phase, executions []codexContextExecution) map[string]map[string]int {
	out := map[string]map[string]int{}
	for _, phase := range phases {
		out[phase.Kind] = map[string]int{}
		for _, execution := range executions {
			if execution.Denied && execution.Start >= phase.Start && execution.End >= execution.Start && execution.End <= phase.End {
				out[phase.Kind][execution.Endpoint]++
			}
		}
	}
	return out
}

// Returned window/item listings may belong to a broken shared upstream. The
// controlled CLI may only read the window this invocation actually ingested;
// it cannot follow other windows supplied in an opaque listing.
func safeCLIHistoryOutput(root nativeObject, window string, alternates ...string) bool {
	allowed := func(value string) bool {
		if value == window && value != "" {
			return true
		}
		for _, alternate := range alternates {
			if value == alternate && value != "" {
				return true
			}
		}
		return false
	}
	if len(list(root["output"])) == 0 {
		return false
	}
	for _, raw := range list(root["output"]) {
		item := object(raw)
		switch str(item["type"]) {
		case "message", "reasoning":
		case "function_call":
			if str(item["namespace"]) != "history" {
				return false
			}
			name := str(item["name"])
			if name != "list_windows" && name != "list_items" && name != "read_item" && name != "search_contents" {
				return false
			}
			args, err := decodeNative([]byte(str(item["arguments"])))
			if err != nil {
				return false
			}
			if name != "list_windows" && !allowed(str(args["window_id"])) {
				return false
			}
			for key, value := range args {
				switch key {
				case "agent_name":
					if value != nil && str(value) != "/root" {
						return false
					}
				case "window_id":
					if !allowed(str(value)) {
						return false
					}
				case "item_id":
					if text := str(value); text == "" || len(text) > 512 {
						return false
					}
				case "limit":
					if n, ok := integer(value); !ok || n < 1 || n > 8 {
						return false
					}
				case "max_chars_per_item", "limit_chars":
					if n, ok := integer(value); !ok || n < 1 || n > 4096 {
						return false
					}
				case "offset_chars":
					if n, ok := integer(value); !ok || n != 0 {
						return false
					}
				case "query":
					if text := str(value); text == "" || len(text) > 64<<10 {
						return false
					}
				case "recent_first":
					if _, ok := value.(bool); !ok {
						return false
					}
				case "role", "tool_namespace", "tool_name":
					if value != nil && len(str(value)) > 64 {
						return false
					}
				default:
					return false
				}
			}
		default:
			return false
		}
	}
	return true
}
