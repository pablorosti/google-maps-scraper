package leadsgen

import (
	"fmt"
	"math"
	"strings"

	"github.com/gosom/google-maps-scraper/grid"
)

// kmPerDegreeLat is the approximate length of one degree of latitude.
const kmPerDegreeLat = 111.0

// City is a large urban area where a single "<term> en <city>" search is
// capped by Google Maps (~120 results), so it is also swept with a grid.
// The grid is the square of RadiusKm around the center, unless Box sets an
// explicit "minLat,minLon,maxLat,maxLon" area.
type City struct {
	Name     string
	Lat      float64
	Lon      float64
	RadiusKm float64
	Box      string
}

// BBox returns the grid bounding box formatted as "minLat,minLon,maxLat,maxLon".
func (c City) BBox() string {
	if c.Box != "" {
		return c.Box
	}

	dLat := c.RadiusKm / kmPerDegreeLat
	dLon := c.RadiusKm / (kmPerDegreeLat * math.Cos(c.Lat*math.Pi/180))

	return fmt.Sprintf("%.5f,%.5f,%.5f,%.5f", c.Lat-dLat, c.Lon-dLon, c.Lat+dLat, c.Lon+dLon)
}

// bigCities lists, per Georef province ID, the cities that get a grid sweep.
// Provinces missing here are scraped by locality only. Centers come from
// Georef locality centroids. Buenos Aires includes CABA, swept first, and the
// Gran Buenos Aires zones around it.
var bigCities = map[string][]City{
	"02": {{Name: cabaName, Box: cabaBox}},
	"06": {
		{Name: cabaName, Box: cabaBox},
		{Name: "Conurbano Norte", Box: "-34.527,-58.950,-34.330,-58.450"},
		{Name: "Conurbano Oeste", Box: "-34.800,-58.950,-34.527,-58.531"},
		{Name: "Conurbano Sur", Box: "-34.950,-58.531,-34.655,-58.150"},
		{Name: "La Plata", Lat: -34.9214, Lon: -57.9545, RadiusKm: 6},
		{Name: "Mar del Plata", Lat: -38.0055, Lon: -57.5426, RadiusKm: 7},
		{Name: "Bahía Blanca", Lat: -38.7196, Lon: -62.2724, RadiusKm: 5},
		{Name: "Tandil", Lat: -37.3287, Lon: -59.1369, RadiusKm: 3},
		{Name: "Luján", Lat: -34.5668, Lon: -59.1145, RadiusKm: 3},
		{Name: "Zárate", Lat: -34.0989, Lon: -59.0297, RadiusKm: 3},
		{Name: "Junín", Lat: -34.5939, Lon: -60.9464, RadiusKm: 3},
		{Name: "Pergamino", Lat: -33.8911, Lon: -60.5746, RadiusKm: 3},
		{Name: "Olavarría", Lat: -36.8938, Lon: -60.3232, RadiusKm: 3},
	},
	"14": {
		{Name: "Córdoba", Lat: -31.4201, Lon: -64.1888, RadiusKm: 11},
		{Name: "Río Cuarto", Lat: -33.1232, Lon: -64.3493, RadiusKm: 5},
		{Name: "Villa María", Lat: -32.4075, Lon: -63.2402, RadiusKm: 4},
		{Name: "San Francisco", Lat: -31.4280, Lon: -62.0828, RadiusKm: 3.5},
		{Name: "Villa Carlos Paz", Lat: -31.4241, Lon: -64.4978, RadiusKm: 4},
		{Name: "Alta Gracia", Lat: -31.6529, Lon: -64.4283, RadiusKm: 3},
		{Name: "Río Tercero", Lat: -32.1730, Lon: -64.1141, RadiusKm: 3},
	},
	"50": {{Name: "Mendoza", Lat: -32.8895, Lon: -68.8458, RadiusKm: 8}},
	"82": {
		{Name: "Rosario", Lat: -32.9468, Lon: -60.6393, RadiusKm: 9},
		{Name: "Santa Fe", Lat: -31.6333, Lon: -60.7000, RadiusKm: 6},
	},
	"90": {{Name: "San Miguel de Tucumán", Lat: -26.8083, Lon: -65.2176, RadiusKm: 6}},
}

// cabaBox is the bounding box of the Ciudad Autónoma de Buenos Aires.
const cabaBox = "-34.705,-58.531,-34.527,-58.335"

// BigCities returns the grid-swept cities of a province.
func BigCities(provinceID string) []City {
	return bigCities[provinceID]
}

// Phase is one scraper run: a queries file plus, for grid phases, a bounding
// box. Expected is the number of seed jobs the scraper will create, used to
// tell when the phase has been fully scraped.
type Phase struct {
	Name     string
	Lines    []string
	GridBBox string
	Expected int
}

// PlanPhases builds the phases for a rubro: first one search per term and
// locality across the whole province, then a grid sweep per big city.
func PlanPhases(r Rubro, prov Province, locs []Locality, cities []City, cellKm float64) []Phase {
	var phases []Phase

	seen := make(map[string]bool, len(locs))
	byLocality := Phase{Name: "localidades"}

	for _, loc := range locs {
		provName := firstNonEmpty(loc.Province, prov.Name)

		key := Fold(loc.Name + "|" + provName)
		if key == "" || seen[key] {
			continue
		}

		seen[key] = true

		for _, term := range r.Terms {
			byLocality.Lines = append(byLocality.Lines, fmt.Sprintf(
				"%s en %s, %s, Argentina #!#%s|%s|%s",
				term, loc.Name, provName, r.Slug, Slugify(term), loc.ID,
			))
		}
	}

	byLocality.Expected = len(byLocality.Lines)
	if byLocality.Expected > 0 {
		phases = append(phases, byLocality)
	}

	for _, c := range cities {
		bbox := c.BBox()

		parsed, err := grid.ParseBoundingBox(bbox)
		if err != nil {
			continue
		}

		cells := len(grid.GenerateCells(parsed, cellKm))
		p := Phase{Name: "grid-" + Slugify(c.Name), GridBBox: bbox}

		for _, term := range r.Terms {
			p.Lines = append(p.Lines, fmt.Sprintf("%s #!#%s|%s|%s", term, r.Slug, Slugify(term), p.Name))
		}

		p.Expected = len(p.Lines) * cells
		phases = append(phases, p)
	}

	return phases
}

// QueriesFile renders the phase lines as a scraper input file.
func (p Phase) QueriesFile() string {
	return strings.Join(p.Lines, "\n") + "\n"
}
