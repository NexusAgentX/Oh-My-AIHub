package catalog

import "testing"

func TestValidateModelIDRejectsAmbiguousPathSegments(t *testing.T) {
	base := Model{
		Name:             "Test Model",
		Provider:         "Provider",
		ContextWindow:    1,
		InputModalities:  []string{"text"},
		OutputModalities: []string{"text"},
		Status:           StatusActive,
	}
	for _, id := range []string{"provider//model", "provider/./model", "provider/../model", "/model", "provider/model/", "provider model"} {
		model := base
		model.ID = id
		if err := validate(model); err == nil {
			t.Fatalf("validate model ID %q unexpectedly succeeded", id)
		}
	}

	for _, id := range []string{"gpt-5", "openai/gpt-5.2", "vendor/family/model:latest"} {
		model := base
		model.ID = id
		if err := validate(model); err != nil {
			t.Fatalf("validate model ID %q: %v", id, err)
		}
	}
}
