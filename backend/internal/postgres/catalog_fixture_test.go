package postgres_test

import (
	"context"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/catalog"
)

// Legacy snapshot fixtures are seeded directly while the final schema fixture
// is converted. No real handler exposes global model mutation anymore.
func (e *env) addModelFixture(model catalog.Model) {
	e.t.Helper()
	if _, err := e.catalog.Create(context.Background(), e.admin.id, model); err != nil {
		e.t.Fatal(err)
	}
}
func (e *env) patchModelFixture(id string, patch catalog.ModelPatch) {
	e.t.Helper()
	if _, err := e.catalog.Update(context.Background(), e.admin.id, id, patch); err != nil {
		e.t.Fatal(err)
	}
}
