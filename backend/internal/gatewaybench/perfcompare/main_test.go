package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run builds the JSON of one run of the baseline tool.
func run(throughput, p50, p99, cpuMS, allocKB, mallocs float64, failed int64) map[string]any {
	return map[string]any{
		"summary": map[string]any{
			"failures":              map[string]int64{"http 500": failed},
			"throughput_per_second": throughput,
			"ttft_ms":               map[string]float64{"p50": p50, "p99": p99},
		},
		"cpu_per_request_ns":      cpuMS * 1e6,
		"alloc_bytes_per_request": allocKB * 1024,
		"mallocs_per_request":     mallocs,
	}
}

// scenario is one entry of results.json: the gateway run adds addP50/addP99 over the direct run.
func scenario(name string, directP50, addP50, directP99, addP99, netCPU float64, failed int64) map[string]any {
	return map[string]any{
		"name":                      name,
		"direct":                    run(50, directP50, directP99, 1, 100, 1000, 0),
		"gateway":                   run(49.5, directP50+addP50, directP99+addP99, 1+netCPU, 100+55, 1000+700, failed),
		"queries_per_request_total": 14,
	}
}

type fixturePass struct {
	side, phase string
	round       int
	results     map[string]any
	report      string
	meta        string
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func jobDir(t *testing.T, cpu string, passes ...fixturePass) string {
	t.Helper()
	dir := t.TempDir()
	if cpu != "" {
		write(t, filepath.Join(dir, "cpu.txt"), cpu+"\n")
	}
	for _, p := range passes {
		where := filepath.Join(dir, p.side, fmt.Sprintf("r%d", p.round), p.phase)
		if p.results != nil {
			data, err := json.Marshal(p.results)
			if err != nil {
				t.Fatal(err)
			}
			write(t, filepath.Join(where, "results.json"), string(data))
		} else {
			if err := os.MkdirAll(where, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if p.report != "" {
			write(t, filepath.Join(where, "report.md"), p.report)
		}
		if p.meta != "" {
			write(t, filepath.Join(where, "meta.txt"), p.meta)
		}
	}
	return dir
}

func results1(scenarios ...map[string]any) map[string]any {
	return map[string]any{"scenarios": scenarios}
}

const sampleReport = `- Go go1.27.0 linux/amd64, GOMAXPROCS 4, unknown
- database as this process sees it, before the runs: round trip mean 60µs p99 150µs, commit mean 1.2ms p99 3ms
- and after them: round trip mean 70µs p99 200µs, commit mean 1.4ms p99 4ms
- load average at the start 0.52 0.30 0.20, at the end 1.00 0.60 0.30
`

func TestSummarize(t *testing.T) {
	cases := []struct {
		name   string
		values []float64
		median float64
		cell   string
	}{
		{"one value", []float64{4}, 4, "4.0"},
		{"odd", []float64{3, 1, 2}, 2, "2.0 [1.0–3.0]"},
		{"even takes the middle pair", []float64{4, 1, 2, 3}, 2.5, "2.5 [1.0–4.0]"},
	}
	for _, c := range cases {
		s := summarize(c.values)
		if s.median != c.median || s.cell("%.1f") != c.cell {
			t.Errorf("%s: median %v cell %q, want %v %q", c.name, s.median, s.cell("%.1f"), c.median, c.cell)
		}
	}
	if got := summarize(nil).cell("%.1f"); got != "–" {
		t.Errorf("no values: %q", got)
	}
}

func TestRelativeAndRanges(t *testing.T) {
	if got := relative(summarize([]float64{10}), summarize([]float64{8})); got != "-20.0%" {
		t.Errorf("relative = %q", got)
	}
	if got := relative(summarize([]float64{0}), summarize([]float64{0})); got != "0%" {
		t.Errorf("zero to zero = %q", got)
	}
	if got := relative(summarize([]float64{0}), summarize([]float64{1})); got != "n/a" {
		t.Errorf("zero to one = %q", got)
	}
	if got := ranges(summarize([]float64{1, 3}), summarize([]float64{2, 5})); got != "overlap" {
		t.Errorf("overlapping = %q", got)
	}
	if got := ranges(summarize([]float64{1, 2}), summarize([]float64{3, 5})); got != "apart" {
		t.Errorf("apart = %q", got)
	}
	if got := ranges(summarize([]float64{1}), summarize([]float64{3, 5})); got != "–" {
		t.Errorf("one run on a side = %q", got)
	}
}

func TestCompareJob(t *testing.T) {
	var passes []fixturePass
	// Three rounds: base added p50 is 10, 11, 12 ms; head 5, 5.5, 6 ms. The default
	// phase runs one scenario, the qps200 phase the same scenario at 200/s.
	for round := 1; round <= 3; round++ {
		r := float64(round)
		passes = append(passes,
			fixturePass{side: "base", round: round, phase: "default", results: results1(scenario("nonstream-small", 50, 9+r, 55, 17+r, 1.2, 0)), report: sampleReport, meta: fmt.Sprintf("started=%d\nexit=0\ncpu_busy_percent=61.5\ncpu_steal_percent=0.25\n", 100+round)},
			fixturePass{side: "head", round: round, phase: "default", results: results1(scenario("nonstream-small", 50, 4.5+r/2, 55, 9+r/2, 0.9, 0)), meta: fmt.Sprintf("started=%d\nexit=0\n", 200+round)},
			fixturePass{side: "base", round: round, phase: "qps200", results: results1(scenario("nonstream-small", 50, 2, 55, 12, 1.1, 0))},
			fixturePass{side: "head", round: round, phase: "qps200", results: results1(scenario("nonstream-small", 50, 1, 55, 4, 1.0, 0))},
		)
	}
	passes = append(passes,
		fixturePass{side: "base", round: 1, phase: "sweep", results: map[string]any{"sweeps": []map[string]any{{"scenario": "nonstream-small", "best_sustainable_qps": 285}}}},
		fixturePass{side: "base", round: 2, phase: "sweep", results: map[string]any{"sweeps": []map[string]any{{"scenario": "nonstream-small", "best_sustainable_qps": 500}}}},
		fixturePass{side: "head", round: 1, phase: "sweep", results: map[string]any{"sweeps": []map[string]any{{"scenario": "nonstream-small", "best_sustainable_qps": 350}}}},
		// A pass that produced nothing is listed, not fatal.
		fixturePass{side: "head", round: 2, phase: "sweep", meta: "started=999\nexit=1\n"},
	)
	dir := jobDir(t, "", passes...)

	var out strings.Builder
	if err := compareJob(&out, dir); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"over 3 round(s)",
		// base 10/11/12 ms against head 5/5.5/6 ms: median 11 and 5.5.
		"| **nonstream-small** | added ttft p50 (ms) | 11.00 [10.00–12.00] | 5.50 [5.00–6.00] | -50.0% | apart | 3/3 |",
		// the qps200 phase is its own scenario.
		"| **nonstream-small @200/s** | added ttft p50 (ms) | 2.00 [2.00–2.00] | 1.00 [1.00–1.00] | -50.0% | apart | 3/3 |",
		"| DB statements (/req) | 14.00 [14.00–14.00] | 14.00 [14.00–14.00] | 0.0% | overlap | 3/3 |",
		// sweeps: the median of 285 and 500 is 392.5.
		"| **sweep nonstream-small** | max sustainable rate (/s) | 392 [285–500] | 350 | -10.8% | – | 2/1 |",
		// the run list: the database probe, the load average and the CPU time stolen by the host.
		"| base | 1 | default | 0 | 60µs → 70µs | 1.2ms → 1.4ms | 0.52 | 61.5 | 0.25 |",
		"| head | 2 | sweep | 1 (no results) |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q\n%s", want, got)
		}
	}
	if strings.Contains(got, "gateway failed") {
		t.Errorf("a row of failures appears although nothing failed:\n%s", got)
	}
}

func TestCompareJobShowsFailures(t *testing.T) {
	dir := jobDir(t, "",
		fixturePass{side: "base", round: 1, phase: "default", results: results1(scenario("nonstream-small", 50, 5, 55, 10, 1, 0))},
		fixturePass{side: "head", round: 1, phase: "default", results: results1(scenario("nonstream-small", 50, 5, 55, 10, 1, 3))},
	)
	var out strings.Builder
	if err := compareJob(&out, dir); err != nil {
		t.Fatal(err)
	}
	if want := "| gateway failed | 0 | 3 | n/a | – | 1/1 |"; !strings.Contains(out.String(), want) {
		t.Errorf("output lacks %q\n%s", want, out.String())
	}
}

func TestCompareJobWithoutResults(t *testing.T) {
	dir := jobDir(t, "", fixturePass{side: "base", round: 1, phase: "default", meta: "exit=2\n"})
	var out strings.Builder
	if err := compareJob(&out, dir); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "No results found") || !strings.Contains(out.String(), "2 (no results)") {
		t.Errorf("unexpected output:\n%s", out.String())
	}
	if err := compareJob(&out, filepath.Join(dir, "missing")); err == nil {
		t.Error("a missing directory is not an error")
	}
}

func TestCompareJobs(t *testing.T) {
	// Two machines measure the same code; the second is twice as slow on the base side.
	one := jobDir(t, "AMD EPYC 7763, 4 vCPU",
		fixturePass{side: "base", round: 1, phase: "default", results: results1(scenario("nonstream-small", 50, 10, 55, 20, 1, 0)), meta: "exit=0\ncpu_steal_percent=0.10\n"},
		fixturePass{side: "head", round: 1, phase: "default", results: results1(scenario("nonstream-small", 50, 5, 55, 10, 1, 0)), meta: "exit=0\ncpu_steal_percent=0.30\n"},
	)
	two := jobDir(t, "Intel Xeon, 4 vCPU",
		fixturePass{side: "base", round: 1, phase: "default", results: results1(scenario("nonstream-small", 50, 20, 55, 40, 1, 0)), meta: "exit=0\n"},
		fixturePass{side: "head", round: 1, phase: "default", results: results1(scenario("nonstream-small", 50, 5, 55, 10, 1, 0)), meta: "exit=0\n"},
	)
	named := filepath.Join(t.TempDir(), "perf-baseline-job-1")
	if err := os.Rename(one, named); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := compareJobs(&out, []string{named, two}); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"| job-1 | AMD EPYC 7763, 4 vCPU | 2 / 2 | 0.20 |",
		"| " + filepath.Base(two) + " | Intel Xeon, 4 vCPU | 2 / 2 | – |",
		// base 10 and 20 ms: spread (20-10)/15 = 67%; head 5 and 5: 0%.
		"| **nonstream-small** | added ttft p50 (ms) | 10.00 / 5.00 (-50.0%) | 20.00 / 5.00 (-75.0%) | 67% | 0% |",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output lacks %q\n%s", want, got)
		}
	}
}

func TestScenarioLabel(t *testing.T) {
	for phase, want := range map[string]string{"default": "x", "sweep": "x", "qps200": "x @200/s", "qps": "x"} {
		if got := scenarioLabel(phase, "x"); got != want {
			t.Errorf("phase %q: %q, want %q", phase, got, want)
		}
	}
}
