package main

import (
	"fmt"
	"strconv"
	"strings"
)

type testSiteConfig struct {
	enabled  bool
	username string
	password string
}

func parseTestSite(getenv func(string) string) (testSiteConfig, error) {
	config := testSiteConfig{username: strings.TrimSpace(getenv("TEST_ADMIN_USERNAME")), password: getenv("TEST_ADMIN_PASSWORD")}
	if value := getenv("TEST_SITE"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return config, fmt.Errorf("TEST_SITE must be a boolean")
		}
		config.enabled = enabled
	}
	if config.enabled && (config.username == "" || config.password == "") {
		return config, fmt.Errorf("TEST_SITE requires TEST_ADMIN_USERNAME and TEST_ADMIN_PASSWORD")
	}
	if !config.enabled && (config.username != "" || config.password != "") {
		return config, fmt.Errorf("test administrator credentials require TEST_SITE=true")
	}
	return config, nil
}
