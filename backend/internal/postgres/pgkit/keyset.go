package pgkit

// After turns the ID of the last row of the previous page into the nullable
// argument of a keyset query (`sqlc.narg(after_id)::uuid IS NULL OR id > ...`):
// an empty ID starts from the first row.
func After(id string) *string {
	if id == "" {
		return nil
	}
	return &id
}
