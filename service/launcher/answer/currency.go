// Package answer parses and evaluates the launcher's inline answers beyond
// arithmetic: currency conversions over the ECB's reference rates, times
// across time zones, and calendar date arithmetic.
package answer

import (
	"strings"
)

// Currency is one of the currencies the ECB publishes a reference rate for,
// plus the euro the rates are quoted against.
type Currency uint8

// The currencies, in the ECB feed's order after the euro.
const (
	EUR Currency = iota
	USD
	JPY
	CZK
	DKK
	GBP
	HUF
	PLN
	RON
	SEK
	CHF
	ISK
	NOK
	TRY
	AUD
	BRL
	CAD
	CNY
	HKD
	IDR
	ILS
	INR
	KRW
	MXN
	MYR
	NZD
	PHP
	SGD
	THB
	ZAR
	currencyCount
)

var currencyCodes = [currencyCount]string{
	EUR: "EUR", USD: "USD", JPY: "JPY", CZK: "CZK", DKK: "DKK", GBP: "GBP", HUF: "HUF", PLN: "PLN",
	RON: "RON", SEK: "SEK", CHF: "CHF", ISK: "ISK", NOK: "NOK", TRY: "TRY", AUD: "AUD", BRL: "BRL",
	CAD: "CAD", CNY: "CNY", HKD: "HKD", IDR: "IDR", ILS: "ILS", INR: "INR", KRW: "KRW", MXN: "MXN",
	MYR: "MYR", NZD: "NZD", PHP: "PHP", SGD: "SGD", THB: "THB", ZAR: "ZAR",
}

// String is the ISO 4217 code.
func (c Currency) String() string { return currencyCodes[c] }

var currencyNames = map[string]Currency{
	"$": USD, "us$": USD, "dollar": USD, "dollars": USD,
	"€": EUR, "euro": EUR, "euros": EUR,
	"£": GBP, "pound": GBP, "pounds": GBP, "sterling": GBP,
	"¥": JPY, "yen": JPY,
	"yuan": CNY, "renminbi": CNY, "rmb": CNY,
	"franc": CHF, "francs": CHF,
	"zł": PLN, "zloty": PLN, "złoty": PLN,
	"₹": INR, "rupee": INR, "rupees": INR,
	"₩": KRW, "won": KRW,
	"₺": TRY, "lira": TRY,
	"r$": BRL, "real": BRL, "reais": BRL,
	"rand": ZAR, "forint": HUF, "koruna": CZK, "kč": CZK,
	"peso": MXN, "pesos": MXN, "baht": THB, "฿": THB,
	"₪": ILS, "shekel": ILS, "shekels": ILS, "ringgit": MYR, "rupiah": IDR,
	"leu": RON, "lei": RON,
	"a$": AUD, "c$": CAD, "hk$": HKD, "nz$": NZD, "s$": SGD, "₱": PHP,
}

// maxCurrencyName is the longest spelling looked up, in bytes.
const maxCurrencyName = 16

// CurrencyParser reads currency conversions. Kroner is what "kr" names in
// the session's language (DKK in Danish); outside the Nordic languages
// "kr" is ambiguous and names nothing.
type CurrencyParser struct {
	kroner    Currency
	hasKroner bool
}

// NewCurrencyParser reads "kr" as the kroner of language, an ISO 639 code.
func NewCurrencyParser(language string) CurrencyParser {
	switch language {
	case "da":
		return CurrencyParser{kroner: DKK, hasKroner: true}
	case "sv":
		return CurrencyParser{kroner: SEK, hasKroner: true}
	case "nb", "nn", "no":
		return CurrencyParser{kroner: NOK, hasKroner: true}
	case "is":
		return CurrencyParser{kroner: ISK, hasKroner: true}
	}
	return CurrencyParser{}
}

// lookup reads one currency name, its code in any case or a symbol.
func (p CurrencyParser) lookup(s string) (Currency, bool) {
	if s == "" || len(s) > maxCurrencyName {
		return 0, false
	}
	var buf [maxCurrencyName]byte
	lower := foldInto(&buf, s)
	if len(lower) == 3 {
		for c, code := range currencyCodes {
			if strings.EqualFold(code, s) {
				return Currency(c), true
			}
		}
	}
	if string(lower) == "kr" {
		return p.kroner, p.hasKroner
	}
	c, ok := currencyNames[string(lower)]
	return c, ok
}

// foldInto lowers the ASCII letters of s into buf; s fits.
func foldInto(buf *[maxCurrencyName]byte, s string) []byte {
	out := buf[:len(s)]
	for i := range len(s) {
		b := s[i]
		if b >= 'A' && b <= 'Z' {
			b += 'a' - 'A'
		}
		out[i] = b
	}
	return out
}

// CurrencyQuery is a parsed "100 usd in eur": Amount is the decimal text
// as typed.
type CurrencyQuery struct {
	Amount   string
	From, To Currency
}

// conversionWords separate the amount from the currency wanted.
var conversionWords = [...]string{" in ", " to ", " into ", " as ", " -> ", " => ", " = "}

// Parse reads "<amount> <currency> in <currency>", the source currency
// before or after the amount ("$100 to €", "100 dollars in euros").
func (p CurrencyParser) Parse(query string) (CurrencyQuery, bool) {
	q := strings.TrimSpace(query)
	for _, word := range conversionWords {
		at := indexFold(q, word)
		if at < 0 {
			continue
		}
		to, ok := p.lookup(strings.TrimSpace(q[at+len(word):]))
		if !ok {
			return CurrencyQuery{}, false
		}
		amount, from, ok := p.splitAmount(strings.TrimSpace(q[:at]))
		if !ok {
			return CurrencyQuery{}, false
		}
		return CurrencyQuery{Amount: amount, From: from, To: to}, true
	}
	return CurrencyQuery{}, false
}

// splitAmount splits "100 usd", "100usd" or "$100" into the decimal and
// its currency.
func (p CurrencyParser) splitAmount(s string) (string, Currency, bool) {
	end := decimalPrefix(s)
	if end > 0 {
		c, ok := p.lookup(strings.TrimSpace(s[end:]))
		return s[:end], c, ok
	}
	start := len(s)
	for start > 0 && isDecimalByte(s[start-1]) {
		start--
	}
	if start == len(s) || decimalPrefix(s[start:]) != len(s)-start {
		return "", 0, false
	}
	c, ok := p.lookup(strings.TrimSpace(s[:start]))
	return s[start:], c, ok
}

func isDecimalByte(b byte) bool { return b >= '0' && b <= '9' || b == '.' }

// decimalPrefix is the length of the plain decimal number s starts with:
// digits with at most one point, a digit on both sides of it.
func decimalPrefix(s string) int {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	if i+1 < len(s) && s[i] == '.' && s[i+1] >= '0' && s[i+1] <= '9' {
		i++
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	return i
}

// indexFold is the last index of the lower-case ASCII word in s, ignoring
// the case of s.
func indexFold(s, word string) int {
	for i := len(s) - len(word); i >= 0; i-- {
		if strings.EqualFold(s[i:i+len(word)], word) {
			return i
		}
	}
	return -1
}
