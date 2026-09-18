//go:build integration

package integration_test

import (
	"net/http"
	"testing"

	openapiv1 "github.com/Mujhtech/idenqa/internal/gen/openapi/v1"
)

func assertPublicCancellation(t *testing.T, client *http.Client, baseURL, backend, profileID, policyID string) {
	t.Helper()
	for _, subject := range []bool{false, true} {
		name := "tenant"
		if subject {
			name = "subject"
		}
		var creation openapiv1.VerificationCreated
		performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodPost, URL: baseURL + "/v1/verifications", Bearer: backend, IdempotencyKey: "cancel-create-" + name, Body: openapiv1.VerificationCreate{CaptureProfileID: profileID, PolicyID: policyID}, WantStatus: http.StatusCreated, Result: &creation})
		if creation.CaptureToken == nil {
			t.Fatal("missing capture token")
		}
		url, credential := baseURL+"/v1/verifications/"+creation.Session.ID+"/cancel", backend
		if subject {
			url, credential = baseURL+"/v1/capture/cancel", *creation.CaptureToken
		}
		var first, second openapiv1.VerificationCancellation
		input := publicJSONRequest{Method: http.MethodPost, URL: url, Bearer: credential, IdempotencyKey: "cancel-" + name, Body: openapiv1.VerificationCancel{ExpectedVersion: 1}, WantStatus: http.StatusOK, Result: &first}
		performPublicJSONRequest(t, client, input)
		input.Result = &second
		performPublicJSONRequest(t, client, input)
		if first.EventID != second.EventID || first.Version != 2 || first.VerificationID != creation.Session.ID || !first.OccurredAt.Equal(second.OccurredAt) {
			t.Fatal("cancellation replay changed")
		}
		input.Body = openapiv1.VerificationCancel{ExpectedVersion: 2}
		input.Result = nil
		input.WantStatus = http.StatusConflict
		performPublicJSONRequest(t, client, input)
		performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodGet, URL: baseURL + "/v1/capture/session", Bearer: *creation.CaptureToken, WantStatus: http.StatusUnauthorized})
		var current openapiv1.VerificationSession
		performPublicJSONRequest(t, client, publicJSONRequest{Method: http.MethodGet, URL: baseURL + "/v1/verifications/" + creation.Session.ID, Bearer: backend, WantStatus: http.StatusOK, Result: &current})
		if current.State != openapiv1.VerificationSessionStateCancelled || current.Version != 2 {
			t.Fatal("cancelled state not visible")
		}
	}
}
