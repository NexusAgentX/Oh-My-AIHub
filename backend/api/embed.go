// Package apicontract exposes the canonical API document to offline contract consumers.
package apicontract

import _ "embed"

//go:embed openapi.yaml
var document string

// Document returns the immutable source, including discovery-stage extensions.
func Document() string { return document }
