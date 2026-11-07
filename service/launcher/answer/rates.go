package answer

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/stubbedev/lys-lib/atomicfile"
)

// FeedURL is the ECB's daily euro foreign exchange reference rates.
const FeedURL = "https://www.ecb.europa.eu/stats/eurofxref/eurofxref-daily.xml"

const (
	maxFeedBytes      = 1 << 20
	retryAfterFailure = time.Hour
	fetchTimeout      = 10 * time.Second
)

// RateTable is one day of reference rates, each currency's units per euro.
type RateTable struct {
	// Date is the reference day, midnight UTC.
	Date   time.Time
	perEUR [currencyCount]*big.Rat
}

type ecbEnvelope struct {
	Day struct {
		Time  string `xml:"time,attr"`
		Rates []struct {
			Currency string `xml:"currency,attr"`
			Rate     string `xml:"rate,attr"`
		} `xml:"Cube"`
	} `xml:"Cube>Cube"`
}

// ParseECB reads the ECB's eurofxref XML. Currencies it does not know are
// left out; a missing day or a rate that is not a positive decimal fails.
func ParseECB(data []byte) (*RateTable, error) {
	var env ecbEnvelope
	if err := xml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("parse ECB rates: %w", err)
	}
	day, err := time.Parse(time.DateOnly, env.Day.Time)
	if err != nil {
		return nil, fmt.Errorf("parse ECB rates: reference day %q: %w", env.Day.Time, err)
	}
	t := &RateTable{Date: day}
	t.perEUR[EUR] = big.NewRat(1, 1)
	known := 0
	for _, r := range env.Day.Rates {
		c, ok := codeCurrency(r.Currency)
		if !ok {
			continue
		}
		rate, ok := new(big.Rat).SetString(r.Rate)
		if !ok || rate.Sign() <= 0 || decimalPrefix(r.Rate) != len(r.Rate) {
			return nil, fmt.Errorf("parse ECB rates: %s rate %q is not a positive decimal", r.Currency, r.Rate)
		}
		t.perEUR[c] = rate
		known++
	}
	if known == 0 {
		return nil, fmt.Errorf("parse ECB rates: no known currency on %s", env.Day.Time)
	}
	return t, nil
}

func codeCurrency(code string) (Currency, bool) {
	for c, known := range currencyCodes {
		if known == code {
			return Currency(c), true
		}
	}
	return 0, false
}

// Convert turns amount, a plain decimal, from one currency into another,
// exactly; false when the table has no rate for either.
func (t *RateTable) Convert(amount string, from, to Currency) (*big.Rat, bool) {
	fromRate, toRate := t.perEUR[from], t.perEUR[to]
	if fromRate == nil || toRate == nil {
		return nil, false
	}
	v, ok := new(big.Rat).SetString(amount)
	if !ok {
		return nil, false
	}
	v.Mul(v, toRate)
	return v.Quo(v, fromRate), true
}

// FormatAmount writes an amount with two decimals, or with as many as it
// takes (up to six) to show a non-zero amount below a cent.
func FormatAmount(v *big.Rat) string {
	s := v.FloatString(2)
	if v.Sign() == 0 || strings.Trim(s, "-0.") != "" {
		return s
	}
	s = v.FloatString(6)
	return strings.TrimRight(s, "0")
}

// Snapshot is the rates in use and when they were fetched.
type Snapshot struct {
	Table     *RateTable
	FetchedAt time.Time
}

// Fresh reports whether the rates were fetched on now's calendar day.
func (s Snapshot) Fresh(now time.Time) bool { return sameDay(s.FetchedAt, now) }

// AgeDays is how many calendar days the reference day lies before now's.
func (s Snapshot) AgeDays(now time.Time) int {
	return daysBetween(s.Table.Date, civil(now))
}

// Rates keeps the reference rates: read from the cache file, fetched from
// the feed at most once per calendar day, retried an hour after a failure.
// Snapshot never waits; Refresh does the I/O on its caller's goroutine.
type Rates struct {
	feed, cache string
	client      *http.Client
	now         func() time.Time

	snap       atomic.Pointer[Snapshot]
	refreshing sync.Mutex
	cacheRead  bool
	lastTry    time.Time
}

// NewRates reads feed, caching it at cache.
func NewRates(feed, cache string, now func() time.Time) *Rates {
	return &Rates{feed: feed, cache: cache, client: &http.Client{Timeout: fetchTimeout}, now: now}
}

// Snapshot is the current rates, false before any are known.
func (r *Rates) Snapshot() (Snapshot, bool) {
	s := r.snap.Load()
	if s == nil {
		return Snapshot{}, false
	}
	return *s, true
}

// Refresh reads the cache on first use and fetches the feed when due; true
// when the rates changed. A refresh already running elsewhere makes it
// return at once.
func (r *Rates) Refresh(ctx context.Context) bool {
	if !r.refreshing.TryLock() {
		return false
	}
	defer r.refreshing.Unlock()
	changed := false
	if !r.cacheRead {
		r.cacheRead = true
		changed = r.readCache()
	}
	now := r.now()
	if s := r.snap.Load(); s != nil && s.Fresh(now) {
		return changed
	}
	if !r.lastTry.IsZero() && now.Sub(r.lastTry) < retryAfterFailure {
		return changed
	}
	r.lastTry = now
	data, err := r.fetch(ctx)
	if err != nil {
		log.Printf("launcher: currency rates: %v", err)
		return changed
	}
	table, err := ParseECB(data)
	if err != nil {
		log.Printf("launcher: currency rates from %s: %v", r.feed, err)
		return changed
	}
	r.snap.Store(&Snapshot{Table: table, FetchedAt: now})
	if err := r.writeCache(data, now); err != nil {
		log.Printf("launcher: currency rates: %v", err)
	}
	return true
}

func (r *Rates) fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.feed, nil)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", r.feed, err)
	}
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", r.feed, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", r.feed, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxFeedBytes))
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", r.feed, err)
	}
	return data, nil
}

func (r *Rates) readCache() bool {
	f, err := os.Open(r.cache)
	if errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if err != nil {
		log.Printf("launcher: currency rates cache: %v", err)
		return false
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		log.Printf("launcher: currency rates cache: %v", err)
		return false
	}
	data, err := io.ReadAll(io.LimitReader(f, maxFeedBytes))
	if err != nil {
		log.Printf("launcher: currency rates cache %s: %v", r.cache, err)
		return false
	}
	table, err := ParseECB(data)
	if err != nil {
		log.Printf("launcher: currency rates cache %s: %v", r.cache, err)
		return false
	}
	r.snap.Store(&Snapshot{Table: table, FetchedAt: info.ModTime()})
	return true
}

func (r *Rates) writeCache(data []byte, fetched time.Time) error {
	if err := os.MkdirAll(filepath.Dir(r.cache), 0o700); err != nil {
		return fmt.Errorf("cache directory for %s: %w", r.cache, err)
	}
	if err := atomicfile.WriteFile(r.cache, data, 0o600); err != nil {
		return fmt.Errorf("write cache %s: %w", r.cache, err)
	}
	if err := os.Chtimes(r.cache, fetched, fetched); err != nil {
		return fmt.Errorf("stamp cache %s: %w", r.cache, err)
	}
	return nil
}
