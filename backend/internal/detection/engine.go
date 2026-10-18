package detection

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/channel"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/gatewaycontract"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/ids"
	"github.com/NexusAgentX/Oh-My-AIHub/backend/internal/market"
)

type Service struct {
	rootMu        sync.RWMutex
	mu            sync.Mutex
	registry      *Registry
	store         Store
	resolver      Resolver
	outbound      channel.Outbound
	prepareEgress func(context.Context) (channel.Outbound, error)
	limits        Limits
	root          []byte
	ctx           context.Context
	cancel        context.CancelFunc
	queue         chan string
	live          map[string]*liveJob
	owners        map[string]int
	wg            sync.WaitGroup
	closed        bool
	settings      SearchStore
	source        string
}
type liveJob struct {
	ownerID string
	actor   Actor
	secret  []byte
	cancel  context.CancelFunc
}
type Config struct {
	Registry       *Registry
	Store          Store
	Resolver       Resolver
	Outbound       channel.Outbound
	PrepareEgress  func(context.Context) (channel.Outbound, error)
	Limits         Limits
	CredentialRoot []byte
	Synthetic      bool
	Settings       SearchStore
}

func New(ctx context.Context, c Config) (*Service, error) {
	if c.Registry == nil || c.Store == nil || c.Resolver == nil || c.Outbound == nil || c.Limits.Validate() != nil || len(c.CredentialRoot) != 32 {
		return nil, ErrInvalid
	}
	startupCtx, cancelStartup := context.WithTimeout(ctx, 10*time.Second)
	defer cancelStartup()
	if err := c.Store.Interrupt(startupCtx); err != nil {
		return nil, err
	}
	workCtx, cancel := context.WithCancel(ctx)
	s := &Service{registry: c.Registry, store: c.Store, resolver: c.Resolver, outbound: c.Outbound, prepareEgress: c.PrepareEgress, limits: c.Limits, root: slices.Clone(c.CredentialRoot), ctx: workCtx, cancel: cancel, queue: make(chan string, c.Limits.Queue), live: map[string]*liveJob{}, owners: map[string]int{}, settings: c.Settings, source: "real_upstream"}
	if c.Synthetic {
		s.source = "synthetic"
	}
	for i := 0; i < c.Limits.Workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	return s, nil
}
func (s *Service) Close() {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return
	}
	s.closed = true
	s.cancel()
	for _, v := range s.live {
		if v.cancel != nil {
			v.cancel()
		} else {
			clear(v.secret)
		}
	}
	s.mu.Unlock()
	s.wg.Wait()
	s.mu.Lock()
	for id, v := range s.live {
		clear(v.secret)
		delete(s.live, id)
	}
	s.rootMu.Lock()
	clear(s.root)
	s.rootMu.Unlock()
	s.mu.Unlock()
}
func (s *Service) scope(target Target, secret string) string {
	s.rootMu.RLock()
	defer s.rootMu.RUnlock()
	groupID := target.GroupID
	if groupID == "" {
		groupID = target.Binding.TargetID
	}
	tag, _ := market.CredentialScope(s.root, groupID, []byte(secret))
	return tag
}

func parametersVersion(p Parameters) string {
	encoded, _ := json.Marshal(p)
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}
func (s *Service) Create(ctx context.Context, actor Actor, target Target, parameters Parameters, secret string) (Job, error) {
	return s.create(ctx, actor, target, parameters, secret, nil)
}
func (s *Service) create(ctx context.Context, actor Actor, target Target, parameters Parameters, secret string, continued *Job) (Job, error) {
	if actor.ID == "" || len(secret) == 0 || len(secret) > 8192 || strings.ContainsAny(secret, "\r\n\x00") || parameters.Context.Validate() != nil || parameters.MaxOutput.Validate() != nil {
		return Job{}, ErrInvalid
	}
	resolved, err := s.resolver.Resolve(ctx, actor, target)
	if err != nil {
		return Job{}, err
	}
	target = resolved.Target
	if target.Binding.TargetKind != "group" && target.Binding.TargetKind != "group_model" {
		return Job{}, ErrInvalid
	}
	plan, err := s.registry.Plan(target)
	if err != nil {
		return Job{}, err
	}
	target.Binding.ManifestVersion = plan.ManifestVersion
	target.Binding.CodeVersion = plan.RegistryVersion
	target.Binding.ParameterVersion = parametersVersion(parameters)
	target.Binding.CredentialScope = s.scope(target, secret)
	id, err := ids.NewUUID()
	if err != nil {
		return Job{}, err
	}
	now := time.Now().UTC()
	job := Job{ID: id, OwnerID: actor.ID, RequestedByID: actor.ID, Target: target, Parameters: parameters, Plan: plan, State: "queued", ExecutionActive: true, PendingCleanup: []Artifact{}, Results: []Result{}, CreatedAt: now, UpdatedAt: now}

	if continued != nil {
		prior := target.Binding
		prior.CredentialScope = continued.Target.Binding.CredentialScope
		if !market.CompatibleBinding(continued.Target.Binding, prior) {
			return Job{}, ErrConflict
		}
		job.OwnerID = continued.OwnerID
		from := continued.ID
		job.ContinuedFromID = &from
		byID := map[string]Planned{}
		for _, item := range plan.Items {
			byID[item.ID] = item
		}
		if continued.ReportID != nil {
			for _, result := range continued.Results {
				item, exists := byID[result.ScenarioID]
				if exists && result.Source == s.source && !item.CredentialDependent && terminal(result.State) && result.State != "inconclusive" && (CompatibleProfile(continued.Target.Profile, target.Profile) || !slices.Contains(profileScenarios(plan), item.ID)) {
					if result.SourceTaskID == "" {
						result.SourceTaskID = continued.ID
					}
					if result.SourceReportID == nil {
						result.SourceReportID = continued.ReportID
					}
					job.Results = append(job.Results, result)
				}
			}
		}
		job.Results = resumeResults(job)
	}
	if HasPending(plan) {
		job.State = "suite_pending"
		job.ExecutionActive = false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if job.State == "queued" && (s.owners[job.OwnerID] >= s.limits.PerOwner || len(s.queue) >= cap(s.queue)) {
		return Job{}, ErrBusy
	}
	if err = s.store.Create(ctx, job); err != nil {
		return Job{}, err
	}
	if job.State == "queued" {
		s.live[id] = &liveJob{ownerID: job.OwnerID, actor: actor, secret: []byte(secret)}
		s.owners[job.OwnerID]++
		s.queue <- id
	}
	return job, nil
}
func (s *Service) Get(ctx context.Context, actor Actor, id string) (Job, error) {
	job, err := s.store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if actor.ID == "" || (actor.ID != job.OwnerID && !actor.Admin) {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (s *Service) List(ctx context.Context, actor Actor, cursor *Cursor, limit int) ([]Job, error) {
	if actor.ID == "" || limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return s.store.List(ctx, actor.ID, actor.Admin, cursor, limit)
}
func (s *Service) Cancel(ctx context.Context, actor Actor, id string) (Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	job, err := s.Get(ctx, actor, id)
	if err != nil {
		return Job{}, err
	}
	if job.State != "queued" && job.State != "running" {
		return job, nil
	}
	if active := s.live[id]; active != nil && active.cancel != nil {
		active.cancel()
	} else if active != nil {
		clear(active.secret)
		job.ExecutionActive = false
		delete(s.live, id)
		s.owners[active.ownerID]--
		// A cancelled queued task releases admission immediately. Producers
		// hold mu; concurrent workers can only remove entries while we compact.
		pending := make([]string, 0, len(s.queue))
		for count := len(s.queue); count > 0; count-- {
			select {
			case queued := <-s.queue:
				if queued != id {
					pending = append(pending, queued)
				}
			default:
			}
		}
		for _, queued := range pending {
			s.queue <- queued
		}
	}
	if s.live[id] == nil {
		job.ExecutionActive = false
	}
	job.State = "cancelled"
	job.Complete = false
	job.UpdatedAt = time.Now().UTC()
	return job, s.store.Save(ctx, job)
}
func (s *Service) Resume(ctx context.Context, actor Actor, id, secret string) (Job, error) {
	return s.retry(ctx, actor, id, secret, nil)
}
func (s *Service) Retest(ctx context.Context, actor Actor, id, secret string, selected []string) (Job, error) {
	if len(selected) == 0 || len(selected) > 512 {
		return Job{}, ErrInvalid
	}
	return s.retry(ctx, actor, id, secret, selected)
}
func (s *Service) retry(ctx context.Context, actor Actor, id, secret string, selected []string) (Job, error) {
	job, err := s.Get(ctx, actor, id)
	if err != nil {
		return Job{}, err
	}
	if job.ExecutionActive || (!slices.Contains([]string{"interrupted", "cancelled", "suite_pending", "failed"}, job.State) && !(len(selected) > 0 && job.State == "finished")) || secret == "" || len(secret) > 8192 || strings.ContainsAny(secret, "\r\n\x00") {
		return Job{}, ErrInvalid
	}
	resolved, err := s.resolver.Resolve(ctx, actor, job.Target)
	if err != nil {
		return Job{}, err
	}
	plan, err := s.registry.Plan(resolved.Target)
	if err != nil {
		return Job{}, err
	}
	current := resolved.Target.Binding
	current.ManifestVersion = plan.ManifestVersion
	current.CodeVersion = plan.RegistryVersion
	current.ParameterVersion = parametersVersion(job.Parameters)
	actualScope := s.scope(resolved.Target, secret)
	// Compare engine inputs separately from credential provenance. A new key
	// forks execution, preserves its own binding and reuses independent evidence.
	current.CredentialScope = job.Target.Binding.CredentialScope
	if !market.CompatibleBinding(job.Target.Binding, current) || plan.RegistryVersion != job.Plan.RegistryVersion {
		return Job{}, ErrConflict
	}
	if !CompatibleProfile(job.Target.Profile, resolved.Target.Profile) {
		selected = append(slices.Clone(selected), profileScenarios(plan)...)
	}
	job, err = withoutSelected(job, selected)
	if err != nil {
		return Job{}, err
	}
	if actualScope != job.Target.Binding.CredentialScope {
		return s.create(ctx, actor, resolved.Target, job.Parameters, secret, &job)
	}
	job.Target = resolved.Target
	job.Target.Binding = current
	if HasPending(plan) {
		return job, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return Job{}, ErrClosed
	}
	if s.live[id] != nil {
		return Job{}, ErrBusy
	}
	if s.owners[job.OwnerID] >= s.limits.PerOwner || len(s.queue) >= cap(s.queue) {
		return Job{}, ErrBusy
	}
	job.Results = resumeResults(job)
	job.RequestedByID = actor.ID
	job.State = "queued"
	job.ExecutionActive = true
	job.Complete = false
	job.UpdatedAt = time.Now().UTC()
	if err = s.store.Save(ctx, job); err != nil {
		return Job{}, err
	}
	s.live[id] = &liveJob{ownerID: job.OwnerID, actor: actor, secret: []byte(secret)}
	s.owners[job.OwnerID]++
	s.queue <- id
	return job, nil
}
func withoutSelected(job Job, selected []string) (Job, error) {
	removed, known := map[string]bool{}, map[string]bool{}
	for _, item := range job.Plan.Items {
		known[item.ID] = true
	}
	for _, id := range selected {
		if !known[id] {
			return job, ErrInvalid
		}
		removed[id] = true
	}
	for changed := true; changed; {
		changed = false
		for _, item := range job.Plan.Items {
			if removed[item.ID] {
				continue
			}
			for _, dep := range item.Dependencies {
				if removed[dep] {
					removed[item.ID] = true
					changed = true
					break
				}
			}
		}
	}
	kept := []Result{}
	for _, result := range job.Results {
		if !removed[result.ScenarioID] {
			kept = append(kept, result)
		}
	}
	job.Results = kept
	return job, nil
}

func (s *Service) worker() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case id := <-s.queue:
			s.execute(id)
		}
	}
}
func (s *Service) execute(id string) {
	s.mu.Lock()
	live := s.live[id]
	if live == nil {
		s.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(s.ctx)
	live.cancel = cancel
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		clear(live.secret)
		delete(s.live, id)
		s.owners[live.ownerID]--
		s.mu.Unlock()
	}()
	job, err := s.store.Get(ctx, id)
	if err != nil || job.State != "queued" {
		return
	}
	job.State = "running"
	job.UpdatedAt = time.Now().UTC()
	if s.saveProgress(ctx, job) != nil {
		return
	}
	resolved, err := s.resolver.Resolve(ctx, live.actor, job.Target)
	if err != nil || resolved.Target.Binding.CapabilityDigest != job.Target.Binding.CapabilityDigest || !CompatibleProfile(resolved.Target.Profile, job.Target.Profile) {
		s.finish(ctx, &job, "outdated")
		return
	}
	endpoints := map[string]gatewaycontract.Endpoint{}
	for _, e := range s.registry.manifest.Endpoints {
		endpoints[e.ID] = e
	}
	session := &Session{Target: job.Target, Parameters: job.Parameters, resolved: resolved, secret: live.secret, outbound: s.outbound, prepareEgress: s.prepareEgress, endpoints: endpoints, store: s.store, jobID: job.ID, limits: s.limits, source: s.source}
	defer session.clear()
	initialUsage := job.Usage
	// Cleanup runs before final usage/state persistence, including cancellation.
	// A process crash leaves durable pending IDs for credential-checked resume.
	cleaned := false
	var cleanupErr error
	cleanup := func() {
		if cleaned {
			return
		}
		cleaned = true
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 160*time.Second)
		defer cancel()
		cleanupErr = session.Cleanup(cleanupCtx)
	}
	finish := func(state string) {
		cleanup()
		if cleanupErr != nil && state == "finished" {
			state = "failed"
		}
		job.Usage = initialUsage
		job.Usage.Add(session.usage)
		s.finish(ctx, &job, state)
	}
	defer func() {
		if job.State == "running" {
			finish("interrupted")
		}
	}()
	completed := map[string]Result{}
	for _, result := range job.Results {
		if terminal(result.State) {
			completed[result.ScenarioID] = result
		}
	}
	for len(completed) < len(job.Plan.Items) {
		progressed := false
		for _, item := range job.Plan.Items {
			if _, ok := completed[item.ID]; ok {
				continue
			}
			if ctx.Err() != nil {
				finish("interrupted")
				return
			}
			ready := true
			for _, dependency := range item.Dependencies {
				if _, ok := completed[dependency]; !ok {
					ready = false
				}
			}
			if !ready {
				continue
			}
			progressed = true
			result := s.run(ctx, session, item, completed)
			if ctx.Err() != nil {
				finish("interrupted")
				return
			}
			if !terminal(result.State) {
				finish("failed")
				return
			}
			if err = s.store.RecordResult(ctx, job.ID, result); err != nil {
				finish("failed")
				return
			}
			completed[item.ID] = result
			job.Results = append(job.Results, result)
			job.Usage.Add(result.Usage)
			job.UpdatedAt = time.Now().UTC()
			if err = s.saveProgress(ctx, job); err != nil {
				finish("interrupted")
				return
			}
		}
		if !progressed {
			finish("failed")
			return
		}
	}
	finish("finished")
}
func (s *Service) run(ctx context.Context, session *Session, item Planned, completed map[string]Result) Result {
	start := time.Now().UTC()
	result := Result{ScenarioID: item.ID, Layer: item.Layer, Source: s.source, State: "inconclusive", Reason: "detector_error", Assertions: []Assertion{}, Attempts: []Attempt{}, StartedAt: start}
	witnesses := map[string]Attempt{}
	dependencyFailed := false
	for _, id := range item.Dependencies {
		dep := completed[id]
		dependencyFailed = dependencyFailed || dep.State != "pass"
		for _, a := range dep.Assertions {
			for _, ref := range a.AttemptIDs {
				witnesses[ref] = Attempt{ID: ref}
			}
		}
	}
	if item.Aggregate || dependencyFailed {
		ids := make([]string, 0, len(witnesses))
		for id := range witnesses {
			ids = append(ids, id)
		}
		slices.Sort(ids)
		if len(ids) > 16 {
			ids = ids[:16]
		}
		result.State = "pass"
		result.Reason = "ok"
		if dependencyFailed {
			result.State = "inconclusive"
			result.Reason = "prerequisite_failed"
		}
		for _, id := range item.Assertions {
			result.Assertions = append(result.Assertions, Assertion{ID: id, State: result.State, Reason: result.Reason, AttemptIDs: ids})
		}
	} else {
		session.allowed = map[string]bool{}
		for _, reqID := range item.Requirements {
			for _, suite := range s.registry.manifest.Suites {
				for _, req := range suite.Requirements {
					if req.ID == reqID {
						for _, endpoint := range req.Endpoints {
							session.allowed[endpoint] = true
						}
					}
				}
			}
		}
		before := session.usage
		d := s.registry.definitions[item.ID]
		if d.Run == nil {
			return result
		}
		result = invoke(ctx, d.Run, session)
		result.ScenarioID = item.ID
		result.Layer = item.Layer
		result.Source = s.source
		result.StartedAt = start
		result.Usage = usageDifference(session.usage, before)
	}
	result.FinishedAt = time.Now().UTC()
	result.ID, _ = ids.NewUUID()
	result.SourceTaskID = session.jobID
	result.SourceReportID = nil
	refs := []string{}
	for _, a := range result.Assertions {
		for _, id := range a.AttemptIDs {
			if !slices.Contains(refs, id) {
				refs = append(refs, id)
			}
		}
	}
	for _, capacity := range result.Capacity {
		for _, step := range capacity.Steps {
			for _, id := range step.AttemptIDs {
				if !slices.Contains(refs, id) {
					refs = append(refs, id)
				}
			}
		}
	}
	if len(refs) > 4096 {
		return Result{State: "invalid"}
	}
	attempts, err := s.store.Attempts(ctx, session.jobID, refs)
	if err != nil {
		return Result{State: "invalid"}
	}
	witnesses = map[string]Attempt{}
	for _, a := range attempts {
		witnesses[a.ID] = a
	}
	result.Attempts = attempts
	if ValidateResult(item, result, witnesses) != nil {
		return Result{State: "invalid"}
	}
	return result
}
func invoke(ctx context.Context, run Runner, session *Session) (result Result) {
	defer func() {
		if recover() != nil {
			result = Result{State: "invalid"}
		}
	}()
	return run(ctx, session)
}
func usageDifference(a, b Usage) Usage {
	return Usage{HTTP: a.HTTP - b.HTTP, Generate: a.Generate - b.Generate, Count: a.Count - b.Count, Query: a.Query - b.Query, Cleanup: a.Cleanup - b.Cleanup, InputTokens: a.InputTokens - b.InputTokens, OutputTokens: a.OutputTokens - b.OutputTokens, Images: a.Images - b.Images, Tools: a.Tools - b.Tools}
}
func (s *Service) finish(ctx context.Context, job *Job, state string) {
	saveCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.store.Get(saveCtx, job.ID)
	if err != nil {
		return
	}
	if current.State == "cancelled" {
		job.State = "cancelled"
	} else {
		job.State = state
	}
	job.ExecutionActive = false
	pending, cleanupErr := s.store.Artifacts(saveCtx, job.ID)
	job.PendingCleanup = append([]Artifact{}, pending...)
	if cleanupErr != nil {
		job.State = "failed"
	}
	job.UpdatedAt = time.Now().UTC()
	job.Complete = Complete(*job)
	saved, err := s.store.Finish(saveCtx, *job)
	if err == nil {
		*job = saved
	} else {
		job.State = "failed"
		job.Complete = false
		_ = s.store.Save(saveCtx, *job)
	}
}

// Serialize progress with cancel/terminal writes so an in-flight save cannot
// resurrect a cancelled task. This lock is private to the detection service.
func (s *Service) saveProgress(ctx context.Context, job Job) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	current, err := s.store.Get(ctx, job.ID)
	if err != nil {
		return err
	}
	if current.State == "cancelled" {
		return context.Canceled
	}
	return s.store.Save(ctx, job)
}

func resumeResults(job Job) []Result {
	done := map[string]Result{}
	for _, r := range job.Results {
		done[r.ScenarioID] = r
	}
	byID := map[string]Planned{}
	for _, item := range job.Plan.Items {
		byID[item.ID] = item
	}
	rerun := map[string]bool{}
	var prerequisite func(string)
	prerequisite = func(id string) {
		if rerun[id] {
			return
		}
		rerun[id] = true
		for _, parent := range byID[id].Dependencies {
			prerequisite(parent)
		}
	}
	for _, item := range job.Plan.Items {
		if _, ok := done[item.ID]; !ok {
			for _, parent := range item.Dependencies {
				prerequisite(parent)
			}
		}
	}
	out := []Result{}
	for _, r := range job.Results {
		if !rerun[r.ScenarioID] {
			out = append(out, r)
		}
	}
	return out
}

func (s *Service) Parameters(ctx context.Context) (Parameters, error) {
	if s.settings == nil {
		return Parameters{}, ErrClosed
	}
	return s.settings.Parameters(ctx)
}
func (s *Service) SetSearch(ctx context.Context, actor Actor, target string, value gatewaycontract.SearchSettings) (gatewaycontract.SearchSettings, error) {
	if actor.ID == "" || !actor.Admin {
		return value, ErrNotFound
	}
	if s.settings == nil {
		return value, ErrClosed
	}
	return s.settings.SetSearch(ctx, actor.ID, target, value)
}
func (s *Service) Start(ctx context.Context, actor Actor, target Target, secret string) (Job, error) {
	parameters, err := s.Parameters(ctx)
	if err != nil {
		return Job{}, err
	}
	return s.Create(ctx, actor, target, parameters, secret)
}
func (s *Service) AttemptPage(ctx context.Context, actor Actor, id string, cursor *Cursor, limit int) ([]Attempt, error) {
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return s.store.AttemptPage(ctx, id, cursor, limit)
}
func (s *Service) Artifacts(ctx context.Context, actor Actor, id string) ([]Artifact, error) {
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	return s.store.Artifacts(ctx, id)
}

func (s *Service) GetReport(ctx context.Context, actor Actor, id string) (Job, error) {
	job, err := s.store.GetReport(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if actor.ID == "" || (actor.ID != job.OwnerID && !actor.Admin) {
		return Job{}, ErrNotFound
	}
	return job, nil
}
func (s *Service) ReportPage(ctx context.Context, actor Actor, id string, cursor *Cursor, limit int) ([]ReportSummary, error) {
	if _, err := s.Get(ctx, actor, id); err != nil {
		return nil, err
	}
	if limit < 1 || limit > 101 {
		return nil, ErrInvalid
	}
	return s.store.ReportPage(ctx, id, cursor, limit)
}
