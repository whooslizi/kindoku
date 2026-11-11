package abi

import (
	"errors"
	"strings"
	"time"
	"unicode"
)

var errDate = errors.New("date does not match format")

// Month names by language, January first. Each entry may list alternatives separated by "|".
var monthNames = map[string][12]string{
	"en": {"january|jan", "february|feb", "march|mar", "april|apr", "may", "june|jun", "july|jul", "august|aug", "september|sept|sep", "october|oct", "november|nov", "december|dec"},
	"es": {"enero|ene", "febrero|feb", "marzo|mar", "abril|abr", "mayo|may", "junio|jun", "julio|jul", "agosto|ago", "septiembre|setiembre|sept|sep", "octubre|oct", "noviembre|nov", "diciembre|dic"},
	"it": {"gennaio|gen", "febbraio|feb", "marzo|mar", "aprile|apr", "maggio|mag", "giugno|giu", "luglio|lug", "agosto|ago", "settembre|set", "ottobre|ott", "novembre|nov", "dicembre|dic"},
	"fr": {"janvier|janv", "février|févr", "mars", "avril|avr", "mai", "juin", "juillet|juil", "août", "septembre|sept", "octobre|oct", "novembre|nov", "décembre|déc"},
	"pt": {"janeiro|jan", "fevereiro|fev", "março|mar", "abril|abr", "maio|mai", "junho|jun", "julho|jul", "agosto|ago", "setembro|set", "outubro|out", "novembro|nov", "dezembro|dez"},
	"de": {"januar|jan", "februar|feb", "märz|mär", "april|apr", "mai", "juni|jun", "juli|jul", "august|aug", "september|sep", "oktober|okt", "november|nov", "dezember|dez"},
	"id": {"januari|jan", "februari|feb", "maret|mar", "april|apr", "mei", "juni|jun", "juli|jul", "agustus|agu|agt", "september|sep", "oktober|okt", "november|nov", "desember|des"},
}

// ParseDate parses s with a Unicode (NSDateFormatter/ICU) date pattern such as "MMM d, yyyy".
// tz is an IANA name, "current", or empty (local time); locale picks month names.
func ParseDate(s, format, locale, tz string) (time.Time, error) {
	loc := time.Local
	if tz != "" && tz != "current" {
		l, err := time.LoadLocation(tz)
		if err != nil {
			return time.Time{}, err
		}
		loc = l
	}
	lang := strings.ToLower(locale)
	if i := strings.IndexAny(lang, "_-"); i >= 0 {
		lang = lang[:i]
	}
	months, ok := monthNames[lang]
	if !ok {
		months = monthNames["en"]
	}

	year, month, day, hour, min, sec, nsec := 2000, 1, 1, 0, 0, 0, 0
	pm, has12 := false, false
	var zone *time.Location
	in := []rune(s)
	pat := []rune(format)
	pos := 0

	digits := func(max int) (int, bool) {
		n, i := 0, 0
		for ; i < max && pos < len(in) && in[pos] >= '0' && in[pos] <= '9'; i++ {
			n = n*10 + int(in[pos]-'0')
			pos++
		}
		return n, i > 0
	}
	word := func(opts string) bool {
		rest := strings.ToLower(string(in[pos:]))
		for _, o := range strings.Split(opts, "|") {
			if strings.HasPrefix(rest, o) {
				pos += len([]rune(o))
				if pos < len(in) && in[pos] == '.' { // "Sept." style abbreviations
					pos++
				}
				return true
			}
		}
		return false
	}

	for i := 0; i < len(pat); {
		c := pat[i]
		if c == '\'' { // quoted literal; '' is a literal quote
			j := i + 1
			var lit []rune
			for ; j < len(pat); j++ {
				if pat[j] == '\'' {
					if j+1 < len(pat) && pat[j+1] == '\'' {
						lit = append(lit, '\'')
						j++
						continue
					}
					break
				}
				lit = append(lit, pat[j])
			}
			if len(lit) == 0 && j == i+1 {
				lit = []rune{'\''}
			}
			for _, r := range lit {
				if pos >= len(in) || unicode.ToLower(in[pos]) != unicode.ToLower(r) {
					return time.Time{}, errDate
				}
				pos++
			}
			i = j + 1
			continue
		}
		n := 1
		for i+n < len(pat) && pat[i+n] == c {
			n++
		}
		i += n
		var ok bool
		switch c {
		case 'y', 'u':
			var v int
			if v, ok = digits(max(n, 4)); ok && n == 2 {
				v += 2000
			}
			year = v
		case 'M', 'L':
			if n >= 3 {
				ok = false
				for mi, names := range months {
					if word(names) {
						month, ok = mi+1, true
						break
					}
				}
			} else {
				month, ok = digits(2)
			}
		case 'd':
			day, ok = digits(2)
		case 'H', 'k':
			hour, ok = digits(2)
		case 'h', 'K':
			hour, ok = digits(2)
			has12 = true
		case 'm':
			min, ok = digits(2)
		case 's':
			sec, ok = digits(2)
		case 'S':
			start := pos
			var v int
			v, ok = digits(9)
			for k := pos - start; k < 9; k++ {
				v *= 10
			}
			nsec = v
		case 'a':
			if ok = word("am|a.m."); !ok {
				ok = word("pm|p.m.")
				pm = ok
			}
		case 'E':
			start := pos
			for pos < len(in) && unicode.IsLetter(in[pos]) {
				pos++
			}
			if pos < len(in) && in[pos] == '.' {
				pos++
			}
			ok = pos > start
		case 'Z', 'X', 'x', 'z', 'O', 'v', 'V':
			zone, ok = parseZone(in, &pos)
		default:
			if unicode.IsLetter(c) && c != 'T' {
				return time.Time{}, errors.New("unsupported date pattern letter: " + string(c))
			}
			ok = true
			for k := 0; k < n && ok; k++ {
				switch {
				case unicode.IsSpace(c): // any run of whitespace matches
					for pos < len(in) && unicode.IsSpace(in[pos]) {
						pos++
					}
				case pos < len(in) && in[pos] == c:
					pos++
				default:
					ok = false
				}
			}
		}
		if !ok {
			return time.Time{}, errDate
		}
	}
	if pos != len(in) {
		return time.Time{}, errDate
	}
	if has12 {
		hour %= 12
		if pm {
			hour += 12
		}
	}
	if zone != nil {
		loc = zone
	}
	if month < 1 || month > 12 || day < 1 || day > 31 || hour > 23 || min > 59 || sec > 60 {
		return time.Time{}, errDate
	}
	return time.Date(year, time.Month(month), day, hour, min, sec, nsec, loc), nil
}

// parseZone accepts "Z", "UTC"/"GMT" with an optional offset, and ±HH, ±HHMM, ±HH:MM.
func parseZone(in []rune, pos *int) (*time.Location, bool) {
	rest := string(in[*pos:])
	if strings.HasPrefix(rest, "Z") {
		*pos++
		return time.UTC, true
	}
	for _, p := range []string{"UTC", "GMT"} {
		if strings.HasPrefix(rest, p) {
			*pos += len(p)
			rest = rest[len(p):]
			if rest == "" || (rest[0] != '+' && rest[0] != '-') {
				return time.UTC, true
			}
		}
	}
	if rest == "" || (rest[0] != '+' && rest[0] != '-') {
		return nil, false
	}
	sign := 1
	if rest[0] == '-' {
		sign = -1
	}
	*pos++
	num := func() (int, bool) {
		if *pos+2 > len(in) || !unicode.IsDigit(in[*pos]) || !unicode.IsDigit(in[*pos+1]) {
			return 0, false
		}
		v := int(in[*pos]-'0')*10 + int(in[*pos+1]-'0')
		*pos += 2
		return v, true
	}
	hh, ok := num()
	if !ok {
		return nil, false
	}
	if *pos < len(in) && in[*pos] == ':' {
		*pos++
	}
	mm, _ := num()
	return time.FixedZone("", sign*(hh*3600+mm*60)), true
}
