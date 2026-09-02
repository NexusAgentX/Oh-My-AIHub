package ledger

import (
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/money"
)

func TestValidatePostRequestRejectsInvalidLedgerShapes(t *testing.T) {
	valid := PostRequest{
		IdempotencyKey: "posting-shape",
		Kind:           TransactionTransfer,
		Reason:         "settlement",
		ReferenceType:  "api_call",
		ReferenceID:    "call-1",
		Entries: []Posting{
			{Account: UserAccount("consumer"), BusinessRole: EntryRoleConsumer, Amount: money.FromNano(-1)},
			{Account: UserAccount("provider"), BusinessRole: EntryRoleProvider, Amount: money.FromNano(1)},
		},
	}
	if err := ValidatePostRequest(valid); err != nil {
		t.Fatalf("valid request: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(*PostRequest)
		wantErr error
	}{
		{
			name: "fewer than two entries",
			mutate: func(request *PostRequest) {
				request.Entries = request.Entries[:1]
			},
			wantErr: ErrInvalidInput,
		},
		{
			name: "zero amount entry",
			mutate: func(request *PostRequest) {
				request.Entries[0].Amount = 0
			},
			wantErr: ErrInvalidInput,
		},
		{
			name: "unbalanced total",
			mutate: func(request *PostRequest) {
				request.Entries[1].Amount = money.FromNano(2)
			},
			wantErr: ErrUnbalanced,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := valid
			request.Entries = append([]Posting(nil), valid.Entries...)
			test.mutate(&request)
			if err := ValidatePostRequest(request); !errors.Is(err, test.wantErr) {
				t.Fatalf("ValidatePostRequest error = %v, want %v", err, test.wantErr)
			}
		})
	}
}
