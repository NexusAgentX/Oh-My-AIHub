package postgres_test

import (
	"context"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestSettingsPersistWithAudit(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	_, admin, _ := accounts(t, store)
	settingsService := settings.NewService(store.Settings)
	current, err := settingsService.Get(ctx)
	if err != nil || current.FeeRateNano != 1_000_000 || current.C2CPaymentTimeoutMinutes != 30 || len(current.ExtraBlockedHosts) != 0 {
		t.Fatalf("settings = %+v, %v", current, err)
	}
	current.FeeRateNano = 2_000_000
	current.ExtraBlockedHosts = []string{"Relay.Example."}
	saved, err := settingsService.Update(ctx, admin.ID, current)
	if err != nil || saved.FeeRateNano != 2_000_000 || saved.ExtraBlockedHosts[0] != "relay.example" {
		t.Fatalf("saved = %+v, %v", saved, err)
	}

	var audits int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'settings.updated' AND actor_id = $1`, admin.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatalf("audit rows = %d, %v", audits, err)
	}
}
