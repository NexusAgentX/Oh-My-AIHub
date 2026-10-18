package quotepricing

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"
)

// ExecutionReceipt is called only after an adapter has established a reliable
// billable creation/runtime period from this response. A first-seen ID alone
// is insufficient. Scope is the stable protected actual-key-within-group tag;
// channel, address, platform account and platform Key do not change upstream
// ownership. Only the digest is stored in the accounting receipt table.
func ExecutionReceipt(root []byte, groupID, credentialScope, executionID, period, unit string) (string, error) {
	if len(root) != 32 || groupID == "" || len(credentialScope) != 64 || executionID == "" || len(executionID) > 256 || period == "" || len(period) > 128 || (unit != "tool_session" && unit != "tool_second") || strings.ContainsAny(executionID+period, "\x00\r\n") {
		return "", ErrQuote
	}
	if _, err := hex.DecodeString(credentialScope); err != nil {
		return "", ErrQuote
	}
	mac := hmac.New(sha256.New, root)
	_, _ = mac.Write([]byte("aihub/in-call-execution-receipt/v1"))
	for _, part := range []string{groupID, credentialScope, executionID, period, unit} {
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len(part)))
		_, _ = mac.Write(length[:])
		_, _ = mac.Write([]byte(part))
	}
	return hex.EncodeToString(mac.Sum(nil)), nil
}
