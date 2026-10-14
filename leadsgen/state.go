package leadsgen

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Rubro and phase statuses persisted in the state file.
const (
	StatusPending = "pending"
	StatusRunning = "running"
	// StatusPaused means the rubro reached its current share but still has
	// searches left for later rounds.
	StatusPaused = "paused"
	StatusDone   = "done"
)

// Phase completion reasons.
const (
	ReasonComplete  = "all searches completed"
	ReasonExhausted = "no new leads after several runs"
	// ReasonTarget is only found in state files from older versions.
	ReasonTarget = "rubro target reached"
)

// State is the durable progress of a province scrape. It is rewritten after
// every scraper run so a restarted container continues where it stopped.
type State struct {
	Province    Province               `json:"province"`
	TotalTarget int                    `json:"total_target"`
	Rubros      map[string]*RubroState `json:"rubros"`
	Order       []string               `json:"order"`
	Done        bool                   `json:"done"`
	UpdatedAt   time.Time              `json:"updated_at"`
}

// RubroState is the progress of one rubro.
type RubroState struct {
	Name   string `json:"name"`
	Status string `json:"status"`
	Leads  int    `json:"leads"`
	Target int    `json:"target"`
	// Exhausted means every phase ran out of searches or new leads.
	Exhausted bool                   `json:"exhausted"`
	Phases    map[string]*PhaseState `json:"phases"`
}

// PhaseState is the progress of one phase of a rubro.
type PhaseState struct {
	Done      bool   `json:"done"`
	Reason    string `json:"reason,omitempty"`
	Completed int    `json:"completed_searches"`
	Expected  int    `json:"expected_searches"`
	Runs      int    `json:"runs"`
	IdleRuns  int    `json:"idle_runs"`
}

// LoadState reads the state file, returning an empty state when missing.
func LoadState(path string) (*State, error) {
	st := &State{Rubros: map[string]*RubroState{}}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return st, nil
	}

	if err != nil {
		return nil, err
	}

	if err := json.Unmarshal(data, st); err != nil {
		return nil, fmt.Errorf("parse state %s: %w", path, err)
	}

	if st.Rubros == nil {
		st.Rubros = map[string]*RubroState{}
	}

	return st, nil
}

// Rubro returns the state of a rubro, creating it when missing.
func (s *State) Rubro(r Rubro) *RubroState {
	rs, ok := s.Rubros[r.Slug]
	if !ok {
		rs = &RubroState{Name: r.Name, Status: StatusPending, Phases: map[string]*PhaseState{}}
		s.Rubros[r.Slug] = rs
		s.Order = append(s.Order, r.Slug)
	}

	if rs.Phases == nil {
		rs.Phases = map[string]*PhaseState{}
	}

	return rs
}

// TotalLeads sums the leads of every rubro.
func (s *State) TotalLeads() int {
	total := 0
	for _, rs := range s.Rubros {
		total += rs.Leads
	}

	return total
}

// Save atomically writes the state as JSON and a human-readable progress
// report next to it.
func (s *State) Save(path, reportPath string) error {
	s.UpdatedAt = time.Now().UTC()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	if err := writeFileAtomic(path, data); err != nil {
		return err
	}

	return writeFileAtomic(reportPath, []byte(s.Report()))
}

// Report renders the progress as plain text.
func (s *State) Report() string {
	var b strings.Builder

	fmt.Fprintf(&b, "Provincia: %s\n", s.Province.Name)
	fmt.Fprintf(&b, "Leads: %d / %d\n", s.TotalLeads(), s.TotalTarget)

	if s.Done {
		b.WriteString("Estado: TERMINADO\n")
	} else {
		b.WriteString("Estado: en curso\n")
	}

	fmt.Fprintf(&b, "Actualizado: %s\n\n", s.UpdatedAt.Format(time.RFC3339))

	for _, slug := range s.Order {
		rs := s.Rubros[slug]
		fmt.Fprintf(&b, "%-36s %-8s %7d / %d\n", slug, rs.Status, rs.Leads, rs.Target)
	}

	return b.String()
}

func writeFileAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

// countCompletedSearches reads the scraper resume sidecar of a results file.
func countCompletedSearches(resultsPath string) int {
	data, err := os.ReadFile(resultsPath + ".resume.json")
	if err != nil {
		return 0
	}

	var sidecar struct {
		Completed []string `json:"completed_inputs"`
	}

	if json.Unmarshal(data, &sidecar) != nil {
		return 0
	}

	return len(sidecar.Completed)
}
