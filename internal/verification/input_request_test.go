package verification

import (
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestBoundedInputRequestReasonsFiltersBoundsAndFallsBack(t *testing.T) {
	t.Parallel()
	maximumToken := "r" + strings.Repeat("a", 63)
	overLongToken := "r" + strings.Repeat("a", 64)
	for _, test := range []struct {
		name   string
		values []string
		want   []string
	}{
		{name: "empty falls back", values: nil, want: []string{InputRequestFallbackReason}},
		{name: "invalid only falls back", values: []string{"Upper", "has space", overLongToken}, want: []string{InputRequestFallbackReason}},
		{
			name:   "drops invalid, deduplicates and bounds",
			values: []string{"b_valid", "a_valid", "a_valid", "Upper", overLongToken, "c_valid", "d_valid", "e_valid", "f_valid", "g_valid", "h_valid", "i_valid"},
			want:   []string{"b_valid", "a_valid", "c_valid", "d_valid", "e_valid", "f_valid", "g_valid", "h_valid"},
		},
		{name: "keeps a maximum-length token", values: []string{maximumToken}, want: []string{maximumToken}},
	} {
		t.Run(test.name, func(t *testing.T) {
			got := BoundedInputRequestReasons(test.values)
			if !slices.Equal(got, test.want) {
				t.Fatalf("BoundedInputRequestReasons(%v) = %v, want %v", test.values, got, test.want)
			}
			if len(got) > MaximumInputRequestReasons {
				t.Fatalf("bounded reasons exceeded maximum: %d", len(got))
			}
		})
	}
}

func TestInputRequestValidationAndSessionProjection(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	registry, profile, published, verificationID := publishedProfileFixture(t, now)
	requestID, _ := id.ParseInputRequest("inp_01K3P4NQF00000000000000000")
	caseID, _ := id.ParseReviewCase("rvc_01K3P4NQF00000000000000000")
	actor, _ := id.ParseTask("tsk_01K3P4NQF00000000000000000")
	request, err := NewInputRequest(requestID, verificationID, caseID, []string{"document_unavailable"}, actor.String(), now)
	if err != nil {
		t.Fatal(err)
	}
	if request.ID() != requestID || request.VerificationID() != verificationID || request.CaseID() != caseID || request.ActorID() != actor.String() ||
		!slices.Equal(request.ReasonCodes(), []string{"document_unavailable"}) || !request.RequestedAt().Equal(now) {
		t.Fatalf("input request = %+v", request)
	}
	reasons := request.ReasonCodes()
	reasons[0] = "mutated"
	if request.ReasonCodes()[0] != "document_unavailable" {
		t.Fatal("input request reason codes were mutable")
	}
	for _, test := range []struct {
		name   string
		mutate func() (InputRequest, error)
	}{
		{name: "zero identifier", mutate: func() (InputRequest, error) {
			return NewInputRequest(id.InputRequest{}, verificationID, id.ReviewCase{}, []string{"document_unavailable"}, actor.String(), now)
		}},
		{name: "invalid reason", mutate: func() (InputRequest, error) {
			return NewInputRequest(requestID, verificationID, id.ReviewCase{}, []string{"Upper"}, actor.String(), now)
		}},
		{name: "duplicate reason", mutate: func() (InputRequest, error) {
			return NewInputRequest(requestID, verificationID, id.ReviewCase{}, []string{"ok", "ok"}, actor.String(), now)
		}},
		{name: "sub-microsecond time", mutate: func() (InputRequest, error) {
			return NewInputRequest(requestID, verificationID, id.ReviewCase{}, []string{"ok"}, actor.String(), now.Add(time.Nanosecond))
		}},
		{name: "non-UTC time", mutate: func() (InputRequest, error) {
			return NewInputRequest(requestID, verificationID, id.ReviewCase{}, []string{"ok"}, actor.String(), now.In(time.FixedZone("offset", 3600)))
		}},
		{name: "unknown actor", mutate: func() (InputRequest, error) {
			return NewInputRequest(requestID, verificationID, id.ReviewCase{}, []string{"ok"}, "anonymous", now)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := test.mutate(); !errors.Is(err, ErrSessionConflict) {
				t.Fatalf("error = %v, want ErrSessionConflict", err)
			}
		})
	}

	policyID, _ := id.ParsePolicy("pol_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	awaiting, err := RestoreSession(verificationID, profile.TenantID(), SessionStateAwaitingInput, 2,
		profile.ID(), published.Number(), published.Digest(), published.Document(), "local",
		policyID, now.Add(-time.Hour), now.Add(-time.Minute), now.Add(time.Hour), registry)
	if err != nil {
		t.Fatal(err)
	}
	attached, err := awaiting.WithInputRequest(request)
	if err != nil {
		t.Fatal(err)
	}
	if _, present := awaiting.InputRequest(); present {
		t.Fatal("original session unexpectedly carried the request")
	}
	projected, present := attached.InputRequest()
	if !present || !slices.Equal(projected.ReasonCodes(), []string{"document_unavailable"}) {
		t.Fatalf("projected request = %+v present=%t", projected, present)
	}
	capture, err := NewSession(verificationID, profile.TenantID(), profile, published, registry, "local", policyID, now, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := capture.WithInputRequest(request); err == nil {
		t.Fatal("collecting session accepted an input request")
	}
}
