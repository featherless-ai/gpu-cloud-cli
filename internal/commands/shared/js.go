package shared

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf16"
)

var (
	decimalNumber = regexp.MustCompile(`^[+-]?([0-9]+\.?[0-9]*|\.[0-9]+)([eE][+-]?[0-9]+)?$`)
	prefixedInt   = regexp.MustCompile(`^0([xX][0-9a-fA-F]+|[oO][0-7]+|[bB][01]+)$`)
)

// ParseNumber reads a flag value as JavaScript's Number() does: surrounding
// whitespace is ignored, empty is 0, and anything else that is not a number
// is NaN.
func ParseNumber(value string) float64 {
	text := strings.TrimSpace(value)

	switch {
	case text == "":
		return 0
	case text == "Infinity" || text == "+Infinity":
		return math.Inf(1)
	case text == "-Infinity":
		return math.Inf(-1)
	case prefixedInt.MatchString(text):
		parsed, err := strconv.ParseInt(text, 0, 64)

		if err != nil {
			return math.NaN()
		}

		return float64(parsed)
	case decimalNumber.MatchString(text):
		parsed, err := strconv.ParseFloat(text, 64)

		if err != nil && !math.IsInf(parsed, 0) {
			return math.NaN()
		}

		return parsed
	}

	return math.NaN()
}

// IsInteger is Number.isInteger.
func IsInteger(number float64) bool {
	return !math.IsNaN(number) && !math.IsInf(number, 0) && number == math.Trunc(number)
}

// FormatNumber is String(number) for the numbers the CLI prints.
func FormatNumber(number float64) string {
	switch {
	case math.IsNaN(number):
		return "NaN"
	case math.IsInf(number, 1):
		return "Infinity"
	case math.IsInf(number, -1):
		return "-Infinity"
	}

	return strconv.FormatFloat(number, 'f', -1, 64)
}

// UTF16Length is a JavaScript string's length.
func UTF16Length(text string) int {
	return len(utf16.Encode([]rune(text)))
}

// Slice is JavaScript's text.slice(0, end), counting UTF-16 code units.
func Slice(text string, end int) string {
	units := utf16.Encode([]rune(text))

	if end >= len(units) {
		return text
	}

	return string(utf16.Decode(units[:end]))
}
