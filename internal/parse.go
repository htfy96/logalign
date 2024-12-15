package internal

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ParsedFormatter struct {
	// Number of formatter arguments.
	ArgCnt int
	// Named-capture regex with top-level group, argument groups,
	// attempts width/precision handling, no ^/$ anchors, uses non-greedy.
	Regex string
	// Hyperscan-compatible regex:
	// - No capturing groups
	// - No zero-width assertions
	// - Non-greedy quantifiers
	// - Does not handle width/padding constraints
	HyperScanRegex string
}

func ParsePrintfFormat(format string, topLevelGroupName string) (ParsedFormatter, error) {
	specRe := regexp.MustCompile(`%([#+0\- ]*)(\d*)(?:\.(\d+))?[hlLjzt]*([diuoxXfFeEgGaAcsp])`)

	matches := specRe.FindAllStringSubmatchIndex(format, -1)
	if len(matches) == 0 {
		// No specifiers
		escaped := regexp.QuoteMeta(format)
		pf := ParsedFormatter{
			ArgCnt:         0,
			Regex:          fmt.Sprintf("(?<%s>%s)", topLevelGroupName, escaped),
			HyperScanRegex: escaped,
		}
		return pf, nil
	}

	argCount := 0
	var namedBuilder strings.Builder
	var hsBuilder strings.Builder

	// Start named-capture regex with top-level group
	namedBuilder.WriteString("(?<")
	namedBuilder.WriteString(topLevelGroupName)
	namedBuilder.WriteString(">")

	lastEnd := 0

	for _, m := range matches {
		fullStart, fullEnd := m[0], m[1]
		flagsStart, flagsEnd := m[2], m[3]
		widthStart, widthEnd := m[4], m[5]
		precStart, precEnd := m[6], m[7]
		specStart, specEnd := m[8], m[9]

		literal := format[lastEnd:fullStart]
		namedBuilder.WriteString(regexp.QuoteMeta(literal))
		hsBuilder.WriteString(regexp.QuoteMeta(literal))

		flags := format[flagsStart:flagsEnd]
		widthStr := format[widthStart:widthEnd]
		precStr := ""
		if precStart != -1 && precEnd != -1 {
			precStr = format[precStart:precEnd]
		}
		spec := format[specStart:specEnd]

		leftAlign := strings.Contains(flags, "-")
		zeroPad := !leftAlign && strings.Contains(flags, "0")
		altForm := strings.Contains(flags, "#")
		// plusFlag := strings.Contains(flags, "+")
		// spaceFlag := strings.Contains(flags, " ")

		width := 0
		if widthStr != "" {
			if w, err := strconv.Atoi(widthStr); err == nil {
				width = w
			}
		}
		precision := -1
		if precStr != "" {
			if p, err := strconv.Atoi(precStr); err == nil {
				precision = p
			}
		}

		argName := fmt.Sprintf("arg%s%d", topLevelGroupName, argCount)

		// Helper to handle width for named regex:
		widthWrap := func(core string, numeric bool) string {
			if width <= 0 {
				// No width constraints
				return core
			}
			// We'll try the logic from before:
			// leftAlign: (?=.{width,})core *
			// rightAlign + zeroPad (numeric): (?=.{width,})0*core
			// rightAlign + spaces: (?=.{width,}) *core

			if leftAlign {
				// left align: ensure total length at least width, trailing spaces allowed
				return fmt.Sprintf("(?=.{%d,})%s *", width, core)
			} else {
				// right align
				if zeroPad && numeric {
					return fmt.Sprintf("(?=.{%d,})0*%s", width, core)
				} else {
					// space padding
					return fmt.Sprintf("(?=.{%d,}) *%s", width, core)
				}
			}
		}

		// For the Hyperscan pattern, we cannot enforce width:
		// We'll just produce a simple pattern without assertions or groups.

		// Construct core patterns:
		// Non-greedy quantifiers: use +? for digits, .+? for strings, etc.
		infNanPattern := `inf|nan`

		var namedArgPattern, hsArgPattern string

		switch spec {
		case "d", "i":
			// Signed integer
			// If precision >=0: at least that many digits
			p := precision
			if p < 0 {
				p = 0
			}
			// But we must ensure at least p digits. Let's simplify: \d{p,}
			// Non-greedy for a bounded quantifier is tricky. We'll just trust \d{p,} to match minimal p digits. Non-greedy quantifier +? after {p,} is not standard, but {p,} is already minimal p digits.
			// We'll do: `[-+]?\d{` + strconv.Itoa(p) + `,}` and rely on no trailing greed.
			ncore := fmt.Sprintf("[-+]?\\d{%d,}", p)
			// To make it "non-greedy", we can add a lazy quantifier to a larger group: \d{%d,}? won't be accepted by most engines.
			// We'll rely on the rest of the pattern and no anchors to not cause over-greedy matches.
			// It's hard to truly enforce non-greedy on a {m,} quantifier, but let's leave it as is.

			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[-+]?\\d+?" // Hyperscan just a simplified version

			// width wrap:
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "u":
			// Unsigned int
			p := precision
			if p < 0 {
				p = 0
			}
			ncore := fmt.Sprintf("\\d{%d,}", p)
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "\\d+?"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "o":
			// Octal
			// If altForm -> leading 0 if nonzero. We'll just allow optional 0 prefix.
			p := precision
			if p < 0 {
				p = 0
			}
			prefix := ""
			if altForm {
				// Allow optional leading '0'
				prefix = "0?"
			}
			ncore := prefix + `[0-7]{` + strconv.Itoa(p) + `,}`
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[0-7]+?"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "x", "X":
			// Hex
			// altForm -> optional 0x prefix
			p := precision
			if p < 0 {
				p = 0
			}
			prefix := ""
			if altForm {
				prefix = "0[xX]?"
			}
			ncore := prefix + `[0-9A-Fa-f]{` + strconv.Itoa(p) + `,}`
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[0-9A-Fa-f]+?"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "f", "F":
			// Simple decimal float: sign?, digits, optional decimal with p digits, or inf/nan
			// p default =6 if not specified, handle altForm decimal point
			p := precision
			if p < 0 {
				p = 6
			}
			// pattern: sign?(inf|nan|(\d+(\.\d{p})?))
			ncore := fmt.Sprintf("[-+]?(?:%s|\\d+(?:\\.\\d{%d})?)", infNanPattern, p)
			if p == 0 && altForm {
				// If p=0 and # set, decimal point but no fraction
				ncore = `[-+]?(?:` + infNanPattern + `|\d+\.)`
			} else if p == 0 && !altForm {
				// no fraction, no decimal point
				ncore = `[-+]?(?:` + infNanPattern + `|\d+)`
			}
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[-+]?(?:\\d+?(?:\\.\\d+?)?|inf|nan)"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "e", "E":
			// Scientific notation: sign?(inf|nan|digit(.digits)?[eE][+-]?digits)
			p := precision
			if p < 0 {
				p = 6
			}
			fraction := ""
			if p > 0 {
				fraction = `\.\d{` + strconv.Itoa(p) + `}`
			} else if altForm {
				fraction = `\.`
			}
			ncore := fmt.Sprintf("[-+]?(?:%s|\\d%s[eE][+-]?\\d+)", infNanPattern, fraction)
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[-+]?(?:\\d+?(?:\\.\\d+?)?[eE][+-]?\\d+?|inf|nan)"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "g", "G":
			// Very tricky. We'll just allow either f or e form:
			p := precision
			if p < 0 {
				p = 6
			}
			ncore := fmt.Sprintf("[-+]?(?:%s|\\d+(?:\\.\\d+)?(?:[eE][+-]?\\d+)?)", infNanPattern)
			// Not exact, but an approximation.
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[-+]?(?:\\d+?(?:\\.\\d+?)?(?:[eE][+-]?\\d+?)?|inf|nan)"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "a", "A":
			// Hex float: sign?(inf|nan|0xhex(.hex)?[pP][+-]?digits)
			p := precision
			if p < 0 {
				p = 6
			}
			frac := ""
			if p > 0 {
				frac = `\.[0-9A-Fa-f]{` + strconv.Itoa(p) + `}`
			} else if altForm {
				frac = `\.`
			}
			ncore := fmt.Sprintf("[-+]?(?:%s|0[xX][0-9A-Fa-f]+%s[pP][+-]?\\d+)", infNanPattern, frac)
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "[-+]?(?:0[xX][0-9A-Fa-f]+?(?:\\.[0-9A-Fa-f]+?)?[pP][+-]?\\d+?|inf|nan)"
			namedArgPattern = widthWrap(namedArgPattern, true)

		case "c":
			// Single char. width means padding either side.
			// We'll treat char as one char plus padding if width.
			// For the named pattern, widthWrap core = '.' (one char)
			ncore := `.`
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = "."
			namedArgPattern = widthWrap(namedArgPattern, false)

		case "s":
			// String
			// Precision means max chars. We'll try .{0,precision}
			// Non-greedy: .+? is non-greedy. If precision>=0, enforce max length: .{0,precision}
			if precision >= 0 {
				ncore := `.{0,` + strconv.Itoa(precision) + `}`
				namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
				hsArgPattern = ".+?" // HS doesn't enforce precision
			} else {
				ncore := `.+?`
				namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
				hsArgPattern = ".+?"
			}
			// widthWrap for strings
			namedArgPattern = widthWrap(namedArgPattern, false)

		case "p":
			// Pointer, typically 0x followed by hex
			ncore := `0x[0-9A-Fa-f]+?`
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = `0x[0-9A-Fa-f]+?`
			namedArgPattern = widthWrap(namedArgPattern, false)

		default:
			// Fallback: string
			ncore := `.+?`
			namedArgPattern = fmt.Sprintf("(?<%s>%s)", argName, ncore)
			hsArgPattern = ".+?"
			namedArgPattern = widthWrap(namedArgPattern, false)
		}

		namedBuilder.WriteString(namedArgPattern)
		hsBuilder.WriteString(hsArgPattern)

		argCount++
		lastEnd = fullEnd
	}

	// Trailing literal
	if lastEnd < len(format) {
		literal := format[lastEnd:]
		namedBuilder.WriteString(regexp.QuoteMeta(literal))
		hsBuilder.WriteString(regexp.QuoteMeta(literal))
	}

	namedBuilder.WriteString(")")

	pf := ParsedFormatter{
		ArgCnt:         argCount,
		Regex:          namedBuilder.String(),
		HyperScanRegex: hsBuilder.String(),
	}

	return pf, nil
}
