package experience

import (
	"errors"
	"testing"

	contract "github.com/Mujhtech/idenqa/contracts/experience/v1"
)

func publishedFixture(experienceID string, rules []contract.Target) Published {
	return Published{
		ExperienceID: mustExperienceID(experienceID),
		Version:      1,
		Manifest: contract.Manifest{
			Document: contract.Document{
				SchemaVersion: contract.SchemaVersion, ExperienceID: experienceID, Version: 1,
				DefaultLocale: "en", Targeting: rules,
			},
		},
	}
}

func TestMatchDocument(t *testing.T) {
	t.Parallel()
	document := contract.Document{Targeting: []contract.Target{
		{Workflow: "capture.identity", Countries: []string{"NG", "KE"}},
		{Workflow: "capture.identity"},
		{Countries: []string{"GH"}},
	}}
	tests := []struct {
		name      string
		request   ResolutionRequest
		wantScore int
		wantMatch bool
	}{
		{name: "first matching rule wins", request: ResolutionRequest{Workflow: "capture.identity", Country: "NG"}, wantScore: 2, wantMatch: true},
		{name: "second rule when country unlisted", request: ResolutionRequest{Workflow: "capture.identity", Country: "ZA"}, wantScore: 1, wantMatch: true},
		{name: "third rule for another country", request: ResolutionRequest{Country: "GH"}, wantScore: 1, wantMatch: true},
		{name: "unlisted workflow still matches the country-only rule", request: ResolutionRequest{Country: "GH", Workflow: "other"}, wantScore: 1, wantMatch: true},
		{name: "missing country cannot prove country rule", request: ResolutionRequest{Workflow: "capture.identity"}, wantScore: 1, wantMatch: true},
		{name: "no matching rule", request: ResolutionRequest{Workflow: "other", Country: "ZA"}, wantMatch: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			score, matched := MatchDocument(document, test.request)
			if matched != test.wantMatch || score != test.wantScore {
				t.Fatalf("MatchDocument() = (%d, %v), want (%d, %v)", score, matched, test.wantScore, test.wantMatch)
			}
		})
	}
}

func TestMatchDocumentCatchAll(t *testing.T) {
	t.Parallel()
	score, matched := MatchDocument(contract.Document{}, ResolutionRequest{Workflow: "anything"})
	if !matched || score != 0 {
		t.Fatalf("catch-all MatchDocument() = (%d, %v), want (0, true)", score, matched)
	}
}

func TestResolveMostSpecificWinsAndConflictsFailClosed(t *testing.T) {
	t.Parallel()
	first := publishedFixture("exp_01J00000000000000000000001", []contract.Target{{Countries: []string{"NG"}}})
	specific := publishedFixture("exp_01J00000000000000000000002", []contract.Target{
		{Workflow: "capture.identity", Countries: []string{"NG"}, ApplicationIDs: []string{"dev.acme.app"}},
	})
	ambiguous := publishedFixture("exp_01J00000000000000000000003", []contract.Target{
		{Workflow: "capture.identity", Countries: []string{"NG"}, ApplicationIDs: []string{"dev.acme.app"}},
	})
	request := ResolutionRequest{Workflow: "capture.identity", Country: "NG", ApplicationID: "dev.acme.app"}

	match, ok, err := Resolve([]Published{first, specific}, request)
	if err != nil || !ok || match.ExperienceID.String() != specific.ExperienceID.String() {
		t.Fatalf("Resolve() = (%v, %v, %v), want most specific", match.ExperienceID, ok, err)
	}
	if _, _, err := Resolve([]Published{specific, ambiguous}, request); !errors.Is(err, ErrResolutionConflict) {
		t.Fatalf("Resolve() conflict error = %v, want ErrResolutionConflict", err)
	}
	if _, ok, err := Resolve([]Published{first}, ResolutionRequest{Workflow: "capture.identity", Country: "ZA"}); ok || err != nil {
		t.Fatalf("Resolve() no-match = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestMatchDocumentSDKRangeFailsClosed(t *testing.T) {
	t.Parallel()
	document := contract.Document{Targeting: []contract.Target{
		{SDKVersionMin: "1.2.0", SDKVersionMax: "1.9.0"},
	}}
	for _, version := range []string{"", "1.1.9", "2.0.0"} {
		if _, matched := MatchDocument(document, ResolutionRequest{SDKVersion: version}); matched {
			t.Fatalf("SDK version %q matched outside the declared range", version)
		}
	}
	for _, version := range []string{"1.2.0", "1.5.3", "1.9.0"} {
		if _, matched := MatchDocument(document, ResolutionRequest{SDKVersion: version}); !matched {
			t.Fatalf("SDK version %q did not match its declared range", version)
		}
	}
}
