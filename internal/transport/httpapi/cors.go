package httpapi

import (
	"net/http"

	chicors "github.com/go-chi/cors"
)

var (
	allowedCORSMethods = []string{
		http.MethodDelete,
		http.MethodGet,
		http.MethodOptions,
		http.MethodPatch,
		http.MethodPost,
		http.MethodPut,
	}
	allowedCORSHeaders = []string{
		"authorization",
		"content-type",
		"idempotency-key",
		"if-match",
		"if-none-match",
		"traceparent",
		"tracestate",
	}
	exposedCORSHeaders = []string{
		"Deprecation",
		"ETag",
		"Link",
		"Location",
		"RateLimit",
		"RateLimit-Policy",
		"Retry-After",
		"Sunset",
		"WWW-Authenticate",
		requestIDHeader,
	}
)

func cors(allowedOrigins []string) func(http.Handler) http.Handler {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		origins[origin] = struct{}{}
	}

	return chicors.Handler(chicors.Options{
		AllowOriginFunc: func(_ *http.Request, origin string) bool {
			_, allowed := origins[origin]

			return allowed
		},
		AllowedMethods:   allowedCORSMethods,
		AllowedHeaders:   allowedCORSHeaders,
		ExposedHeaders:   exposedCORSHeaders,
		AllowCredentials: false,
		MaxAge:           600,
	})
}
