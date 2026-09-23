package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/apierror"
	"github.com/Mujhtech/idenqa/internal/transport/httpapi/respond"
	"github.com/go-chi/chi/v5"
)

const (
	codeCallbackRejected = "CALLBACK_REJECTED"

	callbackLimiterWindow  = 10 * time.Second
	callbackLimiterBurst   = 20
	callbackLimiterMaximum = 4096
	callbackHeaderBytes    = 8 << 10
)

// ProviderCallbackService verifies and records one bounded provider callback.
type ProviderCallbackService interface {
	Handle(context.Context, string, providerv1.CallbackEnvelope) (provider.CallbackOutcome, error)
}

// ProviderCallbackRoutes owns the unauthenticated provider callback ingress.
// The opaque reference is the capability; provider crypto stays in the runner.
type ProviderCallbackRoutes struct {
	handlerBase
	service ProviderCallbackService
	limiter *callbackLimiter
}

// NewProviderCallbackRoutes constructs the provider callback ingress.
func NewProviderCallbackRoutes(service ProviderCallbackService, logger *slog.Logger) (*ProviderCallbackRoutes, error) {
	if service == nil || logger == nil {
		return nil, errors.New("provider callback route dependencies are required")
	}
	return &ProviderCallbackRoutes{
		service:     service,
		limiter:     newCallbackLimiter(callbackLimiterWindow, callbackLimiterBurst, callbackLimiterMaximum, time.Now),
		handlerBase: newHandlerBase(logger, "provider callback"),
	}, nil
}

// Register adds the exact opaque-reference callback endpoint.
func (routes *ProviderCallbackRoutes) Register(router chi.Router) {
	router.Post("/provider-callbacks/{token}", routes.receive)
}

func (routes *ProviderCallbackRoutes) receive(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Cache-Control", "no-store")
	token := chi.URLParam(request, "token")
	if _, err := id.ParseProviderCallback(token); err != nil {
		routes.problem(writer, request, provider.ErrCallbackUnavailable)
		return
	}
	if !routes.limiter.Allow(callbackLimiterKey(request, token)) {
		routes.problem(writer, request, apierror.New(
			http.StatusTooManyRequests,
			apierror.CodeRateLimited,
			"Too many requests",
			"Too many callback attempts for this reference.",
			nil,
		).WithRetryAfter(callbackLimiterWindow))
		return
	}
	envelope, err := providerCallbackEnvelope(writer, request)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	// The raw body is never logged or persisted; only normalized progress is
	// retained and the envelope is released after verification.
	outcome, err := routes.service.Handle(request.Context(), token, envelope)
	if err != nil {
		routes.problem(writer, request, err)
		return
	}
	status := http.StatusOK
	if outcome.Status == provider.CallbackPending {
		status = http.StatusAccepted
	}
	if err := respond.JSON(writer, request, status, struct {
		Status provider.CallbackStatus `json:"status"`
	}{Status: outcome.Status}); err != nil {
		routes.logger.ErrorContext(request.Context(), "write provider callback response")
	}
}

func providerCallbackEnvelope(writer http.ResponseWriter, request *http.Request) (providerv1.CallbackEnvelope, error) {
	if request.ContentLength > providerv1.MaxCallbackBodyBytes {
		return providerv1.CallbackEnvelope{}, apierror.New(
			http.StatusRequestEntityTooLarge,
			apierror.CodeRequestTooLarge,
			"Request too large",
			"The provider callback body exceeds the permitted size.",
			nil,
		)
	}
	body, err := io.ReadAll(io.LimitReader(http.MaxBytesReader(writer, request.Body, providerv1.MaxCallbackBodyBytes), providerv1.MaxCallbackBodyBytes+1))
	if err != nil || len(body) > providerv1.MaxCallbackBodyBytes {
		return providerv1.CallbackEnvelope{}, apierror.New(
			http.StatusRequestEntityTooLarge,
			apierror.CodeRequestTooLarge,
			"Request too large",
			"The provider callback body exceeds the permitted size.",
			err,
		)
	}
	headers, err := boundedCallbackHeaders(request.Header)
	if err != nil {
		return providerv1.CallbackEnvelope{}, err
	}
	envelope := providerv1.CallbackEnvelope{Method: request.Method, Headers: headers, Body: body}
	if err := envelope.Validate(); err != nil {
		return providerv1.CallbackEnvelope{}, provider.ErrCallbackInvalid
	}
	return envelope, nil
}

func boundedCallbackHeaders(source http.Header) ([]providerv1.CallbackHeader, error) {
	if len(source) > providerv1.MaxCallbackHeaders {
		return nil, provider.ErrCallbackInvalid
	}
	total := 0
	headers := make([]providerv1.CallbackHeader, 0, len(source))
	for name, values := range source {
		value := strings.Join(values, ", ")
		if name == "" || len(name) > providerv1.MaxCallbackHeaderNameBytes ||
			value == "" || len(value) > providerv1.MaxCallbackHeaderValueBytes {
			return nil, provider.ErrCallbackInvalid
		}
		total += len(name) + len(value)
		if total > callbackHeaderBytes {
			return nil, provider.ErrCallbackInvalid
		}
		headers = append(headers, providerv1.CallbackHeader{Name: name, Value: value})
	}
	return headers, nil
}

func callbackLimiterKey(request *http.Request, token string) string {
	sum := sha256.Sum256([]byte(token))
	host, _, err := net.SplitHostPort(request.RemoteAddr)
	if err != nil {
		host = request.RemoteAddr
	}
	return hex.EncodeToString(sum[:8]) + "|" + host
}

func (routes *ProviderCallbackRoutes) problem(writer http.ResponseWriter, request *http.Request, err error) {
	var failure *apierror.Error
	if !errors.As(err, &failure) {
		switch {
		case errors.Is(err, provider.ErrCallbackUnavailable):
			err = apierror.New(http.StatusNotFound, apierror.CodeNotFound, "Not found", "The callback reference is not available.", err)
		case errors.Is(err, provider.ErrCallbackInvalid):
			err = apierror.New(http.StatusBadRequest, apierror.CodeInvalidRequest, "Invalid request", "The provider callback was malformed.", err)
		case errors.Is(err, provider.ErrCallbackRejected):
			err = apierror.New(http.StatusForbidden, codeCallbackRejected, "Callback rejected", "The provider callback was rejected.", err)
		case errors.Is(err, provider.ErrCallbackConflict):
			err = apierror.New(http.StatusConflict, apierror.CodeConflict, "Conflict", "The provider callback conflicts with an accepted receipt.", err)
		default:
			err = apierror.New(http.StatusServiceUnavailable, apierror.CodeServiceUnavailable, "Service unavailable", "Provider callback verification is unavailable.", err)
		}
	}
	routes.handlerBase.problem(writer, request, err)
}

// callbackLimiter is a bounded in-process fixed-window limiter. It is the
// selected baseline when no shared cache is available; exceeding its size
// fails closed instead of growing without bound.
type callbackLimiter struct {
	mu      sync.Mutex
	entries map[string]callbackLimitEntry
	limit   int
	window  time.Duration
	maximum int
	now     func() time.Time
}

type callbackLimitEntry struct {
	count   int
	resetAt time.Time
	usedAt  time.Time
}

func newCallbackLimiter(window time.Duration, limit, maximum int, now func() time.Time) *callbackLimiter {
	return &callbackLimiter{entries: make(map[string]callbackLimitEntry), limit: limit, window: window, maximum: maximum, now: now}
}

func (limiter *callbackLimiter) Allow(key string) bool {
	now := limiter.now()
	limiter.mu.Lock()
	defer limiter.mu.Unlock()
	entry, exists := limiter.entries[key]
	if !exists || !now.Before(entry.resetAt) {
		if len(limiter.entries) >= limiter.maximum {
			limiter.evict(now)
			if len(limiter.entries) >= limiter.maximum {
				return false
			}
		}
		entry = callbackLimitEntry{resetAt: now.Add(limiter.window)}
	}
	if entry.count >= limiter.limit {
		return false
	}
	entry.count++
	entry.usedAt = now
	limiter.entries[key] = entry
	return true
}

func (limiter *callbackLimiter) evict(now time.Time) {
	oldestKey := ""
	var oldest callbackLimitEntry
	for key, entry := range limiter.entries {
		if !now.Before(entry.resetAt) {
			delete(limiter.entries, key)
			continue
		}
		if oldestKey == "" || entry.usedAt.Before(oldest.usedAt) {
			oldestKey, oldest = key, entry
		}
	}
	if oldestKey != "" {
		delete(limiter.entries, oldestKey)
	}
}
