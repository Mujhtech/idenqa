package mrz_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/document/mrz"
)

const (
	td3Line1 = "P<UTOERIKSSON<<ANNA<MARIA<<<<<<<<<<<<<<<<<<<"
	td3Line2 = "L898902C36UTO7408122F1204159ZE184226B<<<<<10"
	td1Line1 = "I<UTOD231458907<<<<<<<<<<<<<<<"
	td1Line2 = "7408122F1204159UTO<<<<<<<<<<<6"
	td1Line3 = "ERIKSSON<<ANNA<MARIA<<<<<<<<<<"
)

var td2Line1 = "I<UTOERIKSSON<<ANNA<MARIA" + strings.Repeat("<", 11)

const td2Line2 = "D231458907UTO7408122F1204159<<<<<<<6"

var specimenReference = time.Date(2015, 6, 1, 0, 0, 0, 0, time.UTC)

func TestParseSpecimens(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		lines       []string
		format      mrz.Format
		document    string
		firstName   string
		lastName    string
		birth       string
		expiry      string
		sex         string
		nationality string
	}{
		{
			name: "td1 identity card", lines: []string{td1Line1, td1Line2, td1Line3}, format: mrz.TD1,
			document: "D23145890", firstName: "ANNA MARIA", lastName: "ERIKSSON",
			birth: "1974-08-12", expiry: "2012-04-15", sex: "F", nationality: "UTO",
		},
		{
			name: "td2 official document", lines: []string{td2Line1, td2Line2}, format: mrz.TD2,
			document: "D23145890", firstName: "ANNA MARIA", lastName: "ERIKSSON",
			birth: "1974-08-12", expiry: "2012-04-15", sex: "F", nationality: "UTO",
		},
		{
			name: "td3 passport", lines: []string{td3Line1, td3Line2}, format: mrz.TD3,
			document: "L898902C3", firstName: "ANNA MARIA", lastName: "ERIKSSON",
			birth: "1974-08-12", expiry: "2012-04-15", sex: "F", nationality: "UTO",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			zone, issues, err := mrz.Parse(test.lines, specimenReference)
			if err != nil {
				t.Fatalf("parse = %v (%v)", err, issues)
			}
			if !zone.Valid() || len(issues) != 0 {
				t.Fatalf("specimen issues = %v", issues)
			}
			if zone.Format != test.format || zone.DocumentNumber != test.document ||
				zone.Name.Secondary != test.firstName || zone.Name.Primary != test.lastName ||
				zone.Sex != test.sex || zone.Nationality != test.nationality {
				t.Fatalf("zone = %+v", zone)
			}
			if got := zone.DateOfBirth.Value.Format("2006-01-02"); got != test.birth {
				t.Fatalf("birth = %s", got)
			}
			if got := zone.DateOfExpiry.Value.Format("2006-01-02"); got != test.expiry {
				t.Fatalf("expiry = %s", got)
			}
		})
	}
}

func TestParseRejectsUnsupportedLayout(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lines []string
	}{
		{name: "empty", lines: nil},
		{name: "short td3", lines: []string{strings.Repeat("A", 43), td3Line2}},
		{name: "three short lines", lines: []string{"A", "B", "C"}},
		{name: "two forty-character lines", lines: []string{strings.Repeat("A", 40), strings.Repeat("B", 40)}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			zone, issues, err := mrz.Parse(test.lines, specimenReference)
			if !errors.Is(err, mrz.ErrUnparsable) || len(issues) == 0 || zone.Format != "" {
				t.Fatalf("parse = %v %v %v", zone.Format, issues, err)
			}
		})
	}
}

func TestTamperedCheckDigits(t *testing.T) {
	t.Parallel()
	t.Run("document number", func(t *testing.T) {
		tampered := strings.Replace(td3Line2, "L898902C36", "L898902C37", 1)
		zone, issues, err := mrz.Parse([]string{td3Line1, tampered}, specimenReference)
		if err != nil || zone.Valid() {
			t.Fatalf("tampered zone accepted: %v %v", err, issues)
		}
		if !hasIssue(issues, mrz.IssueCheckDigitInvalid, mrz.FieldDocumentNumber) {
			t.Fatalf("issues = %v", issues)
		}
	})
	t.Run("composite", func(t *testing.T) {
		tampered := td3Line2[:43] + "1"
		zone, issues, err := mrz.Parse([]string{td3Line1, tampered}, specimenReference)
		if err != nil || zone.Valid() {
			t.Fatalf("tampered composite accepted: %v %v", err, issues)
		}
		if !hasIssue(issues, mrz.IssueCompositeInvalid, mrz.FieldComposite) {
			t.Fatalf("issues = %v", issues)
		}
	})
}

func TestCenturyInference(t *testing.T) {
	t.Parallel()
	reference := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		birth      string
		expiry     string
		wantBirth  string
		wantExpiry string
	}{
		{name: "older adult", birth: "900101", expiry: "300101", wantBirth: "1990-01-01", wantExpiry: "2030-01-01"},
		{name: "child", birth: "100101", expiry: "300101", wantBirth: "2010-01-01", wantExpiry: "2030-01-01"},
		{name: "long expired", birth: "900101", expiry: "950101", wantBirth: "1990-01-01", wantExpiry: "1995-01-01"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lines := buildTD3("123456789", "UTO", "DOE<<JOHN", test.birth, test.expiry, "<<<<<<<<<<<<<<")
			zone, issues, err := mrz.Parse(lines, reference)
			if err != nil {
				t.Fatalf("parse = %v %v", err, issues)
			}
			if got := zone.DateOfBirth.Value.Format("2006-01-02"); got != test.wantBirth {
				t.Fatalf("birth = %s, want %s", got, test.wantBirth)
			}
			if got := zone.DateOfExpiry.Value.Format("2006-01-02"); got != test.wantExpiry {
				t.Fatalf("expiry = %s, want %s", got, test.wantExpiry)
			}
		})
	}
}

func TestInvalidAndAbsentDates(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		birth string
		code  string
	}{
		{name: "month", birth: "901301", code: mrz.IssueDateInvalid},
		{name: "day", birth: "900132", code: mrz.IssueDateInvalid},
		{name: "february thirtieth", birth: "900230", code: mrz.IssueDateInvalid},
		{name: "absent", birth: "<<<<<<", code: mrz.IssueDateAbsent},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			lines := buildTD3("123456789", "UTO", "DOE<<JOHN", test.birth, "300101", "<<<<<<<<<<<<<<")
			_, issues, err := mrz.Parse(lines, time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC))
			if err != nil {
				t.Fatal(err)
			}
			if !hasIssue(issues, test.code, mrz.FieldDateOfBirth) {
				t.Fatalf("issues = %v", issues)
			}
		})
	}
}

func TestLowercaseAndInvalidCharacters(t *testing.T) {
	t.Parallel()
	lowered := []string{strings.ToLower(td3Line1), strings.ToLower(td3Line2)}
	zone, issues, err := mrz.Parse(lowered, specimenReference)
	if err != nil || !zone.Valid() {
		t.Fatalf("lowercase rejected: %v %v", err, issues)
	}
	if zone.DocumentNumber != "L898902C3" {
		t.Fatalf("document number = %q", zone.DocumentNumber)
	}

	invalid := strings.Replace(td3Line1, "P", "@", 1)
	_, issues, err = mrz.Parse([]string{invalid, td3Line2}, specimenReference)
	if err != nil {
		t.Fatal(err)
	}
	if !hasIssue(issues, mrz.IssueCharacterInvalid, mrz.FieldZone) {
		t.Fatalf("issues = %v", issues)
	}
}

func TestTransliterate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
		ok    bool
	}{
		{input: "Müller", want: "MUELLER", ok: true},
		{input: "Straße", want: "STRASSE", ok: true},
		{input: "Ægir Þór", want: "AEGIR<THOR", ok: true},
		{input: "O'Brien", want: "OBRIEN", ok: true},
		{input: "Mary-Jane", want: "MARY<JANE", ok: true},
		{input: "李", want: "", ok: false},
	}
	for _, test := range tests {
		t.Run(test.input, func(t *testing.T) {
			t.Parallel()
			got, ok := mrz.Transliterate(test.input)
			if got != test.want || ok != test.ok {
				t.Fatalf("Transliterate(%q) = %q, %t", test.input, got, ok)
			}
		})
	}
}

func TestCheckDigitSpecimens(t *testing.T) {
	t.Parallel()
	computed, ok := mrz.CheckDigit("L898902C3")
	if !ok || computed != '6' {
		t.Fatalf("check digit = %c, %t", computed, ok)
	}
	if _, ok := mrz.CheckDigit("lower"); ok {
		t.Fatal("lowercase accepted")
	}
}

func FuzzParse(f *testing.F) {
	f.Add(td3Line1, td3Line2, "")
	f.Add(td2Line1, td2Line2, "")
	f.Add(td1Line1, td1Line2, td1Line3)
	f.Add("", "", "")
	f.Add(strings.Repeat("A", 44), strings.Repeat("<", 44), "")
	f.Fuzz(func(t *testing.T, first, second, third string) {
		lines := []string{first, second}
		if third != "" {
			lines = append(lines, third)
		}
		zone, issues, err := mrz.Parse(lines, specimenReference)
		if len(issues) > 32 {
			t.Fatalf("unbounded issues: %d", len(issues))
		}
		if err != nil {
			if zone.Format != "" || len(issues) == 0 {
				t.Fatalf("error without bounded issue: %v", err)
			}
			return
		}
		if zone.Format != mrz.TD1 && zone.Format != mrz.TD2 && zone.Format != mrz.TD3 {
			t.Fatalf("unknown format: %q", zone.Format)
		}
		if zone.Valid() != (len(zone.Issues) == 0) {
			t.Fatalf("validity disagrees with issues")
		}
		for _, date := range []mrz.Date{zone.DateOfBirth, zone.DateOfExpiry} {
			if date.Valid && (date.Value.Year() < 1900 || date.Value.Year() > 2099) {
				t.Fatalf("unbounded year: %d", date.Value.Year())
			}
		}
	})
}

func hasIssue(issues []mrz.Issue, code, field string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Field == field {
			return true
		}
	}
	return false
}

func buildTD3(documentNumber, issuing, name, birth, expiry, personal string) []string {
	document := padFiller(documentNumber, 9)
	documentCheck, _ := mrz.CheckDigit(document)
	birthRaw := padFiller(birth, 6)
	birthCheck, _ := mrz.CheckDigit(birthRaw)
	expiryRaw := padFiller(expiry, 6)
	expiryCheck, _ := mrz.CheckDigit(expiryRaw)
	personalRaw := padFiller(personal, 14)
	personalCheck, _ := mrz.CheckDigit(personalRaw)
	line2 := document + string(documentCheck) + issuing + birthRaw + string(birthCheck) + "M" +
		expiryRaw + string(expiryCheck) + personalRaw + string(personalCheck)
	composite := line2[0:10] + line2[13:20] + line2[21:28] + line2[28:43]
	compositeCheck, _ := mrz.CheckDigit(composite)
	line2 += string(compositeCheck)
	line1 := padFiller("P<"+issuing+name, 44)
	return []string{line1, line2}
}

func padFiller(value string, length int) string {
	if len(value) >= length {
		return value[:length]
	}
	return value + strings.Repeat("<", length-len(value))
}
