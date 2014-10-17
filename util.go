package migu

import (
	"bytes"
	"unicode"
)

// toCamelCase returns a copy of the string s with all Unicode letters mapped to their camel case.
// It will convert to upper case previous letter of '_' and first letter, and remove letter of '_'.
func toCamelCase(s string) string {
	if s == "" {
		return ""
	}
	result := make([]rune, 0, len(s))
	upper := false
	for _, r := range s {
		if r == '_' {
			upper = true
			continue
		}
		if upper {
			result = append(result, unicode.ToUpper(r))
			upper = false
			continue
		}
		result = append(result, r)
	}
	result[0] = unicode.ToUpper(result[0])
	return string(result)
}

// toSnakeCase returns a copy of the string s with all Unicode letters mapped to their snake case.
// It will insert letter of '_' at position of previous letter of uppercase and all
// letters convert to lower case.
func toSnakeCase(s string) string {
	if s == "" {
		return ""
	}
	var result bytes.Buffer
	result.WriteRune(unicode.ToLower(rune(s[0])))
	for _, c := range s[1:] {
		if unicode.IsUpper(c) {
			result.WriteRune('_')
		}
		result.WriteRune(unicode.ToLower(c))
	}
	return result.String()
}
