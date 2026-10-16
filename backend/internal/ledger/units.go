package ledger

// Unit prices are per named unit in the model currency; usage is in nano-units.
// No price means unpriced, while an explicit zero means free.
func ValidUnit(key string) bool {
	switch key {
	case "web_search", "file_search", "google_search_query", "google_search_prompt", "google_maps_query", "google_maps_prompt", "container_1gb", "container_4gb", "container_16gb", "container_64gb", "code_execution_second", "storage_gb_hour":
		return true
	}
	return false
}
