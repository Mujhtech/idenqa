// Package broker resolves exact versioned provider credentials through a
// regional workload broker. Its mTLS identity is independent of tenant API
// keys, deployment-agent trust and global control-plane authority.
package broker

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
)

// Options pins the broker origin, tenant and independently mounted mTLS identity.
type Options struct{ Endpoint, CertificateFile, KeyFile, CAFile, TenantID string }

// Resolver reads one exact regional provider credential generation.
type Resolver struct {
	origin, tenant string
	client         *http.Client
}

// Open constructs the pinned TLS client without granting ambient Cloud authority.
func Open(options Options) (*Resolver, error) {
	u, err := url.Parse(options.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || u.Path != "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || !strings.HasPrefix(options.TenantID, "ten_") {
		return nil, secret.ErrInvalid
	}
	stat, err := os.Lstat(options.KeyFile)
	if err != nil || !stat.Mode().IsRegular() || stat.Mode().Perm()&0077 != 0 {
		return nil, secret.ErrInvalid
	}
	certificate, err := tls.LoadX509KeyPair(options.CertificateFile, options.KeyFile)
	if err != nil {
		return nil, secret.ErrUnavailable
	}
	data, err := os.ReadFile(options.CAFile)
	if err != nil || len(data) > 64<<10 {
		return nil, secret.ErrUnavailable
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(data) {
		return nil, secret.ErrInvalid
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, Certificates: []tls.Certificate{certificate}}}
	return &Resolver{origin: options.Endpoint, tenant: options.TenantID, client: &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

// Resolve retrieves an exact version; the symbolic startup marker is resolved to a concrete generation first.
func (r *Resolver) Resolve(ctx context.Context, reference secret.Reference) (secret.Value, error) {
	if r == nil || ctx == nil || reference.Provider() != "broker" || reference.Version() == "" || strings.Count(reference.Path(), "/") != 1 {
		return secret.Value{}, secret.ErrInvalid
	}
	version := reference.Version()
	if version == "regional-current" {
		var err error
		version, err = r.CurrentVersion(ctx, "secret://broker"+reference.Path())
		if err != nil {
			return secret.Value{}, err
		}
	}
	requestBody, _ := json.Marshal(struct {
		AccountID    string `json:"accountId"`
		Version      string `json:"version"`
		CoreTenantID string `json:"coreTenantId"`
	}{strings.TrimPrefix(reference.Path(), "/"), version, r.tenant})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.origin+"/regional/v1/provider-credentials/resolve", bytes.NewReader(requestBody))
	if err != nil {
		return secret.Value{}, secret.ErrInvalid
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return secret.Value{}, secret.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusForbidden {
		return secret.Value{}, secret.ErrDenied
	}
	if response.StatusCode != http.StatusOK {
		return secret.Value{}, secret.ErrUnavailable
	}
	var result struct {
		Version string          `json:"version"`
		Value   json.RawMessage `json:"value"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, secret.MaxPayloadBytes+1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		clear(result.Value)
		return secret.Value{}, secret.ErrInvalid
	}
	defer clear(result.Value)
	if result.Version != version {
		return secret.Value{}, secret.ErrInvalid
	}
	return secret.NewValue(reference, result.Version, result.Value)
}

// CurrentVersion returns metadata only and is available to a separately
// authenticated Core worker. It does not release credential material.
func (r *Resolver) CurrentVersion(ctx context.Context, rawReference string) (string, error) {
	reference, err := secret.ParseReference(rawReference)
	if err != nil || reference.Provider() != "broker" || reference.Version() != "" || strings.Count(reference.Path(), "/") != 1 {
		return "", secret.ErrInvalid
	}
	data, _ := json.Marshal(struct {
		AccountID    string `json:"accountId"`
		CoreTenantID string `json:"coreTenantId"`
	}{strings.TrimPrefix(reference.Path(), "/"), r.tenant})
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, r.origin+"/regional/v1/provider-credentials/current", bytes.NewReader(data))
	if err != nil {
		return "", secret.ErrInvalid
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		return "", secret.ErrUnavailable
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", secret.ErrDenied
	}
	var result struct {
		Version string `json:"version"`
	}
	decoder := json.NewDecoder(io.LimitReader(response.Body, 1024))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&result) != nil || decoder.Decode(&struct{}{}) != io.EOF || result.Version == "" || len(result.Version) > 128 || strings.ContainsAny(result.Version, " /?&#\r\n") {
		return "", secret.ErrInvalid
	}
	return result.Version, nil
}
