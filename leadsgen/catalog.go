// Package leadsgen orchestrates long-running, self-resuming lead scrapes for a
// whole Argentine province, split by business category (rubro).
package leadsgen

import (
	"fmt"
	"strings"
)

// Rubro is a sellable business category and the Google Maps search terms
// that feed it.
type Rubro struct {
	Slug  string
	Name  string
	Terms []string
}

// DefaultCatalog returns the rubros scraped by default, ordered by priority.
// It follows the categories sold by dir.ar, plus a few high-volume extras.
//
//nolint:misspell // Spanish search terms and slugs.
func DefaultCatalog() []Rubro {
	return []Rubro{
		{"gastronomia", "Gastronomía", []string{"restaurantes", "bares", "pizzerías", "cafeterías", "heladerías", "rotiserías", "parrillas", "hamburgueserías", "cervecerías"}},
		{"kioscos-y-almacenes", "Kioscos, polirrubros y almacenes", []string{"kioscos", "almacenes", "polirrubros", "despensas", "golosinerías"}},
		{"supermercados-y-mayoristas", "Retail: supermercados y mayoristas", []string{"supermercados", "autoservicios", "minimercados", "mayoristas", "distribuidoras de alimentos", "verdulerías", "dietéticas"}},
		{"panaderias", "Panaderías", []string{"panaderías", "confiterías"}},
		{"carnicerias", "Carnicerías", []string{"carnicerías", "pollerías", "fiambrerías"}},
		{"alimentos-y-bebidas", "Alimentos y bebidas", []string{"distribuidoras de bebidas", "vinotecas", "fábricas de alimentos", "productores de alimentos"}},
		{"salud", "Salud", []string{"consultorios médicos", "clínicas", "centros médicos", "odontólogos", "kinesiólogos", "psicólogos", "laboratorios de análisis clínicos", "ópticas"}},
		{"farmacias", "Farmacias y laboratorios", []string{"farmacias", "droguerías", "laboratorios farmacéuticos"}},
		{"construccion", "Construcción", []string{"corralones", "materiales de construcción", "empresas constructoras", "sanitarios", "pinturerías", "aberturas", "vidrierías"}},
		{"ferreterias", "Ferreterías y herramientas", []string{"ferreterías", "herramientas", "bulonerías", "casas de electricidad"}},
		{"inmobiliarias", "Inmobiliarias y desarrolladoras", []string{"inmobiliarias", "desarrolladoras inmobiliarias", "loteos"}},
		{"administraciones-de-consorcios", "Administraciones de consorcios", []string{"administraciones de consorcios", "administración de edificios"}},
		{"arquitectos", "Arquitectos y estudios de arquitectura", []string{"arquitectos", "estudios de arquitectura"}},
		{"talleres-mecanicos", "Talleres mecánicos", []string{"talleres mecánicos", "chapa y pintura", "electricidad del automotor", "lavaderos de autos"}},
		{"concesionarias", "Concesionarias de autos", []string{"concesionarias de autos", "agencias de autos usados", "concesionarias de motos"}},
		{"repuestos-y-lubricentros", "Repuestos y lubricentros", []string{"repuestos de autos", "lubricentros", "accesorios para autos"}},
		{"gomerias", "Gomerías", []string{"gomerías", "neumáticos"}},
		{"ropa", "Tiendas de ropa", []string{"tiendas de ropa", "zapaterías", "lencerías", "ropa infantil", "boutiques"}},
		{"belleza-y-estetica", "Belleza y estética", []string{"peluquerías", "barberías", "centros de estética", "salones de uñas", "spa"}},
		{"hogar-y-electrodomesticos", "Hogar y electrodomésticos", []string{"mueblerías", "electrodomésticos", "colchonerías", "tiendas de decoración"}},
		{"bazar-regalerias-papeleras", "Bazar, regalerías, papeleras y cotillón", []string{"bazares", "regalerías", "librerías", "papeleras", "cotillón", "jugueterías"}},
		{"celulares-electronica-informatica", "Celulares, electrónica e informática", []string{"tiendas de celulares", "reparación de celulares", "casas de computación", "servicio técnico de computadoras", "electrónica"}},
		{"educacion", "Educación", []string{"colegios", "escuelas", "jardines de infantes", "institutos", "academias de idiomas", "universidades"}},
		{"gimnasios", "Gimnasios", []string{"gimnasios", "crossfit", "pilates", "yoga"}},
		{"clubes-y-deportes", "Clubes y deportes", []string{"clubes", "canchas de fútbol", "tiendas de deportes", "natación"}},
		{"veterinarias-y-mascotas", "Veterinarias y tiendas de mascotas", []string{"veterinarias", "pet shops", "peluquerías caninas"}},
		{"hoteles-y-alojamientos", "Hoteles y alojamientos", []string{"hoteles", "cabañas", "hosterías", "apart hotel", "hostels", "campings"}},
		{"turismo-y-viajes", "Turismo y viajes", []string{"agencias de viajes", "agencias de turismo", "excursiones"}},
		{"transporte-y-logistica", "Transportes, logística y distribución", []string{"empresas de transporte", "logística", "fletes", "mudanzas", "remiserías"}},
		{"seguros", "Seguros", []string{"seguros", "productores de seguros", "aseguradoras"}},
		{"abogados", "Abogados", []string{"abogados", "estudios jurídicos", "escribanías"}},
		{"contadores", "Contadores y estudios contables", []string{"contadores", "estudios contables"}},
		{"marketing-y-publicidad", "Agencias de marketing y publicidad", []string{"agencias de marketing", "agencias de publicidad", "diseño gráfico"}},
		{"graficas-e-imprentas", "Gráficas e imprentas", []string{"imprentas", "gráficas", "carteles publicitarios"}},
		{"catering-y-eventos", "Catering y eventos", []string{"catering", "salones de eventos", "organización de eventos"}},
		{"geriatricos", "Geriátricos", []string{"geriátricos", "residencias para adultos mayores"}},
		{"agropecuario", "Sector agropecuario", []string{"agroveterinarias", "semillerías", "maquinaria agrícola", "acopio de cereales", "insumos agropecuarios"}},
		{"fabricas-e-industrias", "Fábricas e industrias", []string{"fábricas", "industrias", "metalúrgicas", "carpinterías"}},
		{"comercio-exterior", "Comercio exterior", []string{"importadores", "exportadores", "despachantes de aduana"}},
	}
}

// SelectRubros returns the catalog entries matching the comma-separated
// slugs, keeping the order given by the caller. An empty list returns the
// whole catalog.
func SelectRubros(catalog []Rubro, slugs string) ([]Rubro, error) {
	if strings.TrimSpace(slugs) == "" {
		return catalog, nil
	}

	bySlug := make(map[string]Rubro, len(catalog))
	for _, r := range catalog {
		bySlug[r.Slug] = r
	}

	var selected []Rubro

	for _, s := range strings.Split(slugs, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}

		r, ok := bySlug[s]
		if !ok {
			return nil, fmt.Errorf("unknown rubro %q", s)
		}

		selected = append(selected, r)
	}

	return selected, nil
}
