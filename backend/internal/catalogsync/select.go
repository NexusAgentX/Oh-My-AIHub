package catalogsync

import (
	"sort"
	"unicode/utf8"
)

type Notice struct {
	ModelID   string `json:"model_id"`
	SourceKey string `json:"source_key"`
	Reason    string `json:"reason"`
}

// Select uses the administrator's provider order, then the complete source key.
// Excluded and shadowed records are never deletion tombstones.
func Select(entries []Entry, providers []string) ([]Entry, []Notice, []string) {
	ranks := map[string]int{}
	for i, p := range providers {
		ranks[p] = i
	}
	available := map[string]bool{}
	candidates := []Entry{}
	for _, e := range entries {
		p := e.Model.Provider
		if p != "" {
			available[p] = true
		}
		if _, ok := ranks[p]; ok {
			candidates = append(candidates, e)
		}
	}
	options := []string{}
	for p := range available {
		options = append(options, p)
	}
	sort.Strings(options)
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if ranks[a.Model.Provider] != ranks[b.Model.Provider] {
			return ranks[a.Model.Provider] < ranks[b.Model.Provider]
		}
		return a.Key < b.Key
	})
	chosen := []Entry{}
	notices := []Notice{}
	names := map[string]string{}
	for _, e := range candidates {
		if !legalID.MatchString(e.Model.ID) || utf8.RuneCountInString(e.Model.DisplayName) > 128 {
			notices = append(notices, Notice{e.Model.ID, e.Key, "模型标识字符或名称长度超出支持范围，未导入"})
			continue
		}
		if first, ok := names[e.Model.ID]; ok {
			notices = append(notices, Notice{e.Model.ID, e.Key, "同名来源优先选择 " + first})
			continue
		}
		names[e.Model.ID] = e.Key
		chosen = append(chosen, e)
	}
	sort.Slice(chosen, func(i, j int) bool { return chosen[i].Model.ID < chosen[j].Model.ID })
	return chosen, notices, options
}
