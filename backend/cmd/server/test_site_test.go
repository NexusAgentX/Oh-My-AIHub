package main

import "testing"

func TestTestSiteConfigurationIsExplicit(t *testing.T) {
	for _, test := range []struct {
		name        string
		values      map[string]string
		wantEnabled bool
		wantError   bool
	}{
		{name: "production", values: map[string]string{}},
		{name: "disabled", values: map[string]string{"TEST_SITE": "false"}},
		{name: "enabled", values: map[string]string{"TEST_SITE": "true", "TEST_ADMIN_USERNAME": "testadmin", "TEST_ADMIN_PASSWORD": "Synthetic-password-2026"}, wantEnabled: true},
		{name: "missing password", values: map[string]string{"TEST_SITE": "true", "TEST_ADMIN_USERNAME": "testadmin"}, wantError: true},
		{name: "missing username", values: map[string]string{"TEST_SITE": "true", "TEST_ADMIN_PASSWORD": "Synthetic-password-2026"}, wantError: true},
		{name: "credentials without mode", values: map[string]string{"TEST_ADMIN_PASSWORD": "Synthetic-password-2026"}, wantError: true},
		{name: "invalid boolean", values: map[string]string{"TEST_SITE": "invalid"}, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, err := parseTestSite(func(key string) string { return test.values[key] })
			if (err != nil) != test.wantError || (!test.wantError && config.enabled != test.wantEnabled) {
				t.Fatalf("enabled=%v, error=%v", config.enabled, err)
			}
		})
	}
}
