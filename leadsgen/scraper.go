package leadsgen

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"time"
)

// killGracePeriod is how long a stopped scraper may flush before SIGKILL.
const killGracePeriod = 30 * time.Second

// ScrapeOptions are the google-maps-scraper flags shared by every run.
type ScrapeOptions struct {
	Concurrency      int
	Depth            int
	GridDepth        int
	GridCellKm       float64
	GridZoom         int
	Lang             string
	Email            bool
	ProxiesFile      string
	ExitOnInactivity time.Duration
	ExtraArgs        []string
}

// ExecScraper runs the google-maps-scraper binary as a child process.
type ExecScraper struct {
	Binary  string
	Options ScrapeOptions
}

// Args returns the scraper command line for a job.
func (s *ExecScraper) Args(job ScrapeJob) []string {
	o := s.Options
	depth := o.Depth

	args := []string{
		"-input", job.InputPath,
		"-results", job.ResultsPath,
		"-resume",
		"-lang", o.Lang,
		"-c", strconv.Itoa(o.Concurrency),
		"-exit-on-inactivity", o.ExitOnInactivity.String(),
	}

	if job.GridBBox != "" {
		depth = o.GridDepth
		args = append(args,
			"-grid-bbox", job.GridBBox,
			"-grid-cell", strconv.FormatFloat(o.GridCellKm, 'f', -1, 64),
			"-zoom", strconv.Itoa(o.GridZoom),
		)
	}

	args = append(args, "-depth", strconv.Itoa(depth))

	if o.Email {
		args = append(args, "-email")
	}

	if o.ProxiesFile != "" {
		args = append(args, "-proxies-file", o.ProxiesFile)
	}

	return append(args, o.ExtraArgs...)
}

// Run executes the scraper and waits for it. Canceling ctx sends SIGTERM to
// the whole process group (scraper and browsers), then SIGKILL after a grace
// period.
func (s *ExecScraper) Run(ctx context.Context, job ScrapeJob) error {
	//nolint:gosec // The binary and flags come from the operator, not from scraped data.
	cmd := exec.CommandContext(ctx, s.Binary, s.Args(job)...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.Env = append(os.Environ(), "DISABLE_TELEMETRY=1")
	cmd.WaitDelay = killGracePeriod
	setProcessGroup(cmd)

	return cmd.Run()
}
