package leadsgen

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultGeorefURL is the official Argentine geographic normalization API.
const DefaultGeorefURL = "https://apis.datos.gob.ar/georef/api"

// georefMaxResults is above the largest province locality count (~1.2k).
const georefMaxResults = 5000

// Province is an Argentine province as returned by Georef.
type Province struct {
	ID   string `json:"id"`
	Name string `json:"nombre"`
}

// Slug returns the folder-safe name of the province.
func (p Province) Slug() string {
	return Slugify(p.Name)
}

// Georef IDs of the jurisdictions sold as one "Buenos Aires" base.
const (
	buenosAiresID = "06"
	cabaID        = "02"
)

// cabaName is how CABA localities are named in search queries.
const cabaName = "Ciudad Autónoma de Buenos Aires"

// Locality is a populated place inside a province.
type Locality struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Province overrides the scraped province name in search queries, for
	// CABA barrios scraped as part of Buenos Aires.
	Province   string  `json:"province,omitempty"`
	Department string  `json:"department"`
	Lat        float64 `json:"lat"`
	Lon        float64 `json:"lon"`
}

// Georef resolves provinces and their localities.
type Georef struct {
	BaseURL string
	Client  *http.Client
}

// NewGeoref returns a client for the given base URL.
func NewGeoref(baseURL string) *Georef {
	return &Georef{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Client:  &http.Client{Timeout: 60 * time.Second},
	}
}

// Province resolves a free-form province name (e.g. "cordoba") to its
// canonical Georef record.
func (g *Georef) Province(ctx context.Context, name string) (Province, error) {
	var resp struct {
		Provinces []Province `json:"provincias"`
	}

	q := url.Values{"nombre": {name}, "campos": {"id,nombre"}, "max": {"1"}}
	if err := g.get(ctx, "/provincias", q, &resp); err != nil {
		return Province{}, err
	}

	if len(resp.Provinces) == 0 {
		return Province{}, fmt.Errorf("province %q not found", name)
	}

	return resp.Provinces[0], nil
}

// Localities returns every locality of the province.
func (g *Georef) Localities(ctx context.Context, provinceID string) ([]Locality, error) {
	var resp struct {
		Localities []struct {
			ID         string `json:"id"`
			Name       string `json:"nombre"`
			Department struct {
				Name string `json:"nombre"`
			} `json:"departamento"`
			Centroid struct {
				Lat float64 `json:"lat"`
				Lon float64 `json:"lon"`
			} `json:"centroide"`
		} `json:"localidades"`
	}

	q := url.Values{
		"provincia": {provinceID},
		"campos":    {"id,nombre,departamento.nombre,centroide"},
		"max":       {fmt.Sprint(georefMaxResults)},
	}
	if err := g.get(ctx, "/localidades", q, &resp); err != nil {
		return nil, err
	}

	locs := make([]Locality, 0, len(resp.Localities))
	for _, l := range resp.Localities {
		locs = append(locs, Locality{
			ID:         l.ID,
			Name:       l.Name,
			Department: l.Department.Name,
			Lat:        l.Centroid.Lat,
			Lon:        l.Centroid.Lon,
		})
	}

	return locs, nil
}

func (g *Georef) get(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, g.BaseURL+path+"?"+q.Encode(), http.NoBody)
	if err != nil {
		return err
	}

	resp, err := g.Client.Do(req)
	if err != nil {
		return fmt.Errorf("georef %s: %w", path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("georef %s: status %d: %s", path, resp.StatusCode, body)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("georef %s: decode: %w", path, err)
	}

	return nil
}

var accentReplacer = strings.NewReplacer(
	"á", "a", "é", "e", "í", "i", "ó", "o", "ú", "u", "ü", "u", "ñ", "n",
	"Á", "a", "É", "e", "Í", "i", "Ó", "o", "Ú", "u", "Ü", "u", "Ñ", "n",
)

// Fold lowercases s and strips Spanish accents, for accent-insensitive
// comparisons.
func Fold(s string) string {
	return strings.ToLower(accentReplacer.Replace(strings.TrimSpace(s)))
}

// Slugify turns a name into a lowercase, accent-free, dash-separated slug.
func Slugify(s string) string {
	var b strings.Builder

	dash := false

	for _, r := range Fold(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)

			dash = false

			continue
		}

		if !dash && b.Len() > 0 {
			b.WriteByte('-')

			dash = true
		}
	}

	return strings.TrimRight(b.String(), "-")
}
