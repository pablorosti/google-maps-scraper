package leadsgen

import "strings"

// Argentine numbers have 10 digits after the country code: area code plus
// subscriber number, without the trunk "0" and the mobile "15".
const (
	argNationalDigits = 10
	argMobilePrefix   = "549"
	argMobileMarker   = "15"
)

// WhatsAppNumber returns the international WhatsApp form (549 + area code +
// number, e.g. 5493513254578) of an Argentine mobile phone, or "" when the
// phone is a landline or cannot be told apart from one.
//
// Google Maps writes Argentine mobiles in national format with "15" after
// the area code ("0351 15-325-4578"); numbers without that marker are
// treated as landlines.
func WhatsAppNumber(phone string) string {
	phone = strings.TrimSpace(phone)
	if phone == "" {
		return ""
	}

	if strings.HasPrefix(phone, "+") {
		d := digits(phone)
		if strings.HasPrefix(d, argMobilePrefix) && len(d) == len(argMobilePrefix)+argNationalDigits {
			return d
		}

		return ""
	}

	fields := strings.Fields(phone)
	if len(fields) < 2 || !strings.HasPrefix(fields[0], "0") {
		return ""
	}

	area := digits(fields[0])[1:]
	rest := digits(strings.Join(fields[1:], ""))

	if !strings.HasPrefix(rest, argMobileMarker) || len(area)+len(rest)-len(argMobileMarker) != argNationalDigits {
		return ""
	}

	return argMobilePrefix + area + rest[len(argMobileMarker):]
}

// WhatsAppLink returns the wa.me chat link for a WhatsApp number.
func WhatsAppLink(number string) string {
	if number == "" {
		return ""
	}

	return "https://wa.me/" + number
}

func digits(s string) string {
	var b strings.Builder

	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}

	return b.String()
}
