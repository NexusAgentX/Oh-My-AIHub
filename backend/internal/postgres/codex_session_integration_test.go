package postgres_test

import (
	"context"
	"sync"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/codexsession"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/postgres/resourcepg"
)

func TestCodexSessionConcurrentClaimsNeverReplaceOriginalAndSurviveRestart(t *testing.T) {
	pool, _ := isolatedDatabase(t)
	repo := resourcepg.NewStore(pool)
	id := codexsession.Identity{AccountID: marketID(t), KeyID: marketID(t), SessionID: marketID(t)}
	ctx := context.Background()
	var wg sync.WaitGroup
	results := make(chan codexsession.Route, 16)
	for i := 0; i < 16; i++ {
		route := codexsession.Route{Binding: gatewaycontract.Binding{GroupID: marketID(t), ChannelID: marketID(t), CredentialVersion: "1"}, CredentialScope: "synthetic-scope"}
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, err := repo.ClaimSession(ctx, id, route)
			if err != nil {
				t.Error(err)
				return
			}
			results <- got
		}()
	}
	wg.Wait()
	close(results)
	winner, err := resourcepg.NewStore(pool).LookupSession(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	for got := range results {
		if got != winner {
			t.Fatal("concurrent UPSERT changed the route")
		}
	}
	other := id
	other.KeyID = marketID(t)
	if _, err = repo.LookupSession(ctx, other); err != gatewaycontract.ErrNotFound {
		t.Fatal("platform Key scope bypass", err)
	}
}
