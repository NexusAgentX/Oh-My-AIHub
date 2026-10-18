package detection

import "crypto/sha256"

// Transport-assigned item IDs/timestamps are not the compacted model content.
// All other fields, every sibling and their order stay in this private hash.
func codexCompactionItemHash(raw any) [32]byte {
	item := object(raw)
	content := nativeObject{}
	for name, value := range item {
		if name != "id" && name != "status" && name != "internal_chat_message_metadata_passthrough" {
			content[name] = value
		}
	}
	return sha256.Sum256(mustNativeJSON(content))
}

func codexCompactionWindow(window []any) ([][32]byte, bool) {
	if len(window) == 0 || len(window) > 512 {
		return nil, false
	}
	hashes := make([][32]byte, 0, len(window))
	found := false
	for _, raw := range window {
		item := object(raw)
		kind := str(item["type"])
		if item == nil || kind == "" {
			return nil, false
		}
		found = found || (kind == "compaction" || kind == "context_compaction") && str(item["encrypted_content"]) != ""
		hashes = append(hashes, codexCompactionItemHash(raw))
	}
	return hashes, found
}

func codexCompactionReplayed(input []any, window [][32]byte) bool {
	if len(window) == 0 || len(input) < len(window) {
		return false
	}
	// Linear substring matching also bounds repeated-prefix adversarial windows.
	prefix := make([]int, len(window))
	for i, j := 1, 0; i < len(window); i++ {
		for j > 0 && window[i] != window[j] {
			j = prefix[j-1]
		}
		if window[i] == window[j] {
			j++
		}
		prefix[i] = j
	}
	matched := 0
	for _, raw := range input {
		hash := codexCompactionItemHash(raw)
		for matched > 0 && hash != window[matched] {
			matched = prefix[matched-1]
		}
		if hash == window[matched] {
			matched++
		}
		if matched == len(window) {
			return true
		}
	}
	return false
}
