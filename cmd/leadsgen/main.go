// Command leadsgen scrapes a whole Argentine province rubro by rubro with
// google-maps-scraper, resuming automatically after crashes or restarts.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/gosom/google-maps-scraper/leadsgen"
)

func main() {
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal(err)
	}
}

func run() error {
	var (
		province    = flag.String("provincia", "", "province to scrape, e.g. cordoba")
		total       = flag.Int("total", 100000, "total leads to collect")
		perRubro    = flag.Int("por-rubro", 0, "max leads per rubro, filling rubros in order (default 0: spread the total across all rubros)")
		rubros      = flag.String("rubros", "", "comma-separated rubro slugs (default: all, in catalog order)")
		listRubros  = flag.Bool("list-rubros", false, "print the rubro catalog and exit")
		outDir      = flag.String("out", "leads-output", "output folder")
		folder      = flag.String("carpeta", "", "province folder name inside -out (default: province slug)")
		binary      = flag.String("scraper", "google-maps-scraper", "path to the google-maps-scraper binary")
		concurrency = flag.Int("c", 4, "scraper concurrency")
		depth       = flag.Int("depth", 10, "scroll depth for locality searches")
		gridDepth   = flag.Int("grid-depth", 1, "scroll depth for grid searches in big cities")
		gridCell    = flag.Float64("grid-cell", 1.0, "grid cell size in km for big cities")
		gridZoom    = flag.Int("grid-zoom", 16, "zoom for grid searches")
		proxies     = flag.String("proxies-file", "", "file with one proxy URL per line")
		extraArgs   = flag.String("scraper-args", "", "extra space-separated flags passed to the scraper")
		georefURL   = flag.String("georef-url", leadsgen.DefaultGeorefURL, "Georef API base URL")
		exportOnly  = flag.Bool("solo-exportar", false, "rewrite the rubro CSVs from the scraped data and exit")
		waitDone    = flag.Bool("wait-when-done", false, "keep running idle after finishing (for Docker restart policies)")
	)

	flag.Parse()

	catalog := leadsgen.DefaultCatalog()

	if *listRubros {
		for _, r := range catalog {
			fmt.Printf("%-36s %s\n", r.Slug, r.Name)
		}

		return nil
	}

	if strings.TrimSpace(*province) == "" {
		return errors.New("-provincia is required")
	}

	if *total <= 0 || *perRubro < 0 {
		return errors.New("-total must be positive and -por-rubro cannot be negative")
	}

	selected, err := leadsgen.SelectRubros(catalog, *rubros)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scraper := &leadsgen.ExecScraper{
		Binary: *binary,
		Options: leadsgen.ScrapeOptions{
			Concurrency:      *concurrency,
			Depth:            *depth,
			GridDepth:        *gridDepth,
			GridCellKm:       *gridCell,
			GridZoom:         *gridZoom,
			Lang:             "es",
			Email:            true, // emails are what make the leads sellable
			ProxiesFile:      *proxies,
			ExitOnInactivity: 5 * time.Minute,
			ExtraArgs:        strings.Fields(*extraArgs),
		},
	}

	orch := leadsgen.NewOrchestrator(&leadsgen.Config{
		OutDir:         *outDir,
		Province:       *province,
		Folder:         *folder,
		TotalTarget:    *total,
		PerRubroTarget: *perRubro,
		Rubros:         selected,
		GridCellKm:     *gridCell,
		PollInterval:   time.Minute,
		StallTimeout:   20 * time.Minute,
		MaxRunDuration: 3 * time.Hour,
		MaxIdleRuns:    4,
		RetryDelay:     30 * time.Second,
		MaxRetryDelay:  10 * time.Minute,
		Cooldown:       10 * time.Minute,
		MaxCooldown:    2 * time.Hour,
	}, scraper, leadsgen.NewGeoref(*georefURL))

	if *exportOnly {
		return orch.Export(ctx)
	}

	if err := orch.Run(ctx); err != nil {
		return err
	}

	if *waitDone {
		log.Print("leadsgen: done, idling until stopped")
		<-ctx.Done()
	}

	return nil
}
