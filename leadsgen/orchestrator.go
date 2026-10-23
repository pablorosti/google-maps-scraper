package leadsgen

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

// ScrapeJob is one scraper invocation.
type ScrapeJob struct {
	InputPath   string
	ResultsPath string
	GridBBox    string
}

// Scraper runs one resumable scrape until it finishes or ctx is canceled.
type Scraper interface {
	Run(ctx context.Context, job ScrapeJob) error
}

// LocationSource resolves a province and its localities.
type LocationSource interface {
	Province(ctx context.Context, name string) (Province, error)
	Localities(ctx context.Context, provinceID string) ([]Locality, error)
}

// Config drives an Orchestrator.
type Config struct {
	OutDir   string
	Province string
	// Folder is the output folder name inside OutDir; it defaults to the
	// province slug.
	Folder      string
	TotalTarget int
	// PerRubroTarget caps each rubro and fills rubros in order. Zero spreads
	// the total evenly across every rubro instead (see runBalanced).
	PerRubroTarget int
	Rubros         []Rubro
	GridCellKm     float64

	// PollInterval is how often leads are recounted while a scraper runs.
	PollInterval time.Duration
	// StallTimeout kills a scraper whose results did not grow for this long.
	StallTimeout time.Duration
	// MaxRunDuration recycles the scraper (and its browsers) periodically.
	MaxRunDuration time.Duration
	// MaxIdleRuns is how many clean runs without progress end a phase.
	MaxIdleRuns int
	// RetryDelay is the base wait between Georef retries; it grows up to MaxRetryDelay.
	RetryDelay    time.Duration
	MaxRetryDelay time.Duration
	// Cooldown is the pause after a run that found nothing new, which usually
	// means Google is throttling or blocking. It doubles on every consecutive
	// unproductive run, up to MaxCooldown, and resets as soon as leads flow.
	Cooldown    time.Duration
	MaxCooldown time.Duration
}

// Orchestrator scrapes a province rubro by rubro until the lead target is met,
// persisting progress so it can resume after any crash or restart.
type Orchestrator struct {
	cfg     Config
	scraper Scraper
	geo     LocationSource
	logf    func(format string, args ...any)
	sleep   func(ctx context.Context, d time.Duration) error

	// unproductive counts consecutive runs without new leads or searches.
	unproductive int

	provDir  string
	locIndex *LocalityIndex
	state    *State
}

// NewOrchestrator builds an orchestrator.
func NewOrchestrator(cfg *Config, scraper Scraper, geo LocationSource) *Orchestrator {
	return &Orchestrator{cfg: *cfg, scraper: scraper, geo: geo, logf: log.Printf, sleep: sleep}
}

// Run scrapes until the target is reached or every rubro is exhausted. It
// only returns early when ctx is canceled.
func (o *Orchestrator) Run(ctx context.Context) error {
	prov, err := o.open(ctx)
	if err != nil {
		return err
	}

	o.state.Province = prov
	o.state.TotalTarget = o.cfg.TotalTarget
	o.state.Done = false

	locs, err := o.localities(ctx, prov)
	if err != nil {
		return err
	}

	if o.cfg.PerRubroTarget > 0 {
		o.logf("leadsgen: %s, %d localities, target %d leads (%d per rubro)",
			prov.Name, len(locs), o.cfg.TotalTarget, o.cfg.PerRubroTarget)

		err = o.runSequential(ctx, prov, locs)
	} else {
		o.logf("leadsgen: %s, %d localities, target %d leads spread across %d rubros",
			prov.Name, len(locs), o.cfg.TotalTarget, len(o.cfg.Rubros))

		err = o.runBalanced(ctx, prov, locs)
	}

	if err != nil {
		return err
	}

	// A run cut short mid-rubro leaves it "running"; if the total was met on
	// restart, that rubro is never touched again, so settle it here.
	for _, rs := range o.state.Rubros {
		if rs.Status == StatusRunning {
			rs.Status = StatusPaused
		}
	}

	o.state.Done = true
	if err := o.save(); err != nil {
		return err
	}

	o.logf("leadsgen: finished %s with %d leads in %s", prov.Name, o.state.TotalLeads(), o.provDir)

	return nil
}

// Export rewrites the clean CSV of every rubro that has scraped data, without
// scraping or touching the saved state. It is safe while a scrape runs.
func (o *Orchestrator) Export(ctx context.Context) error {
	prov, err := o.open(ctx)
	if err != nil {
		return err
	}

	o.state.Province = prov

	locs, err := o.localities(ctx, prov)
	if err != nil {
		return err
	}

	for _, r := range o.cfg.Rubros {
		if _, ok := o.state.Rubros[r.Slug]; !ok {
			continue
		}

		phases := PlanPhases(r, prov, locs, BigCities(prov.ID), o.cfg.GridCellKm)
		if err := o.refreshRubro(r, phases); err != nil {
			return err
		}

		o.logf("leadsgen: exported %s (%d leads)", r.Slug, o.state.Rubros[r.Slug].Leads)
	}

	return nil
}

// open resolves the province and loads its saved state.
func (o *Orchestrator) open(ctx context.Context) (Province, error) {
	prov, err := retry(ctx, o, "resolve province", func() (Province, error) {
		return o.geo.Province(ctx, o.cfg.Province)
	})
	if err != nil {
		return Province{}, err
	}

	folder := o.cfg.Folder
	if folder == "" {
		folder = prov.Slug()
	}

	o.provDir = filepath.Join(o.cfg.OutDir, folder)

	o.state, err = LoadState(o.statePath())

	return prov, err
}

// runSequential fills rubros in catalog order, each up to PerRubroTarget.
func (o *Orchestrator) runSequential(ctx context.Context, prov Province, locs []Locality) error {
	for _, r := range o.cfg.Rubros {
		if o.state.TotalLeads() >= o.cfg.TotalTarget {
			return nil
		}

		rs := o.state.Rubro(r)
		if rs.Status == StatusDone {
			continue
		}

		target := min(o.cfg.PerRubroTarget, o.cfg.TotalTarget-(o.state.TotalLeads()-rs.Leads))
		if err := o.runRubro(ctx, r, prov, locs, target); err != nil {
			return err
		}

		rs.Status = StatusDone

		if err := o.save(); err != nil {
			return err
		}
	}

	return nil
}

// runBalanced spreads the total across every rubro. Each round raises all
// rubros that still have searches left to the same level: what is missing
// from the total, divided among them. Rubros that run out of businesses keep
// what they found, and the next round hands their unused share to the rest.
// Targets are absolute, so a restart mid-round resumes fairly.
func (o *Orchestrator) runBalanced(ctx context.Context, prov Province, locs []Locality) error {
	for o.state.TotalLeads() < o.cfg.TotalTarget {
		var active []Rubro

		exhaustedLeads := 0

		for _, r := range o.cfg.Rubros {
			if rs := o.state.Rubro(r); rs.Exhausted {
				exhaustedLeads += rs.Leads
			} else {
				active = append(active, r)
			}
		}

		if len(active) == 0 {
			o.logf("leadsgen: every rubro ran out of businesses at %d leads", o.state.TotalLeads())
			return nil
		}

		level := ceilDiv(o.cfg.TotalTarget-exhaustedLeads, len(active))
		o.logf("leadsgen: round with %d active rubros, %d leads each", len(active), level)

		for _, r := range active {
			if o.state.TotalLeads() >= o.cfg.TotalTarget {
				return nil
			}

			if o.state.Rubro(r).Leads >= level {
				continue
			}

			if err := o.runRubro(ctx, r, prov, locs, level); err != nil {
				return err
			}
		}
	}

	return nil
}

// runRubro scrapes a rubro until it has target leads or runs out of searches.
func (o *Orchestrator) runRubro(ctx context.Context, r Rubro, prov Province, locs []Locality, target int) error {
	rs := o.state.Rubro(r)
	rs.Target = target
	rs.Status = StatusRunning

	phases := PlanPhases(r, prov, locs, BigCities(prov.ID), o.cfg.GridCellKm)

	if err := o.refreshRubro(r, phases); err != nil {
		return err
	}

	for _, p := range phases {
		if rs.Leads >= rs.Target {
			break
		}

		if err := o.runPhase(ctx, r, phases, p); err != nil {
			return err
		}
	}

	rs.Exhausted = true

	for _, p := range phases {
		if ps := rs.Phases[p.Name]; ps == nil || !ps.Done {
			rs.Exhausted = false
		}
	}

	rs.Status = StatusPaused
	if rs.Exhausted {
		rs.Status = StatusDone
		o.logf("leadsgen: rubro %s ran out of businesses with %d leads", r.Slug, rs.Leads)
	} else {
		o.logf("leadsgen: rubro %s reached %d leads", r.Slug, rs.Leads)
	}

	return o.save()
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func (o *Orchestrator) runPhase(ctx context.Context, r Rubro, phases []Phase, p Phase) error {
	rs := o.state.Rubro(r)

	ps, ok := rs.Phases[p.Name]
	if !ok {
		ps = &PhaseState{}
		rs.Phases[p.Name] = ps
	}

	// Phases stopped by a lead target in older runs still have work left.
	if ps.Done && ps.Reason == ReasonTarget {
		ps.Done, ps.Reason = false, ""
	}

	if ps.Done {
		return nil
	}

	ps.Expected = p.Expected
	job := ScrapeJob{
		InputPath:   o.rawPath(r, p.Name+".queries.txt"),
		ResultsPath: o.rawPath(r, p.Name+".csv"),
		GridBBox:    p.GridBBox,
	}

	if err := writeFileAtomic(job.InputPath, []byte(p.QueriesFile())); err != nil {
		return err
	}

	for !ps.Done && rs.Leads < rs.Target {
		if err := ctx.Err(); err != nil {
			return err
		}

		leadsBefore, completedBefore := rs.Leads, countCompletedSearches(job.ResultsPath)

		o.logf("leadsgen: %s/%s run %d (%d/%d searches, %d/%d leads)",
			r.Slug, p.Name, ps.Runs+1, completedBefore, p.Expected, rs.Leads, rs.Target)

		outcome, runErr := o.runOnce(ctx, r, phases, job)
		ps.Runs++
		ps.Completed = countCompletedSearches(job.ResultsPath)

		if err := o.refreshRubro(r, phases); err != nil {
			return err
		}

		if runErr != nil && ctx.Err() == nil && outcome == outcomeClean {
			o.logf("leadsgen: %s/%s scraper failed: %v", r.Slug, p.Name, runErr)
		}

		progressed := rs.Leads > leadsBefore || ps.Completed > completedBefore

		switch {
		case ps.Completed >= p.Expected:
			ps.Done, ps.Reason = true, ReasonComplete
		case ctx.Err() != nil:
			return errors.Join(o.save(), ctx.Err())
		case progressed:
			ps.IdleRuns, o.unproductive = 0, 0
		default:
			if outcome == outcomeClean && runErr == nil {
				ps.IdleRuns++
				if ps.IdleRuns >= o.cfg.MaxIdleRuns {
					ps.Done, ps.Reason = true, ReasonExhausted
				}
			}
		}

		if err := o.save(); err != nil {
			return err
		}

		if ps.Done || progressed || rs.Leads >= rs.Target {
			continue
		}

		o.unproductive++
		delay := cooldown(o.cfg.Cooldown, o.cfg.MaxCooldown, o.unproductive)
		o.logf("leadsgen: %s/%s found nothing new (possible Google block or no internet), pausing %s",
			r.Slug, p.Name, delay)

		if err := o.sleep(ctx, delay); err != nil {
			return err
		}
	}

	if ps.Done {
		o.logf("leadsgen: %s/%s done: %s", r.Slug, p.Name, ps.Reason)
	}

	return nil
}

type runOutcome int

const (
	// outcomeClean means the scraper exited by itself.
	outcomeClean runOutcome = iota
	// outcomeTarget means the scraper was stopped because the target was met.
	outcomeTarget
	// outcomeRecycled means the scraper was stopped by the stall or max
	// duration watchdog and must simply be restarted.
	outcomeRecycled
)

// runOnce runs the scraper while a watchdog recounts leads, stopping it when
// the rubro target is met, when results stall, or after MaxRunDuration.
func (o *Orchestrator) runOnce(ctx context.Context, r Rubro, phases []Phase, job ScrapeJob) (runOutcome, error) {
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	done := make(chan error, 1)

	go func() { done <- o.scraper.Run(runCtx, job) }()

	ticker := time.NewTicker(o.cfg.PollInterval)
	defer ticker.Stop()

	started := time.Now()
	lastSize, lastGrowth := fileSize(job.ResultsPath), time.Now()
	outcome := outcomeClean

	for {
		select {
		case err := <-done:
			return outcome, err
		case <-ticker.C:
		}

		if outcome != outcomeClean {
			continue
		}

		if size := fileSize(job.ResultsPath); size != lastSize {
			lastSize, lastGrowth = size, time.Now()
		}

		if err := o.refreshRubro(r, phases); err != nil {
			o.logf("leadsgen: recount failed: %v", err)
		}

		rs := o.state.Rubro(r)

		switch {
		case rs.Leads >= rs.Target:
			o.logf("leadsgen: %s reached %d leads, stopping scraper", r.Slug, rs.Leads)

			outcome = outcomeTarget
		case time.Since(lastGrowth) > o.cfg.StallTimeout:
			o.logf("leadsgen: no new results for %s, restarting scraper", o.cfg.StallTimeout)

			outcome = outcomeRecycled
		case time.Since(started) > o.cfg.MaxRunDuration:
			o.logf("leadsgen: recycling scraper after %s", o.cfg.MaxRunDuration)

			outcome = outcomeRecycled
		default:
			if err := o.save(); err != nil {
				o.logf("leadsgen: save state failed: %v", err)
			}

			continue
		}

		cancel()
	}
}

// refreshRubro recounts the unique leads of a rubro across its phase files and
// rewrites its clean export.
func (o *Orchestrator) refreshRubro(r Rubro, phases []Phase) error {
	set := NewLeadSet(o.state.Province.Name).WithEnrichment(r.Name, o.locIndex)

	for _, p := range phases {
		if err := set.AddFile(o.rawPath(r, p.Name+".csv")); err != nil {
			return err
		}
	}

	if set.Len() > 0 {
		if err := set.WriteCSV(filepath.Join(o.provDir, r.Slug+".csv")); err != nil {
			return err
		}
	}

	o.state.Rubro(r).Leads = set.Len()

	return nil
}

func (o *Orchestrator) localities(ctx context.Context, prov Province) ([]Locality, error) {
	cache := filepath.Join(o.provDir, ".state", "localidades.json")

	if data, err := os.ReadFile(cache); err == nil {
		var locs []Locality
		if json.Unmarshal(data, &locs) == nil && len(locs) > 0 {
			o.locIndex = NewLocalityIndex(prov.Name, locs)

			return locs, nil
		}
	}

	locs, err := retry(ctx, o, "fetch localities", func() ([]Locality, error) {
		return o.geo.Localities(ctx, prov.ID)
	})
	if err != nil {
		return nil, err
	}

	// Buenos Aires is sold as one base: CABA barrios go first, as the densest
	// commercial area, followed by the province.
	if prov.ID == buenosAiresID {
		barrios, err := retry(ctx, o, "fetch CABA localities", func() ([]Locality, error) {
			return o.geo.Localities(ctx, cabaID)
		})
		if err != nil {
			return nil, err
		}

		for i := range barrios {
			barrios[i].Province = cabaName
		}

		locs = append(barrios, locs...)
	}

	data, err := json.MarshalIndent(locs, "", "  ")
	if err != nil {
		return nil, err
	}

	o.locIndex = NewLocalityIndex(prov.Name, locs)

	return locs, writeFileAtomic(cache, data)
}

func (o *Orchestrator) save() error {
	return o.state.Save(o.statePath(), filepath.Join(o.provDir, "PROGRESO.txt"))
}

func (o *Orchestrator) statePath() string {
	return filepath.Join(o.provDir, ".state", "state.json")
}

func (o *Orchestrator) rawPath(r Rubro, name string) string {
	return filepath.Join(o.provDir, ".raw", r.Slug, name)
}

// retry calls fn until it succeeds or ctx is canceled, backing off between
// attempts.
func retry[T any](ctx context.Context, o *Orchestrator, what string, fn func() (T, error)) (T, error) {
	for attempt := 1; ; attempt++ {
		v, err := fn()
		if err == nil {
			return v, nil
		}

		delay := min(o.cfg.RetryDelay*time.Duration(attempt), o.cfg.MaxRetryDelay)
		o.logf("leadsgen: %s failed (%v), retrying in %s", what, err, delay)

		if err := o.sleep(ctx, delay); err != nil {
			var zero T
			return zero, fmt.Errorf("%s: %w", what, err)
		}
	}
}

// cooldown returns base doubled for every consecutive unproductive run.
func cooldown(base, maxDelay time.Duration, consecutive int) time.Duration {
	d := base
	for i := 1; i < consecutive && d < maxDelay; i++ {
		d *= 2
	}

	return min(d, maxDelay)
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return -1
	}

	return info.Size()
}
