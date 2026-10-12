// Package gatewaybench holds the pieces of the gateway performance baseline
// (Epic #360, G8.0): fake upstream servers that speak the four API formats, a
// load generator that records what a client sees, a TLS test CA, and a query
// counter for the database. docs/runbooks/gateway-baseline.md says how to run
// the baseline and how to read it.
//
// The perfcompare command in this directory turns the results of several runs
// into the comparison tables of the performance workflow
// (.github/workflows/perf-baseline.yml).
//
// Nothing here is imported by the server. The pieces are driven by the build
// tagged test in internal/postgres (gateway_perf_baseline_test.go), which runs
// the gateway in process against a PostgreSQL database; the test, not this
// package, wires the gateway to the fake upstreams, and it does so through the
// seams the outbound policy already has for its own tests. Nothing in this
// package or in the production code relaxes the egress rules.
package gatewaybench
