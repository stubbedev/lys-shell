package answer

import (
	"strings"
	"sync"
	"time"
)

type city struct{ name, zone string }

var cityTable = [...]city{
	{"Abu Dhabi", "Asia/Dubai"},
	{"Accra", "Africa/Accra"},
	{"Addis Ababa", "Africa/Addis_Ababa"},
	{"Amsterdam", "Europe/Amsterdam"},
	{"Anchorage", "America/Anchorage"},
	{"Athens", "Europe/Athens"},
	{"Atlanta", "America/New_York"},
	{"Auckland", "Pacific/Auckland"},
	{"Austin", "America/Chicago"},
	{"Bangkok", "Asia/Bangkok"},
	{"Barcelona", "Europe/Madrid"},
	{"Beijing", "Asia/Shanghai"},
	{"Berlin", "Europe/Berlin"},
	{"Bogota", "America/Bogota"},
	{"Boston", "America/New_York"},
	{"Brisbane", "Australia/Brisbane"},
	{"Brussels", "Europe/Brussels"},
	{"Bucharest", "Europe/Bucharest"},
	{"Budapest", "Europe/Budapest"},
	{"Buenos Aires", "America/Argentina/Buenos_Aires"},
	{"Cairo", "Africa/Cairo"},
	{"Calgary", "America/Edmonton"},
	{"Cape Town", "Africa/Johannesburg"},
	{"Caracas", "America/Caracas"},
	{"Chicago", "America/Chicago"},
	{"Copenhagen", "Europe/Copenhagen"},
	{"Dallas", "America/Chicago"},
	{"Delhi", "Asia/Kolkata"},
	{"Denver", "America/Denver"},
	{"Dhaka", "Asia/Dhaka"},
	{"Doha", "Asia/Qatar"},
	{"Dubai", "Asia/Dubai"},
	{"Dublin", "Europe/Dublin"},
	{"Edinburgh", "Europe/London"},
	{"Frankfurt", "Europe/Berlin"},
	{"Geneva", "Europe/Zurich"},
	{"Hamburg", "Europe/Berlin"},
	{"Hanoi", "Asia/Bangkok"},
	{"Helsinki", "Europe/Helsinki"},
	{"Ho Chi Minh City", "Asia/Ho_Chi_Minh"},
	{"Hong Kong", "Asia/Hong_Kong"},
	{"Honolulu", "Pacific/Honolulu"},
	{"Houston", "America/Chicago"},
	{"Istanbul", "Europe/Istanbul"},
	{"Jakarta", "Asia/Jakarta"},
	{"Jerusalem", "Asia/Jerusalem"},
	{"Johannesburg", "Africa/Johannesburg"},
	{"Karachi", "Asia/Karachi"},
	{"Kathmandu", "Asia/Kathmandu"},
	{"Kyiv", "Europe/Kyiv"},
	{"Kiev", "Europe/Kyiv"},
	{"Kolkata", "Asia/Kolkata"},
	{"Kuala Lumpur", "Asia/Kuala_Lumpur"},
	{"Lagos", "Africa/Lagos"},
	{"Las Vegas", "America/Los_Angeles"},
	{"Lima", "America/Lima"},
	{"Lisbon", "Europe/Lisbon"},
	{"London", "Europe/London"},
	{"Los Angeles", "America/Los_Angeles"},
	{"Madrid", "Europe/Madrid"},
	{"Manila", "Asia/Manila"},
	{"Melbourne", "Australia/Melbourne"},
	{"Mexico City", "America/Mexico_City"},
	{"Miami", "America/New_York"},
	{"Milan", "Europe/Rome"},
	{"Montreal", "America/Toronto"},
	{"Moscow", "Europe/Moscow"},
	{"Mumbai", "Asia/Kolkata"},
	{"Munich", "Europe/Berlin"},
	{"Nairobi", "Africa/Nairobi"},
	{"New Delhi", "Asia/Kolkata"},
	{"New York", "America/New_York"},
	{"Oslo", "Europe/Oslo"},
	{"Paris", "Europe/Paris"},
	{"Perth", "Australia/Perth"},
	{"Phoenix", "America/Phoenix"},
	{"Prague", "Europe/Prague"},
	{"Reykjavik", "Atlantic/Reykjavik"},
	{"Riga", "Europe/Riga"},
	{"Rio de Janeiro", "America/Sao_Paulo"},
	{"Riyadh", "Asia/Riyadh"},
	{"Rome", "Europe/Rome"},
	{"San Francisco", "America/Los_Angeles"},
	{"Santiago", "America/Santiago"},
	{"Sao Paulo", "America/Sao_Paulo"},
	{"São Paulo", "America/Sao_Paulo"},
	{"Seattle", "America/Los_Angeles"},
	{"Seoul", "Asia/Seoul"},
	{"Shanghai", "Asia/Shanghai"},
	{"Shenzhen", "Asia/Shanghai"},
	{"Singapore", "Asia/Singapore"},
	{"Stockholm", "Europe/Stockholm"},
	{"Sydney", "Australia/Sydney"},
	{"Taipei", "Asia/Taipei"},
	{"Tallinn", "Europe/Tallinn"},
	{"Tehran", "Asia/Tehran"},
	{"Tel Aviv", "Asia/Jerusalem"},
	{"Tokyo", "Asia/Tokyo"},
	{"Toronto", "America/Toronto"},
	{"Vancouver", "America/Vancouver"},
	{"Vienna", "Europe/Vienna"},
	{"Vilnius", "Europe/Vilnius"},
	{"Warsaw", "Europe/Warsaw"},
	{"Washington", "America/New_York"},
	{"Wellington", "Pacific/Auckland"},
	{"Zurich", "Europe/Zurich"},
}

type abbreviation struct {
	name   string
	offset int
}

var abbreviationTable = [...]abbreviation{
	{"UTC", 0},
	{"GMT", 0},
	{"PST", -8 * 3600},
	{"PDT", -7 * 3600},
	{"MST", -7 * 3600},
	{"MDT", -6 * 3600},
	{"CST", -6 * 3600},
	{"CDT", -5 * 3600},
	{"EST", -5 * 3600},
	{"EDT", -4 * 3600},
	{"AKST", -9 * 3600},
	{"AKDT", -8 * 3600},
	{"HST", -10 * 3600},
	{"WET", 0},
	{"WEST", 3600},
	{"BST", 3600},
	{"CET", 3600},
	{"CEST", 2 * 3600},
	{"EET", 2 * 3600},
	{"EEST", 3 * 3600},
	{"MSK", 3 * 3600},
	{"IST", 5*3600 + 1800},
	{"SGT", 8 * 3600},
	{"HKT", 8 * 3600},
	{"AWST", 8 * 3600},
	{"JST", 9 * 3600},
	{"KST", 9 * 3600},
	{"ACST", 9*3600 + 1800},
	{"AEST", 10 * 3600},
	{"AEDT", 11 * 3600},
	{"NZST", 12 * 3600},
	{"NZDT", 13 * 3600},
}

var genericZones = map[string]string{
	"pt": "America/Los_Angeles", "mt": "America/Denver", "ct": "America/Chicago", "et": "America/New_York",
}

const maxZoneName = 32

var (
	zoneTablesOnce sync.Once
	citiesByName   map[string]city
	fixedZones     map[string]*time.Location
)

func zoneTables() {
	zoneTablesOnce.Do(func() {
		citiesByName = make(map[string]city, len(cityTable))
		var buf [maxZoneName]byte
		for _, c := range cityTable {
			citiesByName[string(foldZone(&buf, c.name))] = c
		}
		fixedZones = make(map[string]*time.Location, len(abbreviationTable))
		for _, a := range abbreviationTable {
			fixedZones[string(foldZone(&buf, a.name))] = time.FixedZone(a.name, a.offset)
		}
	})
}

// foldZone lowers s's ASCII letters into buf and collapses its runs of
// spaces; nil when it does not fit.
func foldZone(buf *[maxZoneName]byte, s string) []byte {
	n := 0
	space := false
	for i := range len(s) {
		b := s[i]
		if b == ' ' || b == '\t' {
			space = n > 0
			continue
		}
		if space {
			if n == len(buf) {
				return nil
			}
			buf[n] = ' '
			n++
			space = false
		}
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		if n == len(buf) {
			return nil
		}
		buf[n] = b
		n++
	}
	return buf[:n]
}

// zoneKind tells how a spelling names a zone.
type zoneKind uint8

const (
	zoneLocal zoneKind = iota + 1
	zoneFixed
	zoneNamed
)

// zoneRef is a recognised zone spelling, not yet loaded.
type zoneRef struct {
	kind  zoneKind
	fixed *time.Location
	name  string
	iana  string
}

// lookupZone reads a zone spelling: an abbreviation (a fixed offset: PST
// is UTC-8 in July too), a generic one (PT follows Los Angeles), a city, or
// an IANA name.
func lookupZone(s string) (zoneRef, bool) {
	zoneTables()
	var buf [maxZoneName]byte
	key := foldZone(&buf, s)
	if len(key) == 0 {
		return zoneRef{}, false
	}
	if string(key) == "local" || string(key) == "here" {
		return zoneRef{kind: zoneLocal}, true
	}
	if loc, ok := fixedZones[string(key)]; ok {
		return zoneRef{kind: zoneFixed, fixed: loc, name: loc.String()}, true
	}
	if iana, ok := genericZones[string(key)]; ok {
		return zoneRef{kind: zoneNamed, name: strings.TrimSpace(s), iana: iana}, true
	}
	if c, ok := citiesByName[string(key)]; ok {
		return zoneRef{kind: zoneNamed, name: c.name, iana: c.zone}, true
	}
	if strings.IndexByte(s, '/') > 0 && strings.IndexByte(s, ' ') < 0 {
		return zoneRef{kind: zoneNamed, name: s, iana: s}, true
	}
	return zoneRef{}, false
}

// TimeQuery is a parsed "15:00 PST in Copenhagen" or "time in Tokyo".
type TimeQuery struct {
	current      bool
	hour, minute int
	from, to     zoneRef
}

var timeWords = [...]string{" in ", " to "}

// ParseTime reads "<time> [zone] in <zone>" and "time in <zone>". A time
// is "15:00", "3pm" or "3:30 pm"; a zone is an abbreviation, a city or an
// IANA name. A time without a zone is local.
func ParseTime(query string) (TimeQuery, bool) {
	q := strings.TrimSpace(query)
	for _, word := range timeWords {
		at := indexFold(q, word)
		if at < 0 {
			continue
		}
		to, ok := lookupZone(q[at+len(word):])
		if !ok {
			return TimeQuery{}, false
		}
		from := strings.TrimSpace(q[:at])
		if strings.EqualFold(from, "time") || strings.EqualFold(from, "now") {
			return TimeQuery{current: true, to: to}, true
		}
		hour, minute, rest, ok := clockPrefix(from)
		if !ok {
			return TimeQuery{}, false
		}
		src := zoneRef{kind: zoneLocal}
		if rest = strings.TrimSpace(rest); rest != "" {
			if src, ok = lookupZone(rest); !ok {
				return TimeQuery{}, false
			}
		}
		return TimeQuery{hour: hour, minute: minute, from: src, to: to}, true
	}
	return TimeQuery{}, false
}

// clockPrefix reads the time of day s starts with: "15:00", "3pm", "3:30
// pm", "12 am". A bare number is not a time.
func clockPrefix(s string) (hour, minute int, rest string, ok bool) {
	i := 0
	for i < len(s) && i < 2 && s[i] >= '0' && s[i] <= '9' {
		hour = hour*10 + int(s[i]-'0')
		i++
	}
	if i == 0 {
		return 0, 0, "", false
	}
	colon := false
	if i < len(s) && s[i] == ':' {
		if i+3 > len(s) || s[i+1] < '0' || s[i+1] > '9' || s[i+2] < '0' || s[i+2] > '9' {
			return 0, 0, "", false
		}
		minute = int(s[i+1]-'0')*10 + int(s[i+2]-'0')
		i += 3
		colon = true
	}
	j := i
	for j < len(s) && s[j] == ' ' {
		j++
	}
	if j+2 <= len(s) && (j+2 == len(s) || s[j+2] == ' ') {
		switch {
		case strings.EqualFold(s[j:j+2], "am"):
			if hour < 1 || hour > 12 || minute > 59 {
				return 0, 0, "", false
			}
			return hour % 12, minute, s[j+2:], true
		case strings.EqualFold(s[j:j+2], "pm"):
			if hour < 1 || hour > 12 || minute > 59 {
				return 0, 0, "", false
			}
			return hour%12 + 12, minute, s[j+2:], true
		}
	}
	if !colon || hour > 23 || minute > 59 || i < len(s) && s[i] != ' ' {
		return 0, 0, "", false
	}
	return hour, minute, s[i:], true
}

// Zones loads IANA zones once each.
type Zones struct {
	load  func(string) (*time.Location, error)
	mu    sync.Mutex
	cache map[string]*time.Location
}

// NewZones loads zones with load, time.LoadLocation in the shell.
func NewZones(load func(string) (*time.Location, error)) *Zones {
	return &Zones{load: load, cache: map[string]*time.Location{}}
}

func (z *Zones) location(ref zoneRef, local *time.Location) (*time.Location, bool) {
	switch ref.kind {
	case zoneLocal:
		return local, true
	case zoneFixed:
		return ref.fixed, true
	}
	z.mu.Lock()
	defer z.mu.Unlock()
	if loc, ok := z.cache[ref.iana]; ok {
		return loc, true
	}
	loc, err := z.load(ref.iana)
	if err != nil {
		return nil, false
	}
	z.cache[ref.iana] = loc
	return loc, true
}

// TimeAnswer is an evaluated time query: At in the target zone, named
// Place ("" for the local zone), DayOffset days after the source's day.
type TimeAnswer struct {
	At        time.Time
	Place     string
	DayOffset int
}

// Eval places the query's time, on today's date in its source zone, in its
// target zone; now's location is the local zone. False when a zone does
// not load.
func (z *Zones) Eval(q TimeQuery, now time.Time) (TimeAnswer, bool) {
	to, ok := z.location(q.to, now.Location())
	if !ok {
		return TimeAnswer{}, false
	}
	place := q.to.name
	if q.current {
		return TimeAnswer{At: now.In(to), Place: place}, true
	}
	from, ok := z.location(q.from, now.Location())
	if !ok {
		return TimeAnswer{}, false
	}
	y, m, d := now.In(from).Date()
	src := time.Date(y, m, d, q.hour, q.minute, 0, 0, from)
	at := src.In(to)
	return TimeAnswer{At: at, Place: place, DayOffset: daysBetween(civil(src), civil(at))}, true
}
