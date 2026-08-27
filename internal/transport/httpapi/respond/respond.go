// Package respond writes stable JSON resource and problem-details responses.
package respond

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/go-chi/render"
)

const problemBaseURL = "https://idenqa.dev/problems/"

// Problem is Idenqa's problem-details representation.
type Problem struct {
	Type      string `json:"type"`
	Title     string `json:"title"`
	Status    int    `json:"status"`
	Code      string `json:"code"`
	Detail    string `json:"detail"`
	RequestID string `json:"request_id,omitempty"`
}

// JSON writes a successful resource directly with status.
func JSON(writer http.ResponseWriter, request *http.Request, status int, resource any) error {
	return writeJSON(writer, request, status, "application/json; charset=utf-8", resource)
}

// CanonicalJSON writes already validated canonical JSON without re-encoding it
// or appending a newline. Callers retain ownership of the media type.
func CanonicalJSON(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	contentType string,
	payload []byte,
) error {
	if !json.Valid(payload) {
		return fmt.Errorf("encode HTTP response: canonical payload is not valid JSON")
	}

	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	render.Status(request, status)
	writer.WriteHeader(status)
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("write HTTP response: %w", err)
	}

	return nil
}

// NoContent writes a successful response without a body.
func NoContent(writer http.ResponseWriter) {
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	writer.WriteHeader(http.StatusNoContent)
}

// WriteProblem maps err to a safe problem-details response.
func WriteProblem(
	writer http.ResponseWriter,
	request *http.Request,
	err error,
	requestID string,
) error {
	failure := apierror.Map(err)
	if retryAfter := failure.RetryAfter(); retryAfter > 0 {
		seconds := max(int64((retryAfter+time.Second-1)/time.Second), 1)
		writer.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
	}
	if challenge := failure.Challenge(); challenge != "" {
		writer.Header().Set("WWW-Authenticate", challenge)
	}

	problem := Problem{
		Type:      problemBaseURL + strings.ToLower(strings.ReplaceAll(failure.Code(), "_", "-")),
		Title:     failure.Title(),
		Status:    failure.Status(),
		Code:      failure.Code(),
		Detail:    failure.Detail(),
		RequestID: requestID,
	}

	return writeJSON(writer, request, failure.Status(), "application/problem+json; charset=utf-8", problem)
}

func writeJSON(
	writer http.ResponseWriter,
	request *http.Request,
	status int,
	contentType string,
	value any,
) error {
	payload, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode HTTP response: %w", err)
	}
	payload = append(payload, '\n')

	writer.Header().Set("Content-Type", contentType)
	writer.Header().Set("X-Content-Type-Options", "nosniff")
	render.Status(request, status)
	writer.WriteHeader(status)
	if _, err := writer.Write(payload); err != nil {
		return fmt.Errorf("write HTTP response: %w", err)
	}

	return nil
}
