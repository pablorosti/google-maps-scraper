//nolint:testpackage // Tests the unexported phone normalizer alongside the export.
package leadsgen

import "testing"

func TestWhatsAppNumber(t *testing.T) {
	tests := []struct {
		name  string
		phone string
		want  string
	}{
		{"Córdoba capital mobile", "0351 15-325-4578", "5493513254578"},
		{"four-digit area code mobile", "03546 15-45-2903", "5493546452903"},
		{"Buenos Aires mobile", "011 15-3286-9273", "5491132869273"},
		{"international mobile", "+54 9 351 325-4578", "5493513254578"},
		{"landline", "0351 452-0269", ""},
		{"interior landline", "03583 40-0071", ""},
		{"Buenos Aires number without 15 is ambiguous", "011 6428-4676", ""},
		{"toll free", "0800-122-0129", ""},
		{"no area code", "487-0089", ""},
		{"mobile with wrong length", "0351 15-325-45", ""},
		{"empty", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := WhatsAppNumber(tt.phone); got != tt.want {
				t.Fatalf("WhatsAppNumber(%q) = %q, want %q", tt.phone, got, tt.want)
			}
		})
	}
}
