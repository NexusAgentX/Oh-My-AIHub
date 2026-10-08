package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ledger"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/settings"
)

func TestCatalogAndSettingsPersistWithAudit(t *testing.T) {
	pool, store := isolatedDatabase(t)
	ctx := context.Background()
	_, admin, _ := accounts(t, store)
	service := catalog.NewService(store.Catalog)
	start, end := int16(540), int16(720)
	minimum := int64(200_000)
	window := 128000
	contextWindow := int64(window)
	created, err := service.Create(ctx, admin.ID, catalog.Model{
		ID: "deepseek-chat", DisplayName: "DeepSeek Chat", Enabled: true, ContextWindow: &contextWindow,
		InputPrice: mustAmount(t, "1.5"), OutputPrice: mustAmount(t, "4.5"),
		PriceTiers: []ledger.PriceTier{
			{Name: "长上下文", MinPromptTokens: &minimum, InputPrice: mustAmount(t, "2")},
			{Name: "高峰", Timezone: "Asia/Shanghai", Weekdays: []int{5, 1}, StartMinute: &start, EndMinute: &end, InputPrice: mustAmount(t, "3")},
		},
	})
	if err != nil || len(created.PriceTiers) != 2 || created.PriceTiers[1].Weekdays[0] != 1 || created.InputModalities[0] != "text" {
		t.Fatalf("created = %+v, %v", created, err)
	}
	if _, err := service.Create(ctx, admin.ID, created); !errors.Is(err, catalog.ErrConflict) {
		t.Fatalf("duplicate error = %v", err)
	}
	disabled := false
	single := []ledger.PriceTier{created.PriceTiers[1]}
	updated, err := service.Update(ctx, admin.ID, "deepseek-chat", catalog.ModelPatch{Enabled: &disabled, PriceTiers: &single})
	if err != nil || updated.Enabled || len(updated.PriceTiers) != 1 || updated.PriceTiers[0].Name != "高峰" || updated.OutputPrice != mustAmount(t, "4.5") {
		t.Fatalf("updated = %+v, %v", updated, err)
	}
	enabledOnly, err := service.List(ctx, false)
	if err != nil || len(enabledOnly) != 0 {
		t.Fatalf("enabled list = %+v, %v", enabledOnly, err)
	}
	all, err := service.List(ctx, true)
	if err != nil || len(all) != 1 || len(all[0].PriceTiers) != 1 {
		t.Fatalf("admin list = %+v, %v", all, err)
	}
	if _, err := service.Update(ctx, admin.ID, "missing", catalog.ModelPatch{Enabled: &disabled}); !errors.Is(err, catalog.ErrNotFound) {
		t.Fatalf("missing model error = %v", err)
	}

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
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action IN ('model.created', 'model.updated', 'settings.updated') AND actor_id = $1`, admin.ID).Scan(&audits); err != nil || audits != 3 {
		t.Fatalf("audit rows = %d, %v", audits, err)
	}
}
