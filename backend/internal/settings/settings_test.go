package settings

import (
	"errors"
	"testing"
)

func defaults() Settings {
	return Settings{
		FeeRateNano: 1_000_000, C2CPaymentTimeoutMinutes: 30, DefaultMaxAttempts: 3,
		DefaultTTFTTimeoutMS: 30000, DefaultTotalTimeoutMS: 600000, DefaultCooldownFailures: 3, DefaultCooldownSeconds: 300,
	}
}

func TestValidateAcceptsSeedDefaults(t *testing.T) {
	if err := Validate(defaults()); err != nil {
		t.Fatalf("defaults rejected: %v", err)
	}
}

func TestValidateRejectsOutOfRangeValues(t *testing.T) {
	cases := map[string]func(*Settings){
		"fee above 100%":       func(s *Settings) { s.FeeRateNano = 1_000_000_001 },
		"negative fee":         func(s *Settings) { s.FeeRateNano = -1 },
		"short payment window": func(s *Settings) { s.C2CPaymentTimeoutMinutes = 4 },
		"negative credit":      func(s *Settings) { s.DefaultCreditLimit = -1 },
		"zero attempts":        func(s *Settings) { s.DefaultMaxAttempts = 0 },
		"total below ttft":     func(s *Settings) { s.DefaultTotalTimeoutMS = 20000 },
		"cooldown too short":   func(s *Settings) { s.DefaultCooldownSeconds = 9 },
		"bad host":             func(s *Settings) { s.ExtraBlockedHosts = []string{"exa mple.com"} },
		"url instead of host":  func(s *Settings) { s.ExtraBlockedHosts = []string{"https://example.com"} },
	}
	for name, mutate := range cases {
		value := defaults()
		mutate(&value)
		if err := Validate(value); !errors.Is(err, ErrInvalidInput) {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func TestNormalizeHostsLowercasesAndDeduplicates(t *testing.T) {
	hosts := normalizeHosts([]string{" API.Example.com. ", "api.example.com", ""})
	if len(hosts) != 1 || hosts[0] != "api.example.com" {
		t.Fatalf("hosts = %v", hosts)
	}
}
