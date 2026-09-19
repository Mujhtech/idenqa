package tenantexport_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
)

func TestParseCollections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		value   string
		want    []tenantexport.Collection
		wantErr bool
	}{
		{name: "empty selects all"},
		{name: "single", value: "tenant", want: []tenantexport.Collection{tenantexport.CollectionTenant}},
		{name: "ordered entries preserved verbatim", value: "policies,tenant", want: []tenantexport.Collection{tenantexport.CollectionPolicies, tenantexport.CollectionTenant}},
		{name: "unknown", value: "tenant,secrets", wantErr: true},
		{name: "duplicate", value: "tenant,tenant", wantErr: true},
		{name: "empty entry", value: "tenant,", wantErr: true},
		{name: "whitespace entry", value: "tenant, policies", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got, err := tenantexport.ParseCollections(test.value)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ParseCollections(%q) error = nil", test.value)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCollections(%q) error = %v", test.value, err)
			}
			if len(got) != len(test.want) {
				t.Fatalf("ParseCollections(%q) = %v, want %v", test.value, got, test.want)
			}
			for index := range got {
				if got[index] != test.want[index] {
					t.Fatalf("ParseCollections(%q) = %v, want %v", test.value, got, test.want)
				}
			}
		})
	}
}

func TestExporterStreamsCanonicalEnvelopeAndDigest(t *testing.T) {
	t.Parallel()

	fixture := newExportFixture(t)
	profiles := make([]tenantexport.Record, 0, 3)
	for index := range 3 {
		position := fmt.Sprintf("prf_%02d", index)
		profiles = append(profiles, tenantexport.Record{Position: position, Fields: map[string]string{"id": position}})
	}
	decisions := make([]tenantexport.Record, 0, 12)
	for index := range 12 {
		position := fmt.Sprintf("dec_%02d", index)
		decisions = append(decisions, tenantexport.Record{Position: position, Fields: map[string]string{"decision_id": position}})
	}
	fixture.sources.records[tenantexport.CollectionCaptureProfiles] = profiles
	fixture.sources.records[tenantexport.CollectionDecisions] = decisions

	selection := []tenantexport.Collection{
		tenantexport.CollectionTenant,
		tenantexport.CollectionCaptureProfiles,
		tenantexport.CollectionDecisions,
	}
	lines, err := fixture.export(t.Context(), selection)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if len(lines) != 1+1+3+12+1 {
		t.Fatalf("lines = %d, want %d", len(lines), 1+1+3+12+1)
	}
	for _, line := range lines {
		if !bytes.HasSuffix(line, []byte("\n")) {
			t.Fatalf("line %q does not end with a newline", line)
		}
	}
	if got := recordName(t, lines[0]); got != "header" {
		t.Fatalf("first record = %q, want header", got)
	}
	if got := recordName(t, lines[len(lines)-1]); got != "footer" {
		t.Fatalf("last record = %q, want footer", got)
	}
	if got := recordName(t, lines[1]); got != "tenant" {
		t.Fatalf("second record = %q, want tenant", got)
	}
	if got := recordName(t, lines[2]); got != "capture_profiles" {
		t.Fatalf("third record = %q, want capture_profiles", got)
	}
	if fixture.sources.calls[tenantexport.CollectionDecisions] != 2 {
		t.Fatalf("decision pages = %d, want 2", fixture.sources.calls[tenantexport.CollectionDecisions])
	}
	assertFooterDigest(t, lines)
	assertCount(t, lines, "tenant", 1)
	assertCount(t, lines, "capture_profiles", 3)
	assertCount(t, lines, "decisions", 12)
	counts := map[string]int64{}
	if err := json.Unmarshal(decodeLine(t, lines[len(lines)-1])["counts"], &counts); err != nil {
		t.Fatalf("decode footer counts: %v", err)
	}
	if len(counts) != 3 {
		t.Fatalf("counts = %v, want exactly the three selected collections", counts)
	}
}

func TestExporterHonoursCollectionFilter(t *testing.T) {
	t.Parallel()

	fixture := newExportFixture(t)
	fixture.sources.records[tenantexport.CollectionPolicies] = []tenantexport.Record{
		{Position: "pol_01", Fields: map[string]string{"id": "pol_01"}},
	}
	lines, err := fixture.export(t.Context(), []tenantexport.Collection{
		tenantexport.CollectionPolicies,
		tenantexport.CollectionTenant,
	})
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	header := decodeLine(t, lines[0])
	var collections []string
	if err := json.Unmarshal(header["collections"], &collections); err != nil {
		t.Fatalf("decode header collections: %v", err)
	}
	if len(collections) != 2 || collections[0] != "tenant" || collections[1] != "policies" {
		t.Fatalf("header collections = %v, want canonical order [tenant policies]", collections)
	}
	if fixture.sources.calls[tenantexport.CollectionDecisions] != 0 {
		t.Fatal("unselected decisions source was read")
	}
	for _, line := range lines[1 : len(lines)-1] {
		if name := recordName(t, line); name != "tenant" && name != "policies" {
			t.Fatalf("unexpected record %q in filtered export", name)
		}
	}
	assertFooterDigest(t, lines)
}

func TestExporterRejectsUnknownSelection(t *testing.T) {
	t.Parallel()

	fixture := newExportFixture(t)
	if _, err := fixture.export(t.Context(), []tenantexport.Collection{"secrets"}); err == nil {
		t.Fatal("Export(unknown collection) error = nil")
	}
}

func TestExporterRequiresExportAuthority(t *testing.T) {
	t.Parallel()

	fixture := newExportFixture(t)
	fixture.authority.allowed = false
	lines, err := fixture.export(t.Context(), []tenantexport.Collection{tenantexport.CollectionTenant})
	if !errors.Is(err, access.ErrInsufficientScope) {
		t.Fatalf("Export() error = %v, want insufficient scope", err)
	}
	if len(lines) != 0 {
		t.Fatalf("denied export emitted %d lines", len(lines))
	}
}

func TestExporterStopsOnCancellation(t *testing.T) {
	t.Parallel()

	fixture := newExportFixture(t)
	records := make([]tenantexport.Record, 0, 500)
	for index := range 500 {
		position := fmt.Sprintf("prf_%03d", index)
		records = append(records, tenantexport.Record{Position: position, Fields: map[string]string{"id": position}})
	}
	fixture.sources.records[tenantexport.CollectionCaptureProfiles] = records
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	fixture.onEmit = func(index int) {
		if index == 150 {
			cancel()
		}
	}
	lines, err := fixture.export(ctx, []tenantexport.Collection{tenantexport.CollectionCaptureProfiles})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Export() error = %v, want context.Canceled", err)
	}
	if len(lines) >= 1+500+1 {
		t.Fatalf("cancelled export produced %d lines", len(lines))
	}
}

func TestExporterPropagatesSourceAndEmitErrors(t *testing.T) {
	t.Parallel()

	fixture := newExportFixture(t)
	fixture.sources.err = errors.New("source unavailable")
	if _, err := fixture.export(t.Context(), []tenantexport.Collection{tenantexport.CollectionCaptureProfiles}); !errors.Is(err, fixture.sources.err) {
		t.Fatalf("Export(source error) = %v", err)
	}
	fixture = newExportFixture(t)
	emitErr := errors.New("client disconnected")
	fixture.emitErr = emitErr
	if _, err := fixture.export(t.Context(), []tenantexport.Collection{tenantexport.CollectionTenant}); !errors.Is(err, emitErr) {
		t.Fatalf("Export(emit error) = %v", err)
	}
}

func TestNewExporterRejectsIncompleteSources(t *testing.T) {
	t.Parallel()

	if _, err := tenantexport.NewExporter(tenantexport.Sources{}, nil); !errors.Is(err, tenantexport.ErrInvalid) {
		t.Fatalf("NewExporter(incomplete) error = %v", err)
	}
}

type exportAuthority struct {
	scope   tenant.Scope
	allowed bool
}

func (authority exportAuthority) TenantScope() tenant.Scope { return authority.scope }

func (authority exportAuthority) Require(access.Permission) error {
	if !authority.allowed {
		return access.ErrInsufficientScope
	}
	return nil
}

type exportFixture struct {
	exporter  *tenantexport.Exporter
	authority exportAuthority
	sources   *exportSources
	onEmit    func(int)
	emitErr   error
}

func newExportFixture(t *testing.T) *exportFixture {
	t.Helper()

	identifiers, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatalf("NewSystemGenerator() error = %v", err)
	}
	tenantID, err := identifiers.NewTenant()
	if err != nil {
		t.Fatalf("NewTenant() error = %v", err)
	}
	scope, err := tenant.NewScope(tenantID)
	if err != nil {
		t.Fatalf("NewScope() error = %v", err)
	}
	sources := &exportSources{
		records: map[tenantexport.Collection][]tenantexport.Record{
			tenantexport.CollectionTenant: {{Position: tenantID.String(), Fields: map[string]string{"id": tenantID.String()}}},
		},
		calls: map[tenantexport.Collection]int{},
	}
	exporter, err := tenantexport.NewExporter(sources.sources(), func() time.Time {
		return time.Date(2026, time.September, 19, 12, 0, 0, 0, time.UTC)
	})
	if err != nil {
		t.Fatalf("NewExporter() error = %v", err)
	}
	return &exportFixture{
		exporter:  exporter,
		authority: exportAuthority{scope: scope, allowed: true},
		sources:   sources,
	}
}

func (fixture *exportFixture) export(ctx context.Context, selection []tenantexport.Collection) ([][]byte, error) {
	lines := [][]byte{}
	index := 0
	err := fixture.exporter.Export(ctx, fixture.authority, selection, func(line []byte) error {
		if fixture.emitErr != nil {
			return fixture.emitErr
		}
		if fixture.onEmit != nil {
			fixture.onEmit(index)
		}
		index++
		lines = append(lines, bytes.Clone(line))
		return nil
	})
	return lines, err
}

type exportSources struct {
	records map[tenantexport.Collection][]tenantexport.Record
	calls   map[tenantexport.Collection]int
	err     error
}

func (sources *exportSources) sources() tenantexport.Sources {
	return tenantexport.Sources{
		Tenant:                  sources,
		CaptureProfiles:         sources,
		Policies:                sources,
		PolicyRevisions:         sources,
		PolicyActivations:       sources,
		Verifications:           sources,
		VerificationTransitions: sources,
		VerificationChecks:      sources,
		VerificationAttempts:    sources,
		Decisions:               sources,
		AuditRecords:            sources,
		WebhookEndpoints:        sources,
		ReviewCases:             sources,
		ReviewFindings:          sources,
		IdentitySubjects:        sources,
		IdentityRecords:         sources,
		EvidenceAssets:          sources,
		FraudConfiguration:      sources,
		PrivacyDeletions:        sources,
		PrivacyHolds:            sources,
	}
}

func (sources *exportSources) page(collection tenantexport.Collection, after string, limit int) ([]tenantexport.Record, error) {
	sources.calls[collection]++
	if sources.err != nil {
		return nil, sources.err
	}
	result := []tenantexport.Record{}
	for _, record := range sources.records[collection] {
		if after != "" && record.Position <= after {
			continue
		}
		result = append(result, record)
		if len(result) == limit {
			break
		}
	}
	return result, nil
}

func (sources *exportSources) Tenant(context.Context, tenant.Scope) (tenantexport.Record, error) {
	sources.calls[tenantexport.CollectionTenant]++
	if sources.err != nil {
		return tenantexport.Record{}, sources.err
	}
	records := sources.records[tenantexport.CollectionTenant]
	if len(records) == 0 {
		return tenantexport.Record{}, tenantexport.ErrInvalid
	}
	return records[0], nil
}

func (sources *exportSources) CaptureProfiles(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionCaptureProfiles, after, limit)
}

func (sources *exportSources) Policies(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionPolicies, after, limit)
}

func (sources *exportSources) PolicyRevisions(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionPolicyRevisions, after, limit)
}

func (sources *exportSources) PolicyActivations(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionPolicyActivations, after, limit)
}

func (sources *exportSources) Verifications(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionVerifications, after, limit)
}

func (sources *exportSources) VerificationTransitions(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionVerificationTransitions, after, limit)
}

func (sources *exportSources) VerificationChecks(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionVerificationChecks, after, limit)
}

func (sources *exportSources) VerificationAttempts(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionVerificationAttempts, after, limit)
}

func (sources *exportSources) Decisions(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionDecisions, after, limit)
}

func (sources *exportSources) AuditRecords(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionAuditRecords, after, limit)
}

func (sources *exportSources) WebhookEndpoints(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionWebhookEndpoints, after, limit)
}

func (sources *exportSources) ReviewCases(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionReviewCases, after, limit)
}

func (sources *exportSources) ReviewFindings(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionReviewFindings, after, limit)
}

func (sources *exportSources) IdentitySubjects(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionIdentitySubjects, after, limit)
}

func (sources *exportSources) IdentityRecords(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionIdentityRecords, after, limit)
}

func (sources *exportSources) EvidenceAssets(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionEvidenceAssets, after, limit)
}

func (sources *exportSources) FraudConfiguration(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionFraudConfiguration, after, limit)
}

func (sources *exportSources) PrivacyDeletions(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionPrivacyDeletions, after, limit)
}

func (sources *exportSources) PrivacyHolds(_ context.Context, _ tenant.Scope, after string, limit int) ([]tenantexport.Record, error) {
	return sources.page(tenantexport.CollectionPrivacyHolds, after, limit)
}

func recordName(t *testing.T, line []byte) string {
	t.Helper()

	fields := decodeLine(t, line)
	var name string
	if err := json.Unmarshal(fields["record"], &name); err != nil {
		t.Fatalf("decode record name: %v", err)
	}
	return name
}

func decodeLine(t *testing.T, line []byte) map[string]json.RawMessage {
	t.Helper()

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(bytes.TrimSuffix(line, []byte("\n")), &fields); err != nil {
		t.Fatalf("decode line %q: %v", line, err)
	}
	return fields
}

func assertFooterDigest(t *testing.T, lines [][]byte) {
	t.Helper()

	footer := decodeLine(t, lines[len(lines)-1])
	var digest string
	if err := json.Unmarshal(footer["digest"], &digest); err != nil {
		t.Fatalf("decode footer digest: %v", err)
	}
	if !strings.HasPrefix(digest, "sha256:") || len(digest) != len("sha256:")+64 {
		t.Fatalf("footer digest = %q", digest)
	}
	hash := sha256.New()
	for _, line := range lines[:len(lines)-1] {
		if _, err := hash.Write(line); err != nil {
			t.Fatalf("hash export line: %v", err)
		}
	}
	if want := "sha256:" + hex.EncodeToString(hash.Sum(nil)); digest != want {
		t.Fatalf("footer digest = %q, want %q", digest, want)
	}
}

func assertCount(t *testing.T, lines [][]byte, collection string, want int64) {
	t.Helper()

	footer := decodeLine(t, lines[len(lines)-1])
	counts := map[string]int64{}
	if err := json.Unmarshal(footer["counts"], &counts); err != nil {
		t.Fatalf("decode footer counts: %v", err)
	}
	if got := counts[collection]; got != want {
		t.Fatalf("footer count %s = %d, want %d", collection, got, want)
	}
}
