package leadsgen

import (
	"math"
	"net/url"
	"strings"
)

// maxLocalityDistanceKm bounds the nearest-centroid fallback: a business
// farther than this from every known locality gets no normalized location.
const maxLocalityDistanceKm = 50

// LocalityIndex maps a business to its official Georef locality and
// department (partido), so leads share consistent location names.
type LocalityIndex struct {
	province string
	locs     []Locality
	byName   map[string][]int
}

// NewLocalityIndex indexes the localities of a province.
func NewLocalityIndex(province string, locs []Locality) *LocalityIndex {
	idx := &LocalityIndex{province: province, locs: locs, byName: map[string][]int{}}

	for i, l := range locs {
		key := Fold(l.Name)
		idx.byName[key] = append(idx.byName[key], i)
	}

	return idx
}

// Resolve returns the locality of a business. A Google city that matches a
// locality name wins (the nearest one when several share it); otherwise the
// nearest locality centroid within maxLocalityDistanceKm is used.
func (idx *LocalityIndex) Resolve(googleCity string, lat, lon float64) (Locality, bool) {
	if idx == nil || len(idx.locs) == 0 {
		return Locality{}, false
	}

	candidates := idx.byName[Fold(googleCity)]
	if len(candidates) == 0 {
		if lat == 0 && lon == 0 {
			return Locality{}, false
		}

		candidates = make([]int, len(idx.locs))
		for i := range idx.locs {
			candidates[i] = i
		}
	}

	best, bestDist := -1, math.MaxFloat64

	for _, i := range candidates {
		if d := distanceKm(lat, lon, idx.locs[i].Lat, idx.locs[i].Lon); d < bestDist {
			best, bestDist = i, d
		}
	}

	if bestDist > maxLocalityDistanceKm && len(idx.byName[Fold(googleCity)]) == 0 {
		return Locality{}, false
	}

	return idx.locs[best], true
}

// ProvinceOf returns the jurisdiction name of a resolved locality.
func (idx *LocalityIndex) ProvinceOf(l *Locality) string {
	return firstNonEmpty(l.Province, idx.province)
}

// distanceKm is an equirectangular approximation, accurate enough at
// city scale.
func distanceKm(lat1, lon1, lat2, lon2 float64) float64 {
	const earthRadiusKm = 6371

	toRad := math.Pi / 180
	x := (lon2 - lon1) * toRad * math.Cos((lat1+lat2)/2*toRad)
	y := (lat2 - lat1) * toRad

	return math.Hypot(x, y) * earthRadiusKm
}

// splitWebsite separates Instagram and Facebook links from an own website.
func splitWebsite(raw string) (website, instagram, facebook string) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" {
		return raw, "", ""
	}

	host := strings.TrimPrefix(strings.ToLower(u.Host), "www.")

	switch {
	case host == "instagram.com" || strings.HasSuffix(host, ".instagram.com"):
		return "", raw, ""
	case host == "facebook.com" || strings.HasSuffix(host, ".facebook.com") || host == "fb.com":
		return "", "", raw
	default:
		return raw, "", ""
	}
}
