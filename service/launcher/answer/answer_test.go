package answer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

const feedFixture = `<?xml version="1.0" encoding="UTF-8"?>
<gesmes:Envelope xmlns:gesmes="http://www.gesmes.org/xml/2002-08-01" xmlns="http://www.ecb.int/vocabulary/2002-08-01/eurofxref">
	<gesmes:subject>Reference rates</gesmes:subject>
	<gesmes:Sender><gesmes:name>European Central Bank</gesmes:name></gesmes:Sender>
	<Cube>
		<Cube time='2026-11-06'>
			<Cube currency='USD' rate='1.0825'/>
			<Cube currency='JPY' rate='162.31'/>
			<Cube currency='DKK' rate='7.4593'/>
			<Cube currency='GBP' rate='0.8412'/>
			<Cube currency='IDR' rate='17000'/>
			<Cube currency='XXX' rate='3.5'/>
		</Cube>
	</Cube>
</gesmes:Envelope>`

func TestCurrencyParse(t *testing.T) {
	en, da := NewCurrencyParser("en"), NewCurrencyParser("da")
	for _, tc := range []struct {
		p    CurrencyParser
		q    string
		want CurrencyQuery
	}{
		{en, "100 usd in eur", CurrencyQuery{"100", USD, EUR}},
		{en, "100USD to EUR", CurrencyQuery{"100", USD, EUR}},
		{en, "$100 to €", CurrencyQuery{"100", USD, EUR}},
		{en, "12.5 dollars in euros", CurrencyQuery{"12.5", USD, EUR}},
		{en, "£3 as yen", CurrencyQuery{"3", GBP, JPY}},
		{da, "250 kr in usd", CurrencyQuery{"250", DKK, USD}},
		{NewCurrencyParser("sv"), "250 kr in usd", CurrencyQuery{"250", SEK, USD}},
	} {
		got, ok := tc.p.Parse(tc.q)
		if !ok || got != tc.want {
			t.Errorf("%q = %+v, %v; want %+v", tc.q, got, ok, tc.want)
		}
	}
	for _, q := range []string{
		"100 usd", "usd in eur", "100 km in mi", "100 usd in km", "1.2.3 usd in eur", "250 kr in usd",
		"100 usd in", "5 pounds in kg", "15:00 PST in Copenhagen", "", "firefox",
	} {
		if got, ok := en.Parse(q); ok {
			t.Errorf("%q parsed as %+v", q, got)
		}
	}
}

func TestParseECBAndConvertExactly(t *testing.T) {
	table, err := ParseECB([]byte(feedFixture))
	if err != nil {
		t.Fatal(err)
	}
	if got := table.Date.Format(time.DateOnly); got != "2026-11-06" {
		t.Errorf("date = %s", got)
	}
	for _, tc := range []struct {
		amount   string
		from, to Currency
		want     string
	}{
		{"100", USD, EUR, "92.38"},
		{"100", EUR, USD, "108.25"},
		{"1", USD, DKK, "6.89"},
		{"0.1", GBP, GBP, "0.10"},
		{"1", IDR, EUR, "0.000059"},
		{"1000000.01", EUR, JPY, "162310001.62"},
	} {
		v, ok := table.Convert(tc.amount, tc.from, tc.to)
		if !ok {
			t.Fatalf("%s %s->%s: no rate", tc.amount, tc.from, tc.to)
		}
		if got := FormatAmount(v); got != tc.want {
			t.Errorf("%s %s->%s = %s, want %s", tc.amount, tc.from, tc.to, got, tc.want)
		}
	}
	if _, ok := table.Convert("1", USD, CHF); ok {
		t.Error("a currency missing from the feed converted")
	}
	for _, bad := range []string{
		"<Envelope><Cube><Cube time='2026-11-06'></Cube></Cube></Envelope>",
		"<Envelope><Cube><Cube time='yesterday'><Cube currency='USD' rate='1'/></Cube></Cube></Envelope>",
		"<Envelope><Cube><Cube time='2026-11-06'><Cube currency='USD' rate='-1'/></Cube></Cube></Envelope>",
		"<Envelope><Cube><Cube time='2026-11-06'><Cube currency='USD' rate='1/3'/></Cube></Cube></Envelope>",
		"not xml",
	} {
		if _, err := ParseECB([]byte(bad)); err == nil {
			t.Errorf("%q parsed", bad)
		}
	}
}

type feed struct {
	srv      *httptest.Server
	requests atomic.Int32
	down     atomic.Bool
}

func newFeed(t *testing.T) *feed {
	f := &feed{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f.requests.Add(1)
		if f.down.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(feedFixture))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

type clock struct{ now time.Time }

func (c *clock) Now() time.Time { return c.now }

func TestRatesFetchAtMostOncePerDay(t *testing.T) {
	f := newFeed(t)
	cph, err := time.LoadLocation("Europe/Copenhagen")
	if err != nil {
		t.Fatal(err)
	}
	c := &clock{now: time.Date(2026, 11, 7, 0, 30, 0, 0, cph)}
	cache := filepath.Join(t.TempDir(), "lys-shell", "fx.xml")
	r := NewRates(f.srv.URL, cache, c.Now)
	if _, ok := r.Snapshot(); ok {
		t.Fatal("rates before any refresh")
	}
	if !r.Refresh(context.Background()) {
		t.Fatal("the first refresh changed nothing")
	}
	for range 50 {
		c.now = c.now.Add(20 * time.Minute)
		if c.now.Day() != 7 {
			break
		}
		r.Refresh(context.Background())
		reopened := NewRates(f.srv.URL, cache, c.Now)
		reopened.Refresh(context.Background())
		if _, ok := reopened.Snapshot(); !ok {
			t.Fatal("a new launcher did not read the cache")
		}
	}
	if n := f.requests.Load(); n != 1 {
		t.Fatalf("requests on one day = %d, want 1", n)
	}
	info, err := os.Stat(cache)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("cache mode = %v", info.Mode())
	}
	c.now = time.Date(2026, 11, 8, 9, 0, 0, 0, cph)
	r.Refresh(context.Background())
	if n := f.requests.Load(); n != 2 {
		t.Fatalf("requests after the day turned = %d, want 2", n)
	}
	snap, _ := r.Snapshot()
	if !snap.Fresh(c.now) || snap.AgeDays(c.now) != 2 {
		t.Errorf("fresh = %v, age = %d", snap.Fresh(c.now), snap.AgeDays(c.now))
	}
}

func TestRatesOfflineUseTheCacheOrNothing(t *testing.T) {
	f := newFeed(t)
	f.down.Store(true)
	c := &clock{now: time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC)}
	dir := t.TempDir()
	r := NewRates(f.srv.URL, filepath.Join(dir, "fx.xml"), c.Now)
	if r.Refresh(context.Background()) {
		t.Error("a failed fetch changed the rates")
	}
	if _, ok := r.Snapshot(); ok {
		t.Fatal("rates without network or cache")
	}
	c.now = c.now.Add(10 * time.Minute)
	r.Refresh(context.Background())
	if n := f.requests.Load(); n != 1 {
		t.Errorf("retried %d times within the hour", n-1)
	}
	c.now = c.now.Add(time.Hour)
	r.Refresh(context.Background())
	if n := f.requests.Load(); n != 2 {
		t.Errorf("requests after an hour = %d, want 2", n)
	}

	cache := filepath.Join(dir, "cached.xml")
	if err := os.WriteFile(cache, []byte(feedFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	fetched := time.Date(2026, 11, 6, 17, 0, 0, 0, time.UTC)
	if err := os.Chtimes(cache, fetched, fetched); err != nil {
		t.Fatal(err)
	}
	offline := NewRates(f.srv.URL, cache, c.Now)
	if !offline.Refresh(context.Background()) {
		t.Fatal("reading the cache changed nothing")
	}
	snap, ok := offline.Snapshot()
	if !ok {
		t.Fatal("the cache was not used offline")
	}
	if snap.Fresh(c.now) || snap.AgeDays(c.now) != 3 {
		t.Errorf("fresh = %v, age = %d; want stale and 3 days old", snap.Fresh(c.now), snap.AgeDays(c.now))
	}
}

func TestRatesFetchHonoursCancellation(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { <-block }))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(block) })
	r := NewRates(srv.URL, filepath.Join(t.TempDir(), "fx.xml"), time.Now)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool)
	go func() { done <- r.Refresh(ctx) }()
	cancel()
	select {
	case changed := <-done:
		if changed {
			t.Error("a cancelled fetch changed the rates")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch outlived its context")
	}
}

func mustZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestTimeAcrossZonesAndDST(t *testing.T) {
	cph := mustZone(t, "Europe/Copenhagen")
	z := NewZones(time.LoadLocation)
	for _, tc := range []struct {
		q      string
		now    time.Time
		want   string
		offset int
		place  string
	}{
		{"15:00 PST in Copenhagen", time.Date(2026, 7, 1, 9, 0, 0, 0, cph), "2026-07-01 01:00 CEST", 1, "Copenhagen"},
		{"15:00 PST in Copenhagen", time.Date(2026, 12, 1, 9, 0, 0, 0, cph), "2026-12-02 00:00 CET", 1, "Copenhagen"},
		{"3pm los angeles to copenhagen", time.Date(2026, 10, 28, 9, 0, 0, 0, cph), "2026-10-28 23:00 CET", 0, "Copenhagen"},
		{"3pm los angeles to copenhagen", time.Date(2026, 7, 1, 9, 0, 0, 0, cph), "2026-07-02 00:00 CEST", 1, "Copenhagen"},
		{"9:30 am tokyo in new york", time.Date(2026, 3, 9, 12, 0, 0, 0, cph), "2026-03-08 20:30 EDT", -1, "New York"},
		{"12am UTC in Europe/Copenhagen", time.Date(2026, 1, 1, 12, 0, 0, 0, cph), "2026-01-01 01:00 CET", 0, "Europe/Copenhagen"},
		{"08:00 in PT", time.Date(2026, 7, 1, 12, 0, 0, 0, cph), "2026-06-30 23:00 PDT", -1, "PT"},
		{"time in Tokyo", time.Date(2026, 3, 29, 0, 30, 0, 0, cph), "2026-03-29 08:30 JST", 0, "Tokyo"},
		{"time in Tokyo", time.Date(2026, 3, 29, 3, 30, 0, 0, cph), "2026-03-29 10:30 JST", 0, "Tokyo"},
		{"now in london", time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC), "2026-10-25 01:30 GMT", 0, "London"},
	} {
		q, ok := ParseTime(tc.q)
		if !ok {
			t.Errorf("%q did not parse", tc.q)
			continue
		}
		got, ok := z.Eval(q, tc.now)
		if !ok {
			t.Errorf("%q did not evaluate", tc.q)
			continue
		}
		if s := got.At.Format("2006-01-02 15:04 MST"); s != tc.want || got.DayOffset != tc.offset || got.Place != tc.place {
			t.Errorf("%q at %v = %s %+d %q, want %s %+d %q", tc.q, tc.now, s, got.DayOffset, got.Place, tc.want, tc.offset, tc.place)
		}
	}
	for _, q := range []string{
		"15 in tokyo", "25:00 in tokyo", "13pm in tokyo", "15:00 in atlantis", "15:00 atlantis in tokyo",
		"time in", "100 usd in eur", "5 km to mi", "15:0 in tokyo", "firefox",
	} {
		if got, ok := ParseTime(q); ok {
			t.Errorf("%q parsed as %+v", q, got)
		}
	}
	q, ok := ParseTime("15:00 in Nowhere/Atlantis")
	if !ok {
		t.Fatal("an IANA-shaped name did not parse")
	}
	if got, ok := z.Eval(q, time.Now()); ok {
		t.Errorf("an unknown zone evaluated to %v", got)
	}
}

func TestDatesAtMonthAndYearEnds(t *testing.T) {
	at := func(y int, m time.Month, d int) time.Time {
		return time.Date(y, m, d, 23, 30, 0, 0, mustZone(t, "Europe/Copenhagen"))
	}
	day := func(y int, m time.Month, d int) DateAnswer {
		return DateAnswer{Kind: DateIsDay, Day: time.Date(y, m, d, 0, 0, 0, 0, time.UTC)}
	}
	span := func(n int) DateAnswer { return DateAnswer{Kind: DateIsSpan, Days: n} }
	for _, tc := range []struct {
		q    string
		now  time.Time
		want DateAnswer
	}{
		{"today + 90 days", at(2026, 11, 7), day(2027, 2, 5)},
		{"today + 1 day", at(2026, 12, 31), day(2027, 1, 1)},
		{"today+1d", at(2028, 2, 28), day(2028, 2, 29)},
		{"today - 1 day", at(2027, 3, 1), day(2027, 2, 28)},
		{"today + 1 month", at(2027, 1, 31), day(2027, 2, 28)},
		{"today + 1 month", at(2028, 1, 31), day(2028, 2, 29)},
		{"today - 1 month", at(2027, 3, 31), day(2027, 2, 28)},
		{"today + 1 year", at(2028, 2, 29), day(2029, 2, 28)},
		{"today - 13 months", at(2027, 1, 15), day(2025, 12, 15)},
		{"tomorrow + 2 weeks", at(2026, 12, 31), day(2027, 1, 15)},
		{"2026-12-24 + 10 days", at(2026, 1, 1), day(2027, 1, 3)},
		{"3 weeks from tomorrow", at(2026, 12, 20), day(2027, 1, 11)},
		{"10 days ago", at(2027, 1, 5), day(2026, 12, 26)},
		{"days until 2027-01-01", at(2026, 12, 31), span(1)},
		{"days until 2027-01-01", at(2026, 1, 1), span(365)},
		{"days until 2029-01-01", at(2028, 1, 1), span(366)},
		{"days since 2026-02-28", at(2026, 3, 1), span(1)},
		{"Days Till tomorrow", at(2026, 11, 30), span(1)},
		{"2026-12-24 - today", at(2026, 11, 30), span(24)},
		{"yesterday - 2027-01-01", at(2027, 1, 1), span(-1)},
	} {
		got, ok := EvalDate(tc.q, tc.now)
		if !ok || got.Kind != tc.want.Kind || got.Days != tc.want.Days || !got.Day.Equal(tc.want.Day) {
			t.Errorf("%q on %v = %+v, %v; want %+v", tc.q, tc.now, got, ok, tc.want)
		}
	}
	for _, q := range []string{
		"today", "today + days", "today + 3 fortnights", "days until christmas", "2026-13-01 + 1 day",
		"2 - 1", "5 km to mi", "today + 1 day + 1 day + 1", "20261224 - today", "hello world", "",
	} {
		if got, ok := EvalDate(q, at(2026, 11, 7)); ok {
			t.Errorf("%q answered %+v", q, got)
		}
	}
}

func TestParsersAllocateNothingOnOtherQueries(t *testing.T) {
	p := NewCurrencyParser("da")
	now := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)
	for _, q := range []string{
		"firefox", "2+2", "5 km to mi", "open the pod bay doors in space", "sqrt(16)", "weather in paris tomorrow",
		"Thunderbird Mail", "100 usd", "days of thunder", "to be or not to be", "hello - world",
	} {
		allocs := testing.AllocsPerRun(100, func() {
			_, _ = p.Parse(q)
			_, _ = ParseTime(q)
			_, _ = EvalDate(q, now)
		})
		if allocs != 0 {
			t.Errorf("%q: %v allocations per query", q, allocs)
		}
	}
}
