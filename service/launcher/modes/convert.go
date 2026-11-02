package modes

import (
	"strings"
)

// unitKind keeps conversions between things that convert.
type unitKind uint8

const (
	unitLength unitKind = iota + 1
	unitMass
	unitVolume
	unitArea
	unitTime
	unitData
	unitSpeed
	unitTemperature
	unitAngle
	unitPressure
	unitEnergy
)

// unit is one unit: its factor to the kind's base, and for temperature
// its offset (base = value*factor + offset).
type unit struct {
	kind   unitKind
	name   string
	factor float64
	offset float64
}

// units are the spellings accepted, folded to lower case except where
// case is the meaning (Mb megabit against MB megabyte is handled first,
// in lookupUnit).
var units = buildUnits()

func buildUnits() map[string]unit {
	table := []struct {
		kind   unitKind
		name   string
		factor float64
		offset float64
		also   []string
	}{
		{unitLength, "m", 1, 0, []string{"meter", "meters", "metre", "metres"}},
		{unitLength, "km", 1000, 0, []string{"kilometer", "kilometers", "kilometre", "kilometres"}},
		{unitLength, "cm", 0.01, 0, []string{"centimeter", "centimeters", "centimetre", "centimetres"}},
		{unitLength, "mm", 0.001, 0, []string{"millimeter", "millimeters", "millimetre", "millimetres"}},
		{unitLength, "um", 1e-6, 0, []string{"µm", "micrometer", "micrometers", "micron", "microns"}},
		{unitLength, "nm", 1e-9, 0, []string{"nanometer", "nanometers"}},
		{unitLength, "in", 0.0254, 0, []string{"inch", "inches", "\""}},
		{unitLength, "ft", 0.3048, 0, []string{"foot", "feet", "'"}},
		{unitLength, "yd", 0.9144, 0, []string{"yard", "yards"}},
		{unitLength, "mi", 1609.344, 0, []string{"mile", "miles"}},
		{unitLength, "nmi", 1852, 0, []string{"nauticalmile", "nauticalmiles"}},
		{unitMass, "g", 1, 0, []string{"gram", "grams"}},
		{unitMass, "kg", 1000, 0, []string{"kilo", "kilos", "kilogram", "kilograms"}},
		{unitMass, "mg", 0.001, 0, []string{"milligram", "milligrams"}},
		{unitMass, "t", 1e6, 0, []string{"tonne", "tonnes"}},
		{unitMass, "lb", 453.59237, 0, []string{"lbs", "pound", "pounds"}},
		{unitMass, "oz", 28.349523125, 0, []string{"ounce", "ounces"}},
		{unitMass, "st", 6350.29318, 0, []string{"stone", "stones"}},
		{unitVolume, "l", 1, 0, []string{"liter", "liters", "litre", "litres"}},
		{unitVolume, "ml", 0.001, 0, []string{"milliliter", "milliliters", "millilitre", "millilitres"}},
		{unitVolume, "cl", 0.01, 0, []string{"centiliter", "centiliters"}},
		{unitVolume, "dl", 0.1, 0, []string{"deciliter", "deciliters"}},
		{unitVolume, "m3", 1000, 0, nil},
		{unitVolume, "gal", 3.785411784, 0, []string{"gallon", "gallons"}},
		{unitVolume, "qt", 0.946352946, 0, []string{"quart", "quarts"}},
		{unitVolume, "pt", 0.473176473, 0, []string{"pint", "pints"}},
		{unitVolume, "cup", 0.2365882365, 0, []string{"cups"}},
		{unitVolume, "floz", 0.0295735295625, 0, nil},
		{unitVolume, "tbsp", 0.01478676478125, 0, []string{"tablespoon", "tablespoons"}},
		{unitVolume, "tsp", 0.00492892159375, 0, []string{"teaspoon", "teaspoons"}},
		{unitArea, "m2", 1, 0, nil},
		{unitArea, "km2", 1e6, 0, nil},
		{unitArea, "cm2", 1e-4, 0, nil},
		{unitArea, "ft2", 0.09290304, 0, nil},
		{unitArea, "ha", 1e4, 0, []string{"hectare", "hectares"}},
		{unitArea, "acre", 4046.8564224, 0, []string{"acres"}},
		{unitTime, "s", 1, 0, []string{"sec", "secs", "second", "seconds"}},
		{unitTime, "ms", 0.001, 0, []string{"millisecond", "milliseconds"}},
		{unitTime, "us", 1e-6, 0, []string{"µs", "microsecond", "microseconds"}},
		{unitTime, "ns", 1e-9, 0, []string{"nanosecond", "nanoseconds"}},
		{unitTime, "min", 60, 0, []string{"mins", "minute", "minutes"}},
		{unitTime, "h", 3600, 0, []string{"hr", "hrs", "hour", "hours"}},
		{unitTime, "d", 86400, 0, []string{"day", "days"}},
		{unitTime, "wk", 604800, 0, []string{"week", "weeks"}},
		{unitTime, "yr", 31557600, 0, []string{"year", "years"}},
		{unitData, "B", 1, 0, []string{"byte", "bytes"}},
		{unitData, "KB", 1e3, 0, []string{"kilobyte", "kilobytes"}},
		{unitData, "MB", 1e6, 0, []string{"megabyte", "megabytes"}},
		{unitData, "GB", 1e9, 0, []string{"gigabyte", "gigabytes"}},
		{unitData, "TB", 1e12, 0, []string{"terabyte", "terabytes"}},
		{unitData, "PB", 1e15, 0, []string{"petabyte", "petabytes"}},
		{unitData, "KiB", 1 << 10, 0, []string{"kibibyte", "kibibytes"}},
		{unitData, "MiB", 1 << 20, 0, []string{"mebibyte", "mebibytes"}},
		{unitData, "GiB", 1 << 30, 0, []string{"gibibyte", "gibibytes"}},
		{unitData, "TiB", 1 << 40, 0, []string{"tebibyte", "tebibytes"}},
		{unitData, "bit", 0.125, 0, []string{"bits"}},
		{unitData, "Kbit", 125, 0, []string{"kilobit", "kilobits"}},
		{unitData, "Mbit", 125e3, 0, []string{"megabit", "megabits"}},
		{unitData, "Gbit", 125e6, 0, []string{"gigabit", "gigabits"}},
		{unitSpeed, "m/s", 1, 0, []string{"mps"}},
		{unitSpeed, "km/h", 1 / 3.6, 0, []string{"kph", "kmh"}},
		{unitSpeed, "mph", 0.44704, 0, nil},
		{unitSpeed, "kn", 1852.0 / 3600, 0, []string{"knot", "knots"}},
		{unitTemperature, "°C", 1, 273.15, []string{"c", "celsius", "degc"}},
		{unitTemperature, "°F", 5.0 / 9, 459.67 * 5 / 9, []string{"f", "fahrenheit", "degf"}},
		{unitTemperature, "K", 1, 0, []string{"kelvin"}},
		{unitAngle, "deg", 1, 0, []string{"degree", "degrees", "°"}},
		{unitAngle, "rad", 180 / 3.141592653589793, 0, []string{"radian", "radians"}},
		{unitAngle, "turn", 360, 0, []string{"turns"}},
		{unitPressure, "Pa", 1, 0, []string{"pascal", "pascals"}},
		{unitPressure, "kPa", 1000, 0, nil},
		{unitPressure, "bar", 1e5, 0, []string{"bars"}},
		{unitPressure, "atm", 101325, 0, nil},
		{unitPressure, "psi", 6894.757293168, 0, nil},
		{unitEnergy, "J", 1, 0, []string{"joule", "joules"}},
		{unitEnergy, "kJ", 1000, 0, nil},
		{unitEnergy, "cal", 4.184, 0, []string{"calorie", "calories"}},
		{unitEnergy, "kcal", 4184, 0, nil},
		{unitEnergy, "Wh", 3600, 0, nil},
		{unitEnergy, "kWh", 3.6e6, 0, nil},
	}
	out := make(map[string]unit, len(table)*3)
	for _, row := range table {
		u := unit{kind: row.kind, name: row.name, factor: row.factor, offset: row.offset}
		out[row.name] = u
		out[strings.ToLower(row.name)] = u
		for _, a := range row.also {
			out[a] = u
		}
	}
	return out
}

// lookupUnit reads a unit: the exact spelling first, so "Mbit" and "MB"
// keep their case's meaning, then folded.
func lookupUnit(s string) (unit, bool) {
	if u, ok := units[s]; ok {
		return u, true
	}
	u, ok := units[strings.ToLower(s)]
	return u, ok
}

// Conversion is an answered "5 km to mi".
type Conversion struct {
	// Value is the number alone, Unit its unit.
	Value, Unit string
}

// String is the result as shown.
func (c Conversion) String() string {
	if strings.HasPrefix(c.Unit, "°") {
		return c.Value + c.Unit
	}
	return c.Value + " " + c.Unit
}

// conversionWords separate the quantity from the unit wanted.
var conversionWords = []string{" to ", " in ", " as ", " -> ", " => "}

// Convert answers "<expression> <unit> to <unit>", false when the query
// is not one or the units measure different things. Incomplete input is
// not an error: half of what is typed here is half-finished.
func Convert(query string) (Conversion, bool) {
	q := strings.TrimSpace(query)
	for _, word := range conversionWords {
		at := strings.LastIndex(strings.ToLower(q), word)
		if at < 0 {
			continue
		}
		from, to := strings.TrimSpace(q[:at]), strings.TrimSpace(q[at+len(word):])
		target, ok := lookupUnit(to)
		if !ok {
			return Conversion{}, false
		}
		value, source, ok := splitQuantity(from)
		if !ok || source.kind != target.kind {
			return Conversion{}, false
		}
		base := value*source.factor + source.offset
		return Conversion{Value: FormatResult((base - target.offset) / target.factor), Unit: target.name}, true
	}
	return Conversion{}, false
}

// splitQuantity splits "1.5 km" or "2*(3+4)km" into its value and unit:
// the unit is the longest run at the end that names one and leaves an
// expression before it.
func splitQuantity(s string) (float64, unit, bool) {
	for i := range len(s) {
		u, ok := lookupUnit(strings.TrimSpace(s[i:]))
		if !ok {
			continue
		}
		if v, ok := Eval(s[:i]); ok {
			return v, u, true
		}
	}
	return 0, unit{}, false
}
