// Command perfcompare turns the output of the gateway performance baseline
// (docs/runbooks/gateway-baseline.md) into Markdown tables for the job summary
// of the performance workflow (.github/workflows/perf-baseline.yml). It reads
// results.json files only, so it also reads the output of older baseline tools.
//
//	perfcompare <job-dir>             base against head: median [min – max] over the rounds
//	perfcompare <job-dir> <job-dir>…  the same measurement made by several jobs, side by side
//
// A job directory is what scripts/perf-ab.sh writes:
//
//	cpu.txt                      optional: the CPU model of the machine, one line
//	<side>/r<round>/<phase>/     side is base or head; one run of the baseline tool,
//	                             holding results.json, report.md and meta.txt
//
// The phase "default" holds the scenarios at their own rates, "qps<N>" the same
// scenarios at N requests per second, "sweep" the sweeps for the highest
// sustainable rate.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: perfcompare <job-dir>...")
		os.Exit(2)
	}
	var err error
	if len(os.Args) == 2 {
		err = compareJob(os.Stdout, os.Args[1])
	} else {
		err = compareJobs(os.Stdout, os.Args[1:])
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "perfcompare:", err)
		os.Exit(1)
	}
}

// ---- what the baseline tool writes (results.json) ----

type runData struct {
	Summary struct {
		Failures     map[string]int64   `json:"failures"`
		ThroughputPS float64            `json:"throughput_per_second"`
		TTFTMS       map[string]float64 `json:"ttft_ms"`
	} `json:"summary"`
	CPUPerRequestNS float64 `json:"cpu_per_request_ns"`
	AllocBytes      float64 `json:"alloc_bytes_per_request"`
	Mallocs         float64 `json:"mallocs_per_request"`
	// StatementMS is the mean duration of each statement, as the client saw it.
	StatementMS map[string]float64 `json:"statement_mean_ms"`
	// LockHold is how long the settlement kept the ledger row locks taken;
	// tools older than G8.7 do not write it (Holds is 0).
	LockHold struct {
		Holds      int64   `json:"holds"`
		MeanMS     float64 `json:"mean_ms"`
		P99MS      float64 `json:"p99_ms"`
		Statements float64 `json:"statements_per_hold"`
	} `json:"ledger_lock_hold"`
}

type scenarioResult struct {
	Name         string  `json:"name"`
	Direct       runData `json:"direct"`
	Gateway      runData `json:"gateway"`
	QueriesTotal float64 `json:"queries_per_request_total"`
}

type results struct {
	Scenarios []scenarioResult `json:"scenarios"`
	Sweeps    []struct {
		Scenario string  `json:"scenario"`
		Best     float64 `json:"best_sustainable_qps"`
	} `json:"sweeps"`
}

// ---- the metrics of a scenario ----

type metric struct {
	name   string
	format string
	// value is nil for the metrics that do not come from a scenario's runs;
	// ok is false when the tool that wrote the results did not measure it.
	value func(scenarioResult) (value float64, ok bool)
}

func always(f func(scenarioResult) float64) func(scenarioResult) (float64, bool) {
	return func(s scenarioResult) (float64, bool) { return f(s), true }
}

func addedTTFT(percentile string) func(scenarioResult) (float64, bool) {
	return always(func(s scenarioResult) float64 {
		return s.Gateway.Summary.TTFTMS[percentile] - s.Direct.Summary.TTFTMS[percentile]
	})
}

func lockHold(f func(scenarioResult) float64) func(scenarioResult) (float64, bool) {
	return func(s scenarioResult) (float64, bool) { return f(s), s.Gateway.LockHold.Holds > 0 }
}

func failures(r runData) float64 {
	var total int64
	for _, n := range r.Summary.Failures {
		total += n
	}
	return float64(total)
}

var metrics = []metric{
	{"added ttft p50 (ms)", "%.2f", addedTTFT("p50")},
	{"added ttft p99 (ms)", "%.1f", addedTTFT("p99")},
	{"gateway throughput (/s)", "%.0f", always(func(s scenarioResult) float64 { return s.Gateway.Summary.ThroughputPS })},
	{"net CPU (ms/req)", "%.2f", always(func(s scenarioResult) float64 { return (s.Gateway.CPUPerRequestNS - s.Direct.CPUPerRequestNS) / 1e6 })},
	{"net alloc (KB/req)", "%.1f", always(func(s scenarioResult) float64 { return (s.Gateway.AllocBytes - s.Direct.AllocBytes) / 1024 })},
	{"net allocs (/req)", "%.0f", always(func(s scenarioResult) float64 { return s.Gateway.Mallocs - s.Direct.Mallocs })},
	{"DB statements (/req)", "%.2f", always(func(s scenarioResult) float64 { return s.QueriesTotal })},
	// How long the settlement keeps the ledger row locks, and how long a settlement
	// waits for them (the LockLedgerAccounts statement, as the client sees it).
	{"lock hold mean (ms)", "%.2f", lockHold(func(s scenarioResult) float64 { return s.Gateway.LockHold.MeanMS })},
	{"lock hold p99 (ms)", "%.1f", lockHold(func(s scenarioResult) float64 { return s.Gateway.LockHold.P99MS })},
	{"statements per lock hold", "%.1f", lockHold(func(s scenarioResult) float64 { return s.Gateway.LockHold.Statements })},
	{"lock queue: LockLedgerAccounts mean (ms)", "%.2f", func(s scenarioResult) (float64, bool) {
		v, ok := s.Gateway.StatementMS["LockLedgerAccounts"]
		return v, ok
	}},
	// The upstream answered straight, without the gateway: how noisy the machine was.
	{"direct ttft p99 (ms)", "%.1f", always(func(s scenarioResult) float64 { return s.Direct.Summary.TTFTMS["p99"] })},
	{"gateway failed", "%.0f", always(func(s scenarioResult) float64 { return failures(s.Gateway) })},
}

// sweepMetric is the one metric of a sweep.
var sweepMetric = metric{name: "max sustainable rate (/s)", format: "%.0f"}

func metricAt(i int) metric {
	if i < len(metrics) {
		return metrics[i]
	}
	return sweepMetric
}

// ---- passes: one run of the baseline tool ----

type pass struct {
	side    string
	round   int
	phase   string
	results *results // nil when results.json is missing or unreadable
	report  string
	meta    map[string]string
}

func (p pass) started() int64 {
	n, _ := strconv.ParseInt(p.meta["started"], 10, 64)
	return n
}

// phaseRank orders the phases the way the tables list them.
func phaseRank(phase string) int {
	switch {
	case phase == "sweep":
		return 2
	case strings.HasPrefix(phase, "qps"):
		return 1
	}
	return 0
}

// scenarioLabel names a scenario in a table; a phase with a rate of its own is part of the name.
func scenarioLabel(phase, name string) string {
	if rate, ok := strings.CutPrefix(phase, "qps"); ok && rate != "" {
		return name + " @" + rate + "/s"
	}
	return name
}

func readMeta(path string) map[string]string {
	meta := map[string]string{}
	data, err := os.ReadFile(path)
	if err != nil {
		return meta
	}
	for line := range strings.SplitSeq(string(data), "\n") {
		if key, value, ok := strings.Cut(line, "="); ok {
			meta[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
	}
	return meta
}

func subdirs(dir string) []string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var names []string
	for _, entry := range entries {
		if entry.IsDir() {
			names = append(names, entry.Name())
		}
	}
	return names
}

var roundDir = regexp.MustCompile(`^r([0-9]+)$`)

// loadPasses reads every <side>/r<round>/<phase> directory of a job directory.
func loadPasses(dir string) ([]pass, error) {
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dir)
	}
	var passes []pass
	for _, side := range []string{"base", "head"} {
		for _, roundName := range subdirs(filepath.Join(dir, side)) {
			match := roundDir.FindStringSubmatch(roundName)
			if match == nil {
				continue
			}
			round, _ := strconv.Atoi(match[1])
			for _, phase := range subdirs(filepath.Join(dir, side, roundName)) {
				where := filepath.Join(dir, side, roundName, phase)
				p := pass{side: side, round: round, phase: phase, meta: readMeta(filepath.Join(where, "meta.txt"))}
				if data, err := os.ReadFile(filepath.Join(where, "results.json")); err == nil {
					var r results
					if json.Unmarshal(data, &r) == nil {
						p.results = &r
					}
				}
				if data, err := os.ReadFile(filepath.Join(where, "report.md")); err == nil {
					p.report = string(data)
				}
				passes = append(passes, p)
			}
		}
	}
	return passes, nil
}

// ---- samples ----

type rowKey struct {
	scenario string
	metric   int
}

// samples collects the values of every row, per side.
type samples struct {
	scenarios []string // in order of first appearance
	values    map[rowKey]map[string][]float64
	rounds    int
}

func (s *samples) add(side, scenario string, metricIndex int, value float64) {
	if s.values == nil {
		s.values = map[rowKey]map[string][]float64{}
	}
	if !contains(s.scenarios, scenario) {
		s.scenarios = append(s.scenarios, scenario)
	}
	key := rowKey{scenario, metricIndex}
	if s.values[key] == nil {
		s.values[key] = map[string][]float64{}
	}
	s.values[key][side] = append(s.values[key][side], value)
}

func contains(list []string, value string) bool {
	for _, item := range list {
		if item == value {
			return true
		}
	}
	return false
}

func collect(passes []pass) *samples {
	ordered := append([]pass(nil), passes...)
	sort.SliceStable(ordered, func(i, j int) bool {
		a, b := ordered[i], ordered[j]
		if phaseRank(a.phase) != phaseRank(b.phase) {
			return phaseRank(a.phase) < phaseRank(b.phase)
		}
		if a.phase != b.phase {
			return a.phase < b.phase
		}
		if a.round != b.round {
			return a.round < b.round
		}
		return a.side < b.side
	})
	s := &samples{}
	for _, p := range ordered {
		if p.results == nil {
			continue
		}
		s.rounds = max(s.rounds, p.round)
		for _, scenario := range p.results.Scenarios {
			label := scenarioLabel(p.phase, scenario.Name)
			for i, m := range metrics {
				if value, ok := m.value(scenario); ok {
					s.add(p.side, label, i, value)
				}
			}
		}
		for _, sweep := range p.results.Sweeps {
			s.add(p.side, "sweep "+sweep.Scenario, len(metrics), sweep.Best)
		}
	}
	return s
}

// rows lists the rows to print: scenarios in order, metrics in order. A row of
// failures is left out when nothing failed anywhere.
func (s *samples) rows() []rowKey {
	var out []rowKey
	for _, scenario := range s.scenarios {
		for i := 0; i <= len(metrics); i++ {
			key := rowKey{scenario, i}
			sides, ok := s.values[key]
			if !ok {
				continue
			}
			if metricAt(i).name == "gateway failed" && allZero(sides["base"]) && allZero(sides["head"]) {
				continue
			}
			out = append(out, key)
		}
	}
	return out
}

func allZero(values []float64) bool {
	for _, v := range values {
		if v != 0 {
			return false
		}
	}
	return true
}

// ---- statistics ----

type stats struct {
	n                int
	median, min, max float64
	hasValues        bool
}

func summarize(values []float64) stats {
	if len(values) == 0 {
		return stats{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	median := sorted[len(sorted)/2]
	if len(sorted)%2 == 0 {
		median = (sorted[len(sorted)/2-1] + sorted[len(sorted)/2]) / 2
	}
	return stats{n: len(sorted), median: median, min: sorted[0], max: sorted[len(sorted)-1], hasValues: true}
}

// cell is the median with its range, or the value alone when there is one.
func (s stats) cell(format string) string {
	switch {
	case !s.hasValues:
		return "–"
	case s.n == 1:
		return fmt.Sprintf(format, s.median)
	}
	return fmt.Sprintf(format+" ["+format+"–"+format+"]", s.median, s.min, s.max)
}

func (s stats) value(format string) string {
	if !s.hasValues {
		return "–"
	}
	return fmt.Sprintf(format, s.median)
}

// relative is the change from base to head as a percentage of base.
func relative(base, head stats) string {
	if !base.hasValues || !head.hasValues {
		return "–"
	}
	switch {
	case base.median == 0 && head.median == 0:
		return "0%"
	case base.median == 0:
		return "n/a"
	}
	percent := (head.median - base.median) / math.Abs(base.median) * 100
	if math.Abs(percent) < 0.05 {
		return "0.0%"
	}
	return fmt.Sprintf("%+.1f%%", percent)
}

// ranges says whether the spread of the two sides overlaps: when it does, the
// change may be noise. It needs at least two runs on each side.
func ranges(base, head stats) string {
	if base.n < 2 || head.n < 2 {
		return "–"
	}
	if math.Max(base.min, head.min) <= math.Min(base.max, head.max) {
		return "overlap"
	}
	return "apart"
}

// ---- one job: base against head ----

func compareJob(w io.Writer, dir string) error {
	passes, err := loadPasses(dir)
	if err != nil {
		return err
	}
	s := collect(passes)
	rows := s.rows()
	if len(rows) == 0 {
		fmt.Fprintf(w, "No results found in %s: no run of the baseline tool left a readable results.json.\n\n", dir)
		writePasses(w, passes)
		return nil
	}
	fmt.Fprintf(w, "### Base against head\n\nMedian [min–max] over %d round(s); every round measures each scenario once on each side, and the side that goes first alternates. ", s.rounds)
	fmt.Fprint(w, "Added = through the gateway minus straight at the fake upstream; net = gateway run minus direct run. “ranges” says whether the two sides' [min–max] overlap: when they do, a change is within the noise of this machine.\n\n")
	fmt.Fprint(w, "| scenario | metric | base | head | change | ranges | n (base/head) |\n|---|---|---|---|---|---|---|\n")
	last := ""
	for _, key := range rows {
		m := metricAt(key.metric)
		base, head := summarize(s.values[key]["base"]), summarize(s.values[key]["head"])
		name := ""
		if key.scenario != last {
			name = "**" + key.scenario + "**"
			last = key.scenario
		}
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s | %d/%d |\n", name, m.name, base.cell(m.format), head.cell(m.format), relative(base, head), ranges(base, head), base.n, head.n)
	}
	fmt.Fprint(w, "\n")
	writePasses(w, passes)
	return nil
}

var (
	probeBefore = regexp.MustCompile(`before the runs: round trip mean (\S+) p99 \S+, commit mean (\S+) p99`)
	probeAfter  = regexp.MustCompile(`and after them: round trip mean (\S+) p99 \S+, commit mean (\S+) p99`)
	loadStart   = regexp.MustCompile(`load average at the start (\S+)`)
)

func field(re *regexp.Regexp, text string, group int) string {
	if match := re.FindStringSubmatch(text); match != nil {
		return match[group]
	}
	return "?"
}

// writePasses lists every run of the baseline tool in the order they ran, with
// what says whether the machine and its database were steady: the database's
// round trip and commit latency at the start and the end, the load average, and
// how much of the CPU time the virtual machine lost to its host (steal).
func writePasses(w io.Writer, passes []pass) {
	if len(passes) == 0 {
		return
	}
	ordered := append([]pass(nil), passes...)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].started() < ordered[j].started() })
	fmt.Fprint(w, "### Runs, in the order they ran\n\n")
	fmt.Fprint(w, "| # | side | round | phase | exit | DB round trip (start → end) | DB commit (start → end) | load avg at start | CPU busy % | CPU steal % |\n|---|---|---|---|---|---|---|---|---|---|\n")
	for i, p := range ordered {
		exit := p.meta["exit"]
		if exit == "" {
			exit = "?"
		}
		if p.results == nil {
			exit += " (no results)"
		}
		fmt.Fprintf(w, "| %d | %s | %d | %s | %s | %s → %s | %s → %s | %s | %s | %s |\n", i+1, p.side, p.round, p.phase, exit,
			field(probeBefore, p.report, 1), field(probeAfter, p.report, 1), field(probeBefore, p.report, 2), field(probeAfter, p.report, 2),
			field(loadStart, p.report, 1), orDash(p.meta["cpu_busy_percent"]), orDash(p.meta["cpu_steal_percent"]))
	}
	fmt.Fprint(w, "\n")
}

func orDash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

// ---- several jobs: the same measurement made on different machines ----

type jobData struct {
	label   string
	cpu     string
	passes  []pass
	samples *samples
}

func compareJobs(w io.Writer, dirs []string) error {
	var jobs []jobData
	for _, dir := range dirs {
		passes, err := loadPasses(dir)
		if err != nil {
			return err
		}
		cpu := "unknown"
		if data, err := os.ReadFile(filepath.Join(dir, "cpu.txt")); err == nil {
			cpu = strings.TrimSpace(string(data))
		}
		label := strings.TrimPrefix(filepath.Base(filepath.Clean(dir)), "perf-baseline-")
		jobs = append(jobs, jobData{label: label, cpu: cpu, passes: passes, samples: collect(passes)})
	}

	fmt.Fprint(w, "### Jobs\n\n| job | CPU | runs (ok / all) | CPU steal % (mean over runs) |\n|---|---|---|---|\n")
	for _, job := range jobs {
		ok, steal, counted := 0, 0.0, 0
		for _, p := range job.passes {
			if p.results != nil && p.meta["exit"] == "0" {
				ok++
			}
			if v, err := strconv.ParseFloat(p.meta["cpu_steal_percent"], 64); err == nil {
				steal += v
				counted++
			}
		}
		stealText := "–"
		if counted > 0 {
			stealText = fmt.Sprintf("%.2f", steal/float64(counted))
		}
		fmt.Fprintf(w, "| %s | %s | %d / %d | %s |\n", job.label, job.cpu, ok, len(job.passes), stealText)
	}

	// The union of the scenarios, in the order the jobs list them.
	merged := &samples{}
	for _, job := range jobs {
		for _, scenario := range job.samples.scenarios {
			if !contains(merged.scenarios, scenario) {
				merged.scenarios = append(merged.scenarios, scenario)
			}
		}
	}
	fmt.Fprint(w, "\n### Spread between jobs\n\nEach job's median over its rounds, as “base / head (change)”. Spread = (highest − lowest median across the jobs) ÷ their median, per side: how much the same code measures differently on different machines.\n\n")
	fmt.Fprint(w, "| scenario | metric |")
	for _, job := range jobs {
		fmt.Fprintf(w, " %s |", job.label)
	}
	fmt.Fprint(w, " spread base | spread head |\n|---|---|")
	for range jobs {
		fmt.Fprint(w, "---|")
	}
	fmt.Fprint(w, "---|---|\n")
	last := ""
	for _, scenario := range merged.scenarios {
		for i := 0; i <= len(metrics); i++ {
			key := rowKey{scenario, i}
			present, failed := false, false
			for _, job := range jobs {
				if sides, ok := job.samples.values[key]; ok {
					present = true
					failed = failed || !allZero(sides["base"]) || !allZero(sides["head"])
				}
			}
			if !present || (metricAt(i).name == "gateway failed" && !failed) {
				continue
			}
			m := metricAt(i)
			name := ""
			if scenario != last {
				name = "**" + scenario + "**"
				last = scenario
			}
			fmt.Fprintf(w, "| %s | %s |", name, m.name)
			var baseMedians, headMedians []float64
			for _, job := range jobs {
				base, head := summarize(job.samples.values[key]["base"]), summarize(job.samples.values[key]["head"])
				if base.hasValues {
					baseMedians = append(baseMedians, base.median)
				}
				if head.hasValues {
					headMedians = append(headMedians, head.median)
				}
				fmt.Fprintf(w, " %s / %s (%s) |", base.value(m.format), head.value(m.format), relative(base, head))
			}
			fmt.Fprintf(w, " %s | %s |\n", spread(baseMedians), spread(headMedians))
		}
	}
	fmt.Fprint(w, "\n")
	return nil
}

// spread is the distance between the highest and the lowest value as a
// percentage of their median.
func spread(values []float64) string {
	if len(values) < 2 {
		return "–"
	}
	s := summarize(values)
	if s.median == 0 {
		if s.max == s.min {
			return "0%"
		}
		return "n/a"
	}
	return fmt.Sprintf("%.0f%%", (s.max-s.min)/math.Abs(s.median)*100)
}
