// Package mrz parses ICAO 9303 TD1, TD2, and TD3 machine-readable zones from
// already-obtained text lines. It validates per-field and composite check
// digits, resolves two-digit dates with bounded century inference at an
// explicit reference time, and reports stable issue codes. It performs no
// image decoding or OCR and never asserts document authenticity.
package mrz

import (
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// Format identifies one ICAO 9303 machine-readable zone layout.
type Format string

// Supported zone formats.
const (
	TD1 Format = "td1"
	TD2 Format = "td2"
	TD3 Format = "td3"
)

// String returns the stable wire form of the format.
func (format Format) String() string { return string(format) }

// Stable issue codes. They are bounded vocabulary and never contain a value.
const (
	IssueLengthInvalid        = "mrz_length_invalid"
	IssueCharacterInvalid     = "mrz_character_invalid"
	IssueCheckDigitInvalid    = "mrz_check_digit_invalid"
	IssueCompositeInvalid     = "mrz_composite_check_invalid"
	IssueDateAbsent           = "mrz_date_absent"
	IssueDateInvalid          = "mrz_date_invalid"
	IssueDateUnresolved       = "mrz_date_unresolved"
	IssueNameAbsent           = "mrz_name_absent"
	IssueDocumentNumberAbsent = "mrz_document_number_absent"
	IssueCountryInvalid       = "mrz_country_invalid"
	IssueSexInvalid           = "mrz_sex_invalid"
)

// Field labels used by issues. They are canonical labels, never values.
const (
	FieldZone           = "zone"
	FieldDocumentNumber = "document_number"
	FieldDateOfBirth    = "date_of_birth"
	FieldDateOfExpiry   = "date_of_expiry"
	FieldPersonalNumber = "personal_number"
	FieldComposite      = "composite"
	FieldIssuingState   = "issuing_state"
	FieldNationality    = "nationality"
	FieldSex            = "sex"
	FieldName           = "name"
)

// ErrUnparsable means the input does not match any supported zone layout.
var ErrUnparsable = errors.New("mrz: unparsable machine-readable zone")

const (
	td1Length     = 30
	td2Length     = 36
	td3Length     = 44
	maximumIssues = 32
	maximumAge    = 120
)

// Issue is one bounded structural or checksum finding.
type Issue struct {
	Code  string
	Field string
}

// Date is a resolved calendar date; Valid is false when unavailable.
type Date struct {
	Valid bool
	Value time.Time
}

// Name is the primary (family-name by ICAO convention) and secondary
// (given-names) identifier with fillers resolved to spaces.
type Name struct {
	Primary   string
	Secondary string
}

// Zone is one parsed machine-readable zone. It contains extracted identity
// values and must not be logged or persisted whole.
type Zone struct {
	Format         Format
	DocumentCode   string
	IssuingState   string
	DocumentNumber string
	Nationality    string
	Sex            string
	PersonalNumber string
	DateOfBirth    Date
	DateOfExpiry   Date
	Name           Name
	Issues         []Issue
}

// Valid reports whether the zone parsed without any issue.
func (zone Zone) Valid() bool { return len(zone.Issues) == 0 }

// Parse validates the line layout, character set, check digits, and dates of
// one zone. A layout mismatch returns ErrUnparsable and a bounded issue list.
// Success returns partial data together with any field-level issues.
func Parse(lines []string, reference time.Time) (Zone, []Issue, error) {
	normalised, normalisationIssues := normaliseLines(lines)
	format, issues, err := detectFormat(normalised)
	if err != nil {
		return Zone{}, appendIssues(issues, normalisationIssues...), err
	}
	issues = appendIssues(issues, normalisationIssues...)
	reference = reference.UTC()

	zone := Zone{Format: format}
	switch format {
	case TD1:
		parseTD1(&zone, normalised, reference, &issues)
	case TD2:
		parseTD2(&zone, normalised, reference, &issues)
	case TD3:
		parseTD3(&zone, normalised, reference, &issues)
	}
	zone.Issues = issues
	return zone, issues, nil
}

// CheckDigit computes the ICAO 9303 check digit over value. It reports false
// when value contains a character outside the MRZ character set.
func CheckDigit(value string) (byte, bool) {
	weights := [3]int{7, 3, 1}
	sum := 0
	for index := 0; index < len(value); index++ {
		character := value[index]
		digit := 0
		switch {
		case character == '<':
			digit = 0
		case character >= '0' && character <= '9':
			digit = int(character - '0')
		case character >= 'A' && character <= 'Z':
			digit = int(character-'A') + 10
		default:
			return 0, false
		}
		sum += digit * weights[index%3]
	}
	// The remainder is bounded to 0..9, so the byte conversion is exact.
	return byte('0' + sum%10), true
}

// Transliterate maps a name into the ICAO 9303 character set. Diacritics use
// the mandatory and recommended ICAO substitutions, apostrophes are skipped,
// and other punctuation or spaces become filler characters. It reports false
// when a rune has no defined transliteration.
func Transliterate(value string) (string, bool) {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, character := range value {
		upper := unicode.ToUpper(character)
		if mapped, exists := transliteration[upper]; exists {
			builder.WriteString(mapped)
			continue
		}
		switch {
		case upper >= 'A' && upper <= 'Z', upper >= '0' && upper <= '9', upper == '<':
			builder.WriteRune(upper)
		case upper == '\'' || upper == '\u2019' || upper == '\u02bc':
			continue
		case unicode.Is(unicode.Mn, upper):
			continue
		case unicode.IsSpace(upper) || unicode.IsPunct(upper) || unicode.IsSymbol(upper):
			builder.WriteByte('<')
		default:
			return "", false
		}
	}
	return builder.String(), true
}

var transliteration = map[rune]string{
	'Æ': "AE", 'Œ': "OE", 'ß': "SS", 'Þ': "TH",
	'Å': "AA", 'Ä': "AE", 'Ö': "OE", 'Ü': "UE", 'Ø': "OE", 'Ð': "D", 'Ñ': "N",
	'Ç': "C", 'É': "E", 'È': "E", 'Ê': "E", 'Ë': "E",
	'Á': "A", 'À': "A", 'Â': "A", 'Ã': "A",
	'Í': "I", 'Ì': "I", 'Î': "I", 'Ï': "I",
	'Ó': "O", 'Ò': "O", 'Ô': "O", 'Õ': "O",
	'Ú': "U", 'Ù': "U", 'Û': "U", 'Ý': "Y", 'Ÿ': "Y",
	'Š': "S", 'Ž': "Z", 'Ł': "L", 'Ć': "C", 'Č': "C", 'Đ': "D",
}

func detectFormat(lines []string) (Format, []Issue, error) {
	switch {
	case len(lines) == 3 && len(lines[0]) == td1Length && len(lines[1]) == td1Length && len(lines[2]) == td1Length:
		return TD1, nil, nil
	case len(lines) == 2 && len(lines[0]) == td2Length && len(lines[1]) == td2Length:
		return TD2, nil, nil
	case len(lines) == 2 && len(lines[0]) == td3Length && len(lines[1]) == td3Length:
		return TD3, nil, nil
	default:
		return "", []Issue{{Code: IssueLengthInvalid, Field: FieldZone}}, ErrUnparsable
	}
}

func normaliseLines(lines []string) ([]string, []Issue) {
	normalised := make([]string, len(lines))
	var issues []Issue
	for index, line := range lines {
		upper := strings.ToUpper(strings.TrimRight(line, " \t"))
		cleaned := []byte(upper)
		for position := 0; position < len(cleaned); position++ {
			if validCharacter(cleaned[position]) {
				continue
			}
			issues = appendIssue(issues, Issue{Code: IssueCharacterInvalid, Field: FieldZone})
			cleaned[position] = '<'
		}
		normalised[index] = string(cleaned)
	}
	return normalised, issues
}

func validCharacter(character byte) bool {
	return character == '<' || (character >= '0' && character <= '9') || (character >= 'A' && character <= 'Z')
}

func parseTD1(zone *Zone, lines []string, reference time.Time, issues *[]Issue) {
	line1, line2 := lines[0], lines[1]
	zone.DocumentCode = cleanField(line1[0:2])
	zone.IssuingState = parseCountry(line1[2:5], FieldIssuingState, issues)
	documentNumber := line1[5:14]
	verifyField(documentNumber, line1[14], FieldDocumentNumber, issues)
	zone.DocumentNumber = cleanDocumentNumber(documentNumber, issues)

	dateOfBirth := line2[0:6]
	verifyField(dateOfBirth, line2[6], FieldDateOfBirth, issues)
	zone.DateOfBirth = resolve(dateOfBirth, dateOfBirthKind, reference, issues)
	zone.Sex = parseSex(line2[7], issues)
	dateOfExpiry := line2[8:14]
	verifyField(dateOfExpiry, line2[14], FieldDateOfExpiry, issues)
	zone.DateOfExpiry = resolve(dateOfExpiry, dateOfExpiryKind, reference, issues)
	zone.Nationality = parseCountry(line2[15:18], FieldNationality, issues)

	composite := line1[5:30] + line2[0:7] + line2[8:15] + line2[18:29]
	verifyComposite(composite, line2[29], issues)
	zone.Name = parseName(lines[2], issues)
}

func parseTD2(zone *Zone, lines []string, reference time.Time, issues *[]Issue) {
	line1, line2 := lines[0], lines[1]
	zone.DocumentCode = cleanField(line1[0:2])
	zone.IssuingState = parseCountry(line1[2:5], FieldIssuingState, issues)

	documentNumber := line2[0:9]
	verifyField(documentNumber, line2[9], FieldDocumentNumber, issues)
	zone.DocumentNumber = cleanDocumentNumber(documentNumber, issues)

	zone.Nationality = parseCountry(line2[10:13], FieldNationality, issues)
	dateOfBirth := line2[13:19]
	verifyField(dateOfBirth, line2[19], FieldDateOfBirth, issues)
	zone.DateOfBirth = resolve(dateOfBirth, dateOfBirthKind, reference, issues)
	zone.Sex = parseSex(line2[20], issues)
	dateOfExpiry := line2[21:27]
	verifyField(dateOfExpiry, line2[27], FieldDateOfExpiry, issues)
	zone.DateOfExpiry = resolve(dateOfExpiry, dateOfExpiryKind, reference, issues)

	composite := line2[0:10] + line2[13:20] + line2[21:28] + line2[28:35]
	verifyComposite(composite, line2[35], issues)
	zone.Name = parseName(line1[5:36], issues)
}

func parseTD3(zone *Zone, lines []string, reference time.Time, issues *[]Issue) {
	line1, line2 := lines[0], lines[1]
	zone.DocumentCode = cleanField(line1[0:2])
	zone.IssuingState = parseCountry(line1[2:5], FieldIssuingState, issues)

	documentNumber := line2[0:9]
	verifyField(documentNumber, line2[9], FieldDocumentNumber, issues)
	zone.DocumentNumber = cleanDocumentNumber(documentNumber, issues)

	zone.Nationality = parseCountry(line2[10:13], FieldNationality, issues)
	dateOfBirth := line2[13:19]
	verifyField(dateOfBirth, line2[19], FieldDateOfBirth, issues)
	zone.DateOfBirth = resolve(dateOfBirth, dateOfBirthKind, reference, issues)
	zone.Sex = parseSex(line2[20], issues)
	dateOfExpiry := line2[21:27]
	verifyField(dateOfExpiry, line2[27], FieldDateOfExpiry, issues)
	zone.DateOfExpiry = resolve(dateOfExpiry, dateOfExpiryKind, reference, issues)

	personalNumber := line2[28:42]
	verifyField(personalNumber, line2[42], FieldPersonalNumber, issues)
	zone.PersonalNumber = cleanPersonalNumber(personalNumber)

	composite := line2[0:10] + line2[13:20] + line2[21:28] + line2[28:43]
	verifyComposite(composite, line2[43], issues)
	zone.Name = parseName(line1[5:44], issues)
}

func verifyField(raw string, expected byte, label string, issues *[]Issue) {
	computed, ok := CheckDigit(raw)
	if ok && (expected == computed || (allFillers(raw) && expected == '<')) {
		return
	}
	*issues = appendIssue(*issues, Issue{Code: IssueCheckDigitInvalid, Field: label})
}

func verifyComposite(raw string, expected byte, issues *[]Issue) {
	computed, ok := CheckDigit(raw)
	if ok && expected == computed {
		return
	}
	*issues = appendIssue(*issues, Issue{Code: IssueCompositeInvalid, Field: FieldComposite})
}

type dateKind int

const (
	dateOfBirthKind dateKind = iota
	dateOfExpiryKind
)

func resolve(raw string, kind dateKind, reference time.Time, issues *[]Issue) Date {
	label := FieldDateOfBirth
	if kind == dateOfExpiryKind {
		label = FieldDateOfExpiry
	}
	if allFillers(raw) {
		*issues = appendIssue(*issues, Issue{Code: IssueDateAbsent, Field: label})
		return Date{}
	}
	if !allDigits(raw) {
		*issues = appendIssue(*issues, Issue{Code: IssueDateInvalid, Field: label})
		return Date{}
	}
	if reference.IsZero() {
		*issues = appendIssue(*issues, Issue{Code: IssueDateUnresolved, Field: label})
		return Date{}
	}
	year, _ := strconv.Atoi(raw[0:2])
	month, _ := strconv.Atoi(raw[2:4])
	day, _ := strconv.Atoi(raw[4:6])
	if month < 1 || month > 12 || day < 1 || day > 31 {
		*issues = appendIssue(*issues, Issue{Code: IssueDateInvalid, Field: label})
		return Date{}
	}
	candidates := [2]time.Time{
		time.Date(1900+year, time.Month(month), day, 0, 0, 0, 0, time.UTC),
		time.Date(2000+year, time.Month(month), day, 0, 0, 0, 0, time.UTC),
	}
	calendarValidCandidates := make([]time.Time, 0, len(candidates))
	for _, candidate := range candidates {
		if calendarValid(candidate, month, day) {
			calendarValidCandidates = append(calendarValidCandidates, candidate)
		}
	}
	if len(calendarValidCandidates) == 0 {
		*issues = appendIssue(*issues, Issue{Code: IssueDateInvalid, Field: label})
		return Date{}
	}
	if kind == dateOfBirthKind {
		resolved := time.Time{}
		for _, candidate := range calendarValidCandidates {
			if candidate.After(reference) || ageAt(candidate, reference) > maximumAge {
				continue
			}
			resolved = candidate
		}
		if resolved.IsZero() {
			*issues = appendIssue(*issues, Issue{Code: IssueDateUnresolved, Field: label})
			return Date{}
		}
		return Date{Valid: true, Value: resolved}
	}
	best := time.Time{}
	bestDistance := time.Duration(1<<63 - 1)
	for _, candidate := range calendarValidCandidates {
		distance := reference.Sub(candidate)
		if distance < 0 {
			distance = -distance
		}
		if distance < bestDistance {
			best, bestDistance = candidate, distance
		}
	}
	if best.IsZero() || bestDistance > 100*365*24*time.Hour {
		*issues = appendIssue(*issues, Issue{Code: IssueDateUnresolved, Field: label})
		return Date{}
	}
	return Date{Valid: true, Value: best}
}

func calendarValid(candidate time.Time, month, day int) bool {
	return int(candidate.Month()) == month && candidate.Day() == day
}

func ageAt(birth, now time.Time) int {
	years := now.Year() - birth.Year()
	if now.Month() < birth.Month() || (now.Month() == birth.Month() && now.Day() < birth.Day()) {
		years--
	}
	return years
}

func parseName(raw string, issues *[]Issue) Name {
	primary, secondary, found := strings.Cut(raw, "<<")
	if !found {
		primary = raw
	}
	name := Name{Primary: cleanNamePart(primary), Secondary: cleanNamePart(secondary)}
	if name.Primary == "" && name.Secondary == "" {
		*issues = appendIssue(*issues, Issue{Code: IssueNameAbsent, Field: FieldName})
	}
	return name
}

func cleanNamePart(raw string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(raw, "<", " ")), " ")
}

func parseSex(raw byte, issues *[]Issue) string {
	switch raw {
	case 'M', 'F':
		return string(raw)
	case '<':
		return ""
	default:
		*issues = appendIssue(*issues, Issue{Code: IssueSexInvalid, Field: FieldSex})
		return ""
	}
}

func parseCountry(raw string, label string, issues *[]Issue) string {
	value := cleanField(raw)
	if value == "" {
		return ""
	}
	if len(value) > 3 || !letters(value) {
		*issues = appendIssue(*issues, Issue{Code: IssueCountryInvalid, Field: label})
		return ""
	}
	return value
}

func cleanDocumentNumber(raw string, issues *[]Issue) string {
	value := cleanField(raw)
	if value == "" {
		*issues = appendIssue(*issues, Issue{Code: IssueDocumentNumberAbsent, Field: FieldDocumentNumber})
	}
	return value
}

func cleanPersonalNumber(raw string) string {
	return cleanField(raw)
}

func cleanField(raw string) string {
	replaced := strings.ReplaceAll(raw, "<", "")
	return strings.TrimSpace(replaced)
}

func letters(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] < 'A' || value[index] > 'Z' {
			return false
		}
	}
	return true
}

func allFillers(value string) bool { return strings.Trim(value, "<") == "" }

func allDigits(value string) bool {
	if value == "" {
		return false
	}
	for index := 0; index < len(value); index++ {
		if value[index] < '0' || value[index] > '9' {
			return false
		}
	}
	return true
}

func appendIssue(issues []Issue, issue Issue) []Issue {
	if len(issues) >= maximumIssues {
		return issues
	}
	for _, existing := range issues {
		if existing == issue {
			return issues
		}
	}
	return append(issues, issue)
}

func appendIssues(issues []Issue, extra ...Issue) []Issue {
	for _, issue := range extra {
		issues = appendIssue(issues, issue)
	}
	return issues
}
