//nolint:misspell,testpackage // Spanish fixtures; tests reach orchestrator internals.
package leadsgen

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gosom/google-maps-scraper/gmaps"
)

var testProvince = Province{ID: "14", Name: "Córdoba"}

func TestPlanPhases(t *testing.T) {
	r := Rubro{Slug: "gastronomia", Terms: []string{"restaurantes", "bares"}}
	locs := []Locality{
		{ID: "1", Name: "Villa María"},
		{ID: "2", Name: "Córdoba"},
		{ID: "3", Name: "Cordoba"}, // duplicated name, accent-insensitive
	}
	city := City{Name: "Córdoba", Lat: -31.42, Lon: -64.19, RadiusKm: 2}

	phases := PlanPhases(r, testProvince, locs, []City{city}, 1)

	if len(phases) != 2 {
		t.Fatalf("got %d phases, want 2", len(phases))
	}

	byLoc := phases[0]
	if byLoc.Name != "localidades" || byLoc.Expected != 4 || byLoc.GridBBox != "" {
		t.Fatalf("unexpected locality phase: %+v", byLoc)
	}

	want := "restaurantes en Villa María, Córdoba, Argentina #!#gastronomia|restaurantes|1"
	if byLoc.Lines[0] != want {
		t.Fatalf("line = %q, want %q", byLoc.Lines[0], want)
	}

	grid := phases[1]
	if grid.Name != "grid-cordoba" || grid.GridBBox == "" {
		t.Fatalf("unexpected grid phase: %+v", grid)
	}

	if grid.Expected <= len(r.Terms) || grid.Expected%len(r.Terms) != 0 {
		t.Fatalf("grid expected = %d, want terms x cells", grid.Expected)
	}

	if grid.Lines[0] != "restaurantes #!#gastronomia|restaurantes|grid-cordoba" {
		t.Fatalf("grid line = %q", grid.Lines[0])
	}
}

func TestSelectRubros(t *testing.T) {
	catalog := DefaultCatalog()

	all, err := SelectRubros(catalog, "")
	if err != nil || len(all) != len(catalog) {
		t.Fatalf("empty selection: got %d, %v", len(all), err)
	}

	got, err := SelectRubros(catalog, "panaderias, gastronomia")
	if err != nil || len(got) != 2 || got[0].Slug != "panaderias" || got[1].Slug != "gastronomia" {
		t.Fatalf("selection = %+v, %v", got, err)
	}

	if _, err := SelectRubros(catalog, "nope"); err == nil {
		t.Fatal("expected error for unknown rubro")
	}
}

func TestLeadSetFiltersAndExports(t *testing.T) {
	dir := t.TempDir()
	raw := filepath.Join(dir, "raw.csv")

	mobile := entry("p1", "Bar Uno", "Córdoba")
	mobile.Phone = "0351 15-325-4578"

	noPhone := entry("p5", "Bar sin telefono", "Córdoba")
	noPhone.Phone = ""

	writeRaw(t, raw, []*gmaps.Entry{
		mobile,
		entry("p1", "Bar Uno dup", "Córdoba"),
		entry("p2", "Bar Santa Fe", "Santa Fe"),
		entry("p3", "Bar sin provincia", ""),
		closedEntry("p4"),
		noPhone,
	})

	// Simulate a scraper killed mid-write.
	f, _ := os.OpenFile(raw, os.O_APPEND|os.O_WRONLY, 0o644)
	_, _ = f.WriteString(`x,"https://broken`)
	f.Close()

	set := NewLeadSet("Córdoba")
	if err := set.AddFile(raw); err != nil {
		t.Fatal(err)
	}

	if err := set.AddFile(filepath.Join(dir, "missing.csv")); err != nil {
		t.Fatalf("missing file: %v", err)
	}

	if set.Len() != 2 {
		t.Fatalf("got %d leads, want 2: %+v", set.Len(), set.Leads)
	}

	out := filepath.Join(dir, "out", "gastronomia.csv")
	if err := set.WriteCSV(out); err != nil {
		t.Fatal(err)
	}

	data, _ := os.ReadFile(out)
	if !strings.HasPrefix(string(data), utf8BOM+"nombre;rubro;rubro_google;telefono;whatsapp;link_whatsapp;emails") {
		t.Fatalf("missing BOM or header: %q", string(data[:60]))
	}

	r := csv.NewReader(strings.NewReader(strings.TrimPrefix(string(data), utf8BOM)))
	r.Comma = ';'

	rows, err := r.ReadAll()
	if err != nil || len(rows) != 3 || len(rows[0]) != 22 {
		t.Fatalf("rows = %v, %v", rows, err)
	}

	col := columnLookup(rows[0])

	if col(rows[1], "nombre") != "Bar Uno" || col(rows[1], "localidad_google") != "Córdoba Capital" {
		t.Fatalf("first row = %v", rows[1])
	}

	if col(rows[1], "whatsapp") != "5493513254578" || col(rows[1], "link_whatsapp") != "https://wa.me/5493513254578" {
		t.Fatalf("mobile whatsapp = %q, %q", col(rows[1], "whatsapp"), col(rows[1], "link_whatsapp"))
	}

	// The landline gets no WhatsApp.
	if col(rows[2], "whatsapp") != "" || col(rows[2], "link_whatsapp") != "" {
		t.Fatalf("landline whatsapp = %q", col(rows[2], "whatsapp"))
	}
}

// fakeScraper appends a batch of leads per run. A rubro with a cap stops
// finding leads at the cap and then reports every search as completed, like
// a small rubro that ran out of businesses.
type fakeScraper struct {
	mu        sync.Mutex
	calls     int
	perRun    int
	failFirst bool
	next      int
	caps      map[string]int
	found     map[string]int
}

func (f *fakeScraper) Run(_ context.Context, job ScrapeJob) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.failFirst && f.calls == 1 {
		return errors.New("browser crashed")
	}

	if f.found == nil {
		f.found = map[string]int{}
	}

	slug := filepath.Base(filepath.Dir(job.ResultsPath))
	limit, capped := f.caps[slug]

	n := f.perRun
	if capped {
		n = max(0, min(n, limit-f.found[slug]))
	}

	var entries []*gmaps.Entry

	for range n {
		f.next++
		entries = append(entries, entry(fmt.Sprintf("id-%d", f.next), "Negocio", "Córdoba"))
	}

	f.found[slug] += n

	appendRaw(job.ResultsPath, entries)

	if !capped || f.found[slug] < limit {
		return nil
	}

	data, _ := os.ReadFile(job.InputPath)
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	sidecar, _ := json.Marshal(map[string]any{"version": 1, "completed_inputs": lines})

	return os.WriteFile(job.ResultsPath+".resume.json", sidecar, 0o600)
}

type fakeGeo struct{}

func (fakeGeo) Province(context.Context, string) (Province, error) { return testProvince, nil }

func (fakeGeo) Localities(context.Context, string) ([]Locality, error) {
	return []Locality{{ID: "1", Name: "Villa María"}}, nil
}

func testConfig(dir string, rubros []Rubro) Config {
	return Config{
		OutDir:         dir,
		Province:       "cordoba",
		TotalTarget:    5,
		PerRubroTarget: 3,
		Rubros:         rubros,
		GridCellKm:     1,
		PollInterval:   time.Hour,
		StallTimeout:   time.Hour,
		MaxRunDuration: time.Hour,
		MaxIdleRuns:    2,
		RetryDelay:     time.Millisecond,
		MaxRetryDelay:  time.Millisecond,
		Cooldown:       time.Millisecond,
		MaxCooldown:    4 * time.Millisecond,
	}
}

func testRubros() []Rubro {
	return []Rubro{
		{Slug: "gastronomia", Name: "Gastronomía", Terms: []string{"bares"}},
		{Slug: "panaderias", Name: "Panaderías", Terms: []string{"panaderías"}},
		{Slug: "ferreterias", Name: "Ferreterías", Terms: []string{"ferreterías"}},
	}
}

func TestOrchestratorFillsRubrosUntilTotal(t *testing.T) {
	dir := t.TempDir()
	scraper := &fakeScraper{perRun: 3, failFirst: true}
	orch := NewOrchestrator(new(testConfig(dir, testRubros())), scraper, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	st, err := LoadState(filepath.Join(dir, "cordoba", ".state", "state.json"))
	if err != nil {
		t.Fatal(err)
	}

	if !st.Done || st.TotalLeads() < 5 {
		t.Fatalf("state done=%v leads=%d", st.Done, st.TotalLeads())
	}

	// gastronomia hits its 3-lead cap, panaderias gets the remaining 2, and
	// ferreterias is never started.
	if st.Rubros["gastronomia"].Target != 3 || st.Rubros["panaderias"].Target != 2 {
		t.Fatalf("targets = %d, %d", st.Rubros["gastronomia"].Target, st.Rubros["panaderias"].Target)
	}

	if _, ok := st.Rubros["ferreterias"]; ok {
		t.Fatal("ferreterias should not have started")
	}

	for _, name := range []string{"gastronomia.csv", "panaderias.csv", "PROGRESO.txt"} {
		if _, err := os.Stat(filepath.Join(dir, "cordoba", name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}

	// A crashed first run is retried, not skipped.
	if scraper.calls != 3 {
		t.Fatalf("scraper calls = %d, want 3 (1 failed + 2 rubros)", scraper.calls)
	}

	// Restarting after completion resumes from state and scrapes nothing.
	again := &fakeScraper{perRun: 3}
	orch = NewOrchestrator(new(testConfig(dir, testRubros())), again, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if again.calls != 0 {
		t.Fatalf("resumed run scraped %d times, want 0", again.calls)
	}
}

func TestOrchestratorMarksExhaustedPhases(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros()[:1])
	cfg.TotalTarget, cfg.PerRubroTarget = 100, 100

	// A scraper that never completes searches nor finds leads.
	scraper := scraperFunc(func(context.Context, ScrapeJob) error { return nil })
	orch := NewOrchestrator(&cfg, scraper, fakeGeo{})
	orch.logf = t.Logf

	var pauses []time.Duration

	orch.sleep = func(_ context.Context, d time.Duration) error {
		pauses = append(pauses, d)
		return nil
	}

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	ps := orch.state.Rubros["gastronomia"].Phases["localidades"]
	if !ps.Done || ps.Reason != ReasonExhausted || ps.Runs != 2 {
		t.Fatalf("phase = %+v", ps)
	}

	// Each unproductive run pauses, doubling up to MaxCooldown.
	if len(pauses) < 3 || pauses[0] != time.Millisecond || pauses[1] != 2*time.Millisecond || pauses[2] != 4*time.Millisecond {
		t.Fatalf("pauses = %v", pauses[:min(len(pauses), 4)])
	}

	for _, d := range pauses {
		if d > 4*time.Millisecond {
			t.Fatalf("pause %s exceeds MaxCooldown", d)
		}
	}
}

type scraperFunc func(context.Context, ScrapeJob) error

func (f scraperFunc) Run(ctx context.Context, job ScrapeJob) error { return f(ctx, job) }

func entry(id, title, state string) *gmaps.Entry {
	return &gmaps.Entry{
		PlaceID:         id,
		Title:           title,
		Link:            "https://maps.google.com/?q=" + id,
		Phone:           "0351 123456",
		CompleteAddress: gmaps.Address{City: "Córdoba Capital", State: state},
	}
}

func closedEntry(id string) *gmaps.Entry {
	e := entry(id, "Cerrado", "Córdoba")
	e.Status = "Cerrado permanentemente"

	return e
}

func writeRaw(t *testing.T, path string, entries []*gmaps.Entry) {
	t.Helper()
	appendRaw(path, entries)
}

func appendRaw(path string, entries []*gmaps.Entry) {
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	_, statErr := os.Stat(path)

	f, _ := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	defer f.Close()

	w := csv.NewWriter(f)
	if statErr != nil {
		_ = w.Write((&gmaps.Entry{}).CsvHeaders())
	}

	for _, e := range entries {
		_ = w.Write(e.CsvRow())
	}

	w.Flush()
}

func TestCooldown(t *testing.T) {
	tests := []struct {
		name        string
		consecutive int
		want        time.Duration
	}{
		{"first pause is the base", 1, 10 * time.Minute},
		{"second pause doubles", 2, 20 * time.Minute},
		{"fourth pause", 4, 80 * time.Minute},
		{"capped at max", 10, 2 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cooldown(10*time.Minute, 2*time.Hour, tt.consecutive); got != tt.want {
				t.Fatalf("cooldown = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestScraperArgsAlwaysExtractEmails(t *testing.T) {
	s := &ExecScraper{Options: ScrapeOptions{Email: true, Lang: "es", Concurrency: 2, Depth: 10}}

	args := strings.Join(s.Args(ScrapeJob{InputPath: "q.txt", ResultsPath: "r.csv"}), " ")
	if !strings.Contains(args, "-email") || !strings.Contains(args, "-resume") {
		t.Fatalf("args = %s", args)
	}
}

func TestLeadSetBuenosAiresIncludesCABA(t *testing.T) {
	tests := []struct {
		name     string
		province string
		state    string
		kept     bool
	}{
		{"province keeps provincia de buenos aires", "Buenos Aires", "Provincia de Buenos Aires", true},
		{"Buenos Aires includes CABA addresses", "Buenos Aires", "Cdad. Autónoma de Buenos Aires", true},
		{"Buenos Aires includes capital federal", "Buenos Aires", "Capital Federal", true},
		{"Buenos Aires drops other provinces", "Buenos Aires", "Santa Fe", false},
		{"CABA keeps CABA address", "Ciudad Autónoma de Buenos Aires", "CABA", true},
		{"CABA drops province address", "Ciudad Autónoma de Buenos Aires", "Provincia de Buenos Aires", false},
		{"unknown state is kept", "Buenos Aires", "", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raw := filepath.Join(t.TempDir(), "raw.csv")
			writeRaw(t, raw, []*gmaps.Entry{entry("p1", "Negocio", tt.state)})

			set := NewLeadSet(tt.province)
			if err := set.AddFile(raw); err != nil {
				t.Fatal(err)
			}

			if got := set.Len() == 1; got != tt.kept {
				t.Fatalf("kept = %v, want %v", got, tt.kept)
			}
		})
	}
}

func TestBigCitiesGridConurbanoAndCABA(t *testing.T) {
	r := Rubro{Slug: "gastronomia", Terms: []string{"bares"}}
	locs := []Locality{{ID: "1", Name: "Quilmes"}}

	phases := PlanPhases(r, Province{ID: "06", Name: "Buenos Aires"}, locs, BigCities("06"), 1)

	byName := map[string]Phase{}
	for _, p := range phases {
		byName[p.Name] = p
	}

	for _, zone := range []string{"grid-conurbano-norte", "grid-conurbano-oeste", "grid-conurbano-sur", "grid-la-plata"} {
		p, ok := byName[zone]
		if !ok {
			t.Fatalf("missing phase %s in %v", zone, phases)
		}

		// Each zone is tens of km wide, so a 1 km grid has hundreds of cells.
		if p.Expected < 100 {
			t.Fatalf("%s expected %d searches, want a dense grid", zone, p.Expected)
		}
	}

	caba := PlanPhases(r, Province{ID: "02", Name: "Ciudad Autónoma de Buenos Aires"}, locs, BigCities("02"), 1)
	if last := caba[len(caba)-1]; last.GridBBox != cabaBox {
		t.Fatalf("CABA grid bbox = %q, want %q", last.GridBBox, cabaBox)
	}
}

func TestOrchestratorUsesConfiguredFolder(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros()[:1])
	cfg.Folder = "caba"

	orch := NewOrchestrator(&cfg, &fakeScraper{perRun: 3}, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, p := range []string{"caba/gastronomia.csv", "caba/PROGRESO.txt", "caba/.state/state.json"} {
		if _, err := os.Stat(filepath.Join(dir, p)); err != nil {
			t.Fatalf("missing %s: %v", p, err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "cordoba")); !os.IsNotExist(err) {
		t.Fatalf("province slug folder should not exist, stat err = %v", err)
	}
}

func TestOrchestratorBalancesAcrossRubros(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros())
	cfg.TotalTarget, cfg.PerRubroTarget = 12, 0

	// panaderias only has 2 businesses; its unused share goes to the others.
	scraper := &fakeScraper{perRun: 2, caps: map[string]int{"panaderias": 2}}
	orch := NewOrchestrator(&cfg, scraper, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	st := orch.state
	if !st.Done || st.TotalLeads() < 12 {
		t.Fatalf("done=%v total=%d, want >= 12", st.Done, st.TotalLeads())
	}

	pan := st.Rubros["panaderias"]
	if !pan.Exhausted || pan.Status != StatusDone || pan.Leads != 2 {
		t.Fatalf("panaderias = %+v, want exhausted with 2 leads", pan)
	}

	// Round one gives every rubro 4. Round two raises the two rubros left to
	// ceil((12-2)/2) = 5: gastronomia overshoots to 6, which already completes
	// the total, so ferreterias keeps its 4.
	want := map[string]int{"gastronomia": 6, "ferreterias": 4}
	for slug, leads := range want {
		rs := st.Rubros[slug]
		if rs.Leads != leads || rs.Exhausted || rs.Status != StatusPaused {
			t.Fatalf("%s = %+v, want %d leads and paused", slug, rs, leads)
		}
	}
}

func TestOrchestratorBalancedCoversEveryRubro(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros())
	cfg.TotalTarget, cfg.PerRubroTarget = 9, 0

	scraper := &fakeScraper{perRun: 3}
	orch := NewOrchestrator(&cfg, scraper, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	for _, r := range testRubros() {
		if got := orch.state.Rubros[r.Slug].Leads; got != 3 {
			t.Fatalf("%s leads = %d, want 3", r.Slug, got)
		}

		if _, err := os.Stat(filepath.Join(dir, "cordoba", r.Slug+".csv")); err != nil {
			t.Fatalf("missing %s.csv: %v", r.Slug, err)
		}
	}

	if scraper.calls != 3 {
		t.Fatalf("calls = %d, want one run per rubro", scraper.calls)
	}
}

func TestOrchestratorReopensPhasesStoppedByOldTargets(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros()[:1])
	cfg.TotalTarget, cfg.PerRubroTarget = 6, 0

	// State left by the previous version: rubro done, phase closed by target.
	old := &State{Rubros: map[string]*RubroState{}}
	old.Rubro(testRubros()[0]).Status = StatusDone
	old.Rubros["gastronomia"].Phases["localidades"] = &PhaseState{Done: true, Reason: ReasonTarget}

	stateDir := filepath.Join(dir, "cordoba", ".state")
	if err := old.Save(filepath.Join(stateDir, "state.json"), filepath.Join(dir, "cordoba", "PROGRESO.txt")); err != nil {
		t.Fatal(err)
	}

	scraper := &fakeScraper{perRun: 3}
	orch := NewOrchestrator(&cfg, scraper, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := orch.state.Rubros["gastronomia"].Leads; got != 6 || scraper.calls != 2 {
		t.Fatalf("leads = %d, calls = %d; want the old phase resumed to 6 leads", got, scraper.calls)
	}
}

func TestOrchestratorSettlesRubroLeftRunning(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros()[:1])
	cfg.TotalTarget, cfg.PerRubroTarget = 6, 0

	// State left by a run cut short mid-rubro after the total was already met.
	old := &State{Rubros: map[string]*RubroState{}}
	rs := old.Rubro(testRubros()[0])
	rs.Status, rs.Leads, rs.Target = StatusRunning, 6, 8

	stateDir := filepath.Join(dir, "cordoba", ".state")
	if err := old.Save(filepath.Join(stateDir, "state.json"), filepath.Join(dir, "cordoba", "PROGRESO.txt")); err != nil {
		t.Fatal(err)
	}

	scraper := &fakeScraper{perRun: 3}
	orch := NewOrchestrator(&cfg, scraper, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	if got := orch.state.Rubros["gastronomia"].Status; !orch.state.Done || got != StatusPaused || scraper.calls != 0 {
		t.Fatalf("done = %v, status = %q, calls = %d; want done, paused, no scraping", orch.state.Done, got, scraper.calls)
	}
}

func TestExportRewritesCSVsWithoutScraping(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros())

	orch := NewOrchestrator(&cfg, &fakeScraper{perRun: 3}, fakeGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	csvPath := filepath.Join(dir, "cordoba", "gastronomia.csv")
	statePath := filepath.Join(dir, "cordoba", ".state", "state.json")

	if err := os.Remove(csvPath); err != nil {
		t.Fatal(err)
	}

	stateBefore, _ := os.ReadFile(statePath)

	never := scraperFunc(func(context.Context, ScrapeJob) error {
		t.Fatal("export must not scrape")
		return nil
	})

	exp := NewOrchestrator(&cfg, never, fakeGeo{})
	exp.logf = t.Logf

	if err := exp.Export(context.Background()); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(csvPath)
	if err != nil || !strings.Contains(string(data), "whatsapp;link_whatsapp") {
		t.Fatalf("csv not rewritten: %v", err)
	}

	// Every exported lead carries its general rubro.
	if !strings.Contains(string(data), "Negocio;Gastronomía;") {
		t.Fatalf("rubro column missing: %s", data)
	}

	if stateAfter, _ := os.ReadFile(statePath); !bytes.Equal(stateAfter, stateBefore) {
		t.Fatal("export must not change the saved state")
	}

	// Rubros never scraped get no file.
	if _, err := os.Stat(filepath.Join(dir, "cordoba", "ferreterias.csv")); !os.IsNotExist(err) {
		t.Fatalf("ferreterias.csv should not exist: %v", err)
	}
}

// baGeo serves Buenos Aires province and CABA localities by Georef ID.
type baGeo struct{}

func (baGeo) Province(context.Context, string) (Province, error) {
	return Province{ID: buenosAiresID, Name: "Buenos Aires"}, nil
}

func (baGeo) Localities(_ context.Context, id string) ([]Locality, error) {
	switch id {
	case buenosAiresID:
		return []Locality{{ID: "06-1", Name: "Mar del Plata"}}, nil
	case cabaID:
		return []Locality{{ID: "02-1", Name: "Recoleta"}}, nil
	}

	return nil, fmt.Errorf("unexpected province %s", id)
}

func TestBuenosAiresScrapesCABAToo(t *testing.T) {
	dir := t.TempDir()
	cfg := testConfig(dir, testRubros()[:1])
	cfg.TotalTarget, cfg.PerRubroTarget = 3, 0

	orch := NewOrchestrator(&cfg, &fakeScraper{perRun: 3}, baGeo{})
	orch.logf = t.Logf

	if err := orch.Run(context.Background()); err != nil {
		t.Fatal(err)
	}

	queries, err := os.ReadFile(filepath.Join(dir, "buenos-aires", ".raw", "gastronomia", "localidades.queries.txt"))
	if err != nil {
		t.Fatal(err)
	}

	// CABA barrios come first, each searched with its own jurisdiction name.
	lines := strings.Split(strings.TrimSpace(string(queries)), "\n")
	if len(lines) != 2 ||
		!strings.HasPrefix(lines[0], "bares en Recoleta, Ciudad Autónoma de Buenos Aires, Argentina") ||
		!strings.HasPrefix(lines[1], "bares en Mar del Plata, Buenos Aires, Argentina") {
		t.Fatalf("queries = %q", lines)
	}

	// The CABA grid is swept before the conurbano.
	cities := BigCities(buenosAiresID)
	if cities[0].Box != cabaBox || cities[1].Name != "Conurbano Norte" {
		t.Fatalf("first grids = %+v, %+v", cities[0], cities[1])
	}
}

func columnLookup(header []string) func(row []string, name string) string {
	idx := map[string]int{}
	for i, h := range header {
		idx[h] = i
	}

	return func(row []string, name string) string {
		i, ok := idx[name]
		if !ok {
			panic("unknown column " + name)
		}

		return row[i]
	}
}

func TestSplitWebsite(t *testing.T) {
	tests := []struct {
		name                     string
		url                      string
		web, instagram, facebook string
	}{
		{"own website", "http://lacapke.com/", "http://lacapke.com/", "", ""},
		{"instagram", "https://www.instagram.com/panicafe/", "", "https://www.instagram.com/panicafe/", ""},
		{"facebook", "https://m.facebook.com/barcito", "", "", "https://m.facebook.com/barcito"},
		{"fb short link", "https://fb.com/barcito", "", "", "https://fb.com/barcito"},
		{"linktree stays as website", "https://linktr.ee/barcito", "https://linktr.ee/barcito", "", ""},
		{"empty", "", "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			web, ig, fb := splitWebsite(tt.url)
			if web != tt.web || ig != tt.instagram || fb != tt.facebook {
				t.Fatalf("splitWebsite(%q) = %q, %q, %q", tt.url, web, ig, fb)
			}
		})
	}
}

func TestLocalityIndexResolve(t *testing.T) {
	idx := NewLocalityIndex("Buenos Aires", []Locality{
		{ID: "1", Name: "San Isidro", Department: "San Isidro", Lat: -34.4699, Lon: -58.5113},
		{ID: "2", Name: "Martínez", Department: "San Isidro", Lat: -34.489, Lon: -58.4994},
		{ID: "3", Name: "Recoleta", Department: "Comuna 2", Lat: -34.5875, Lon: -58.3974, Province: cabaName},
		{ID: "4", Name: "San Isidro", Department: "Lomas de Zamora", Lat: -34.80, Lon: -58.40},
	})

	tests := []struct {
		name       string
		googleCity string
		lat, lon   float64
		wantName   string
		wantDept   string
		wantProv   string
	}{
		{"Google city matches, accent-insensitive", "Martinez", -34.4699, -58.5113, "Martínez", "San Isidro", "Buenos Aires"},
		{"same name picks the nearest", "San Isidro", -34.79, -58.41, "San Isidro", "Lomas de Zamora", "Buenos Aires"},
		{"unknown city falls back to nearest centroid", "Acassuso", -34.475, -58.505, "San Isidro", "San Isidro", "Buenos Aires"},
		{"CABA barrio keeps its jurisdiction", "Buenos Aires", -34.588, -58.398, "Recoleta", "Comuna 2", cabaName},
		{"too far from any locality", "Rosario", -32.95, -60.64, "", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loc, ok := idx.Resolve(tt.googleCity, tt.lat, tt.lon)
			if tt.wantName == "" {
				if ok {
					t.Fatalf("Resolve = %+v, want no match", loc)
				}

				return
			}

			if !ok || loc.Name != tt.wantName || loc.Department != tt.wantDept || idx.ProvinceOf(&loc) != tt.wantProv {
				t.Fatalf("Resolve = %+v (%v), want %s / %s / %s", loc, ok, tt.wantName, tt.wantDept, tt.wantProv)
			}
		})
	}
}

func TestLeadSetEnrichesLeads(t *testing.T) {
	raw := filepath.Join(t.TempDir(), "raw.csv")
	e := entry("p1", "Bar", "Provincia de Buenos Aires")
	e.WebSite = "https://www.instagram.com/bar/"
	e.Latitude, e.Longtitude = -34.489, -58.4994
	e.CompleteAddress.City = "Martínez"

	writeRaw(t, raw, []*gmaps.Entry{e})

	idx := NewLocalityIndex("Buenos Aires", []Locality{
		{ID: "2", Name: "Martínez", Department: "San Isidro", Lat: -34.489, Lon: -58.4994},
	})

	set := NewLeadSet("Buenos Aires").WithEnrichment("Gastronomía", idx)
	if err := set.AddFile(raw); err != nil {
		t.Fatal(err)
	}

	if set.Len() != 1 {
		t.Fatalf("leads = %d", set.Len())
	}

	l := set.Leads[0]
	if l.Rubro != "Gastronomía" || l.Instagram != "https://www.instagram.com/bar/" || l.Website != "" ||
		l.Locality != "Martínez" || l.Department != "San Isidro" || l.Province != "Buenos Aires" {
		t.Fatalf("lead = %+v", l)
	}
}
