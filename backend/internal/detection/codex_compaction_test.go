package detection

import "testing"

func TestCompactionReplayRequiresEveryOrderedSiblingAndExactOpaqueContent(t *testing.T) {
	before := nativeObject{"type": "message", "role": "user", "content": "retained prefix"}
	state := nativeObject{"type": "compaction", "encrypted_content": "opaque+cipher==", "future": true}
	after := nativeObject{"type": "message", "role": "assistant", "content": "retained suffix"}
	window, valid := codexCompactionWindow([]any{before, state, after})
	if !valid || !codexCompactionReplayed([]any{before, before, state, after}, window) {
		t.Fatal("complete ordered window/prefix overlap was not recognized")
	}
	for _, input := range [][]any{{state}, {before, state}, {after, state, before}, {before, nativeObject{"type": "compaction", "encrypted_content": "modified", "future": true}, after}, {before, nativeObject{"type": "compaction", "encrypted_content": "opaque+cipher=="}, after}} {
		if codexCompactionReplayed(input, window) {
			t.Fatal("truncated/reordered/rewritten compaction became complete replay")
		}
	}
}
