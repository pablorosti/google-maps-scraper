package leadsgen

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// utf8BOM makes Excel detect UTF-8 when opening the CSV.
const utf8BOM = "\xEF\xBB\xBF"

// excelSeparator is the list separator of Spanish-locale Excel.
const excelSeparator = ';'

// cabaKey identifies the Ciudad Autónoma de Buenos Aires, which Google writes
// as "CABA", "Cdad. Autónoma de Buenos Aires" or "Capital Federal".
const cabaKey = "caba"

var cabaMarkers = []string{"autonoma", "caba", "capital federal", "ciudad de buenos aires"}

// provinceKeys are accent-free fragments that identify each Argentine
// province in a Google Maps address. CABA markers are checked first so
// "buenos aires" only matches the province.
var provinceKeys = []string{
	"santiago del estero", "tierra del fuego", "buenos aires", "catamarca", "chaco",
	"chubut", "cordoba", "corrientes", "entre rios", "formosa", "jujuy", "la pampa",
	"la rioja", "mendoza", "misiones", "neuquen", "rio negro", "salta", "san juan",
	"san luis", "santa cruz", "santa fe", "tucuman",
}

// Lead is one exported business contact.
type Lead struct {
	Name       string
	Category   string
	Rubro      string
	Phone      string
	WhatsApp   string
	Emails     string
	Website    string
	Instagram  string
	Facebook   string
	Address    string
	Locality   string
	Department string
	Province   string
	PostalCode string
	GoogleCity string
	Lat        string
	Lon        string
	Rating     string
	Reviews    string
	MapsURL    string
	PlaceID    string

	// googleState is the raw province from Google, used for filtering.
	googleState string
}

var leadHeader = []string{
	"nombre", "rubro", "rubro_google", "telefono", "whatsapp", "link_whatsapp", "emails",
	"sitio_web", "instagram", "facebook", "direccion", "localidad", "partido", "provincia",
	"codigo_postal", "localidad_google", "latitud", "longitud", "rating", "cantidad_resenas",
	"link_google_maps", "place_id",
}

func (l *Lead) row() []string {
	return []string{
		l.Name, l.Rubro, l.Category, l.Phone, l.WhatsApp, WhatsAppLink(l.WhatsApp), l.Emails,
		l.Website, l.Instagram, l.Facebook, l.Address, l.Locality, l.Department, l.Province,
		l.PostalCode, l.GoogleCity, l.Lat, l.Lon, l.Rating, l.Reviews,
		l.MapsURL, l.PlaceID,
	}
}

// LeadSet accumulates unique leads of one province.
type LeadSet struct {
	accepted   map[string]bool
	seen       map[string]bool
	rubro      string
	localities *LocalityIndex
	Leads      []Lead
}

// NewLeadSet returns an empty set that only accepts leads located in the
// given province (or with an unknown province). Buenos Aires also accepts
// CABA, since both are sold as one base.
func NewLeadSet(provinceName string) *LeadSet {
	accepted := map[string]bool{}

	if k := provinceKeyOf(provinceName); k != "" {
		accepted[k] = true
		if k == "buenos aires" {
			accepted[cabaKey] = true
		}
	}

	return &LeadSet{accepted: accepted, seen: map[string]bool{}}
}

// WithEnrichment makes the set fill the general rubro name and the official
// locality, partido and province of every lead.
func (s *LeadSet) WithEnrichment(rubro string, localities *LocalityIndex) *LeadSet {
	s.rubro, s.localities = rubro, localities

	return s
}

// Len returns the number of unique leads.
func (s *LeadSet) Len() int {
	return len(s.Leads)
}

// AddFile reads a raw scraper CSV and adds its valid, unseen leads (those
// with a phone number). A missing
// file adds nothing, and a truncated trailing record (from a killed scraper)
// is ignored.
func (s *LeadSet) AddFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}

		return err
	}
	defer f.Close()

	r := csv.NewReader(f)
	r.FieldsPerRecord = -1
	r.LazyQuotes = true

	header, err := r.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}

		return fmt.Errorf("read header of %s: %w", path, err)
	}

	idx := make(map[string]int, len(header))
	for i, h := range header {
		idx[strings.TrimPrefix(h, utf8BOM)] = i
	}

	for {
		rec, err := r.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}

		if err != nil {
			// Partial last record written while the scraper was killed.
			return nil
		}

		lead := leadFromRecord(rec, idx)
		s.add(&lead)
	}
}

func (s *LeadSet) add(l *Lead) {
	key := firstNonEmpty(l.PlaceID, l.MapsURL)
	// The phone is what makes a lead sellable, so businesses without one are skipped.
	if l.Name == "" || l.Phone == "" || key == "" || s.seen[key] {
		return
	}

	if k := provinceKeyOf(l.googleState); k != "" && len(s.accepted) > 0 && !s.accepted[k] {
		return
	}

	l.Rubro = s.rubro

	lat, latErr := strconv.ParseFloat(l.Lat, 64)
	lon, lonErr := strconv.ParseFloat(l.Lon, 64)

	if latErr != nil || lonErr != nil {
		lat, lon = 0, 0
	}

	if loc, ok := s.localities.Resolve(l.GoogleCity, lat, lon); ok {
		l.Locality, l.Department, l.Province = loc.Name, loc.Department, s.localities.ProvinceOf(&loc)
	}

	s.seen[key] = true
	s.Leads = append(s.Leads, *l)
}

type rawAddress struct {
	City       string `json:"city"`
	PostalCode string `json:"postal_code"`
	State      string `json:"state"`
}

func leadFromRecord(rec []string, idx map[string]int) Lead {
	get := func(name string) string {
		i, ok := idx[name]
		if !ok || i >= len(rec) {
			return ""
		}

		return strings.TrimSpace(rec[i])
	}

	if isPermanentlyClosed(get("status")) {
		return Lead{}
	}

	var addr rawAddress

	_ = json.Unmarshal([]byte(get("complete_address")), &addr)

	website, instagram, facebook := splitWebsite(get("website"))

	return Lead{
		Name:        get("title"),
		Category:    get("category"),
		Phone:       get("phone"),
		WhatsApp:    WhatsAppNumber(get("phone")),
		Emails:      get("emails"),
		Website:     website,
		Instagram:   instagram,
		Facebook:    facebook,
		Address:     get("address"),
		GoogleCity:  addr.City,
		Province:    addr.State,
		googleState: addr.State,
		PostalCode:  addr.PostalCode,
		Lat:         trimFloat(get("latitude")),
		Lon:         trimFloat(get("longitude")),
		Rating:      trimFloat(get("review_rating")),
		Reviews:     get("review_count"),
		MapsURL:     get("link"),
		PlaceID:     firstNonEmpty(get("place_id"), get("cid"), get("data_id")),
	}
}

// WriteCSV atomically writes the leads as an Excel-friendly CSV.
func (s *LeadSet) WriteCSV(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}

	tmp := path + ".tmp"

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	if _, err := f.WriteString(utf8BOM); err != nil {
		f.Close()
		return err
	}

	w := csv.NewWriter(f)
	w.Comma = excelSeparator

	_ = w.Write(leadHeader)
	for i := range s.Leads {
		_ = w.Write(s.Leads[i].row())
	}

	w.Flush()

	if err := w.Error(); err != nil {
		f.Close()
		return err
	}

	if err := f.Close(); err != nil {
		return err
	}

	return os.Rename(tmp, path)
}

func provinceKeyOf(name string) string {
	folded := Fold(name)
	if folded == "" {
		return ""
	}

	for _, m := range cabaMarkers {
		if strings.Contains(folded, m) {
			return cabaKey
		}
	}

	for _, k := range provinceKeys {
		if strings.Contains(folded, k) {
			return k
		}
	}

	return ""
}

func isPermanentlyClosed(status string) bool {
	s := Fold(status)
	return strings.Contains(s, "permanente") || strings.Contains(s, "permanently")
}

func trimFloat(s string) string {
	if !strings.Contains(s, ".") {
		return s
	}

	s = strings.TrimRight(s, "0")

	return strings.TrimSuffix(s, ".")
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}

	return ""
}
