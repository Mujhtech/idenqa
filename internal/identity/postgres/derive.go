package postgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func (s *Store) importObservation(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, r *identity.Record, origin string, at time.Time) (identity.ProtectedValue, error) {
	var outcome string
	var recorded time.Time
	e := tx.QueryRow(ctx, `SELECT o.runner_kind,o.runner_id,o.check_id,o.attempt_id,o.package_digest,o.request_digest,o.configuration_digest,o.signal_outcome,o.recorded_at
 FROM idenqa.verification_observations o JOIN idenqa.verification_checks c ON c.tenant_id=o.tenant_id AND c.id=o.check_id
 JOIN idenqa.verification_attempts a ON a.tenant_id=o.tenant_id AND a.id=o.attempt_id
 WHERE o.tenant_id=$1 AND o.id=$2 AND o.verification_id=$3 AND c.state='completed' AND a.state='completed' AND o.recorded_at<=$4`, scope.ID().String(), origin, r.VerificationID, at).Scan(&r.SourceClass, &r.SourceName, &r.CheckID, &r.AttemptID, &r.PackageDigest, &r.RequestDigest, &r.ConfigurationDigest, &outcome, &recorded)
	if errors.Is(e, pgx.ErrNoRows) {
		return identity.ProtectedValue{}, identity.ErrNotFound
	}
	if e != nil {
		return identity.ProtectedValue{}, e
	}
	r.OriginObservationID = origin
	r.OriginOutcome = outcome
	r.SourceClasses = []string{r.SourceClass}
	r.InputVersion = "identity.check_import.v1"
	// Importing an existing observation cannot refresh its collection timestamp.
	r.ObservedAt = recorded.UTC()
	r.CollectedAt = recorded.UTC()
	r.LineageRoots = []string{"check." + r.CheckID, "request." + r.RequestDigest}
	if r.SourceClass == "provider" {
		config, version, _, e := configuration(ctx, tx, scope, subject.Region, at)
		if e != nil {
			return identity.ProtectedValue{}, e
		}
		r.SourceConfigurationVersion = version
		matched := false
		if config.Region == subject.Region {
			for _, binding := range config.ProviderSources {
				if binding.RunnerID == r.SourceName && binding.PackageDigest == r.PackageDigest {
					for _, g := range binding.Groups {
						r.LineageRoots = append(r.LineageRoots, "upstream."+g)
					}
					matched = true
				}
			}
		}
		if !matched {
			r.LineageRoots = append(r.LineageRoots, "external.unclassified")
		}
	} else {
		r.LineageRoots = append(r.LineageRoots, "capture."+r.VerificationID)
	}
	// Until every runner has a field-level input manifest, all available capture
	// artefacts are conservatively treated as potential shared inputs.
	rows, e := tx.Query(ctx, `SELECT id FROM idenqa.evidence_assets WHERE tenant_id=$1 AND verification_id=$2 AND created_at<=$3 ORDER BY id LIMIT 65`, scope.ID().String(), r.VerificationID, at)
	if e != nil {
		return identity.ProtectedValue{}, e
	}
	defer rows.Close()
	for rows.Next() {
		var evidence string
		if e = rows.Scan(&evidence); e != nil {
			return identity.ProtectedValue{}, e
		}
		r.EvidenceIDs = append(r.EvidenceIDs, evidence)
	}
	if e = rows.Err(); e != nil {
		return identity.ProtectedValue{}, e
	}
	if len(r.EvidenceIDs) > identity.MaximumSources {
		return identity.ProtectedValue{}, identity.ErrUnavailable
	}
	value := "false"
	if outcome == "satisfied" {
		value = "true"
	}
	return identity.ProtectedValue{Value: identity.Value{Type: "boolean", Text: value}}, nil
}

func (s *Store) derive(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, r *identity.Record, key []byte, at time.Time) (identity.ProtectedValue, error) {
	var secret identity.ProtectedValue
	for i, sourceID := range r.SourceRecordIDs {
		parent, p, e := s.loadRecord(ctx, tx, scope, subject, sourceID, key, at, true)
		if e != nil {
			return secret, e
		}
		if !parent.Current || !parent.Available {
			return secret, identity.ErrConflict
		}
		if (r.Kind == "fact" && parent.Kind != "fact" && parent.Kind != "observation") || (r.Kind != "fact" && (parent.Kind != "fact" || parent.Name != r.Name)) {
			return secret, identity.ErrInvalid
		}
		value, e := identity.Normalize(p.Value, r.Normalization)
		if e != nil {
			return secret, e
		}
		if i == 0 {
			secret.Value = value
			secret.Original = p.Original
			if secret.Original == nil {
				original := p.Value
				secret.Original = &original
			}
		} else if value != secret.Value {
			return secret, identity.ErrConflict
		}
		r.EvidenceIDs = union(r.EvidenceIDs, parent.EvidenceIDs)
		r.LineageRoots = union(r.LineageRoots, parent.LineageRoots)
		r.AncestorIDs = union(r.AncestorIDs, append(slices.Clone(parent.AncestorIDs), parent.ID))
		r.SourceClasses = union(r.SourceClasses, parent.SourceClasses)
		if parent.CollectedAt.Before(r.CollectedAt) {
			r.CollectedAt = parent.CollectedAt
		}
		if parent.ObservedAt.Before(r.ObservedAt) {
			r.ObservedAt = parent.ObservedAt
		}
		if parent.ValidFrom.After(r.ValidFrom) {
			r.ValidFrom = parent.ValidFrom
		}
		if parent.ValidUntil.Before(r.ValidUntil) {
			r.ValidUntil = parent.ValidUntil
		}
		if parent.RetainUntil.Before(r.RetainUntil) {
			r.RetainUntil = parent.RetainUntil
		}
		if parent.FreshUntil == nil {
			r.FreshUntil = nil
		} else if r.FreshUntil != nil && parent.FreshUntil.Before(*r.FreshUntil) {
			fresh := *parent.FreshUntil
			r.FreshUntil = &fresh
		}
		if r.Unit != parent.Unit {
			return secret, identity.ErrInvalid
		}
		if parent.ConfidenceBPS == nil {
			if r.ConfidenceBPS != nil {
				return secret, identity.ErrInvalid
			}
		} else if r.ConfidenceBPS != nil && *r.ConfidenceBPS > *parent.ConfidenceBPS {
			return secret, identity.ErrInvalid
		}
	}
	if !r.ValidUntil.After(r.ValidFrom) || !r.RetainUntil.After(at) {
		return secret, identity.ErrConflict
	}
	r.SourceClass = "mixed"
	if len(r.SourceClasses) == 1 {
		r.SourceClass = r.SourceClasses[0]
	}
	r.SourceName = "identity.derived"
	r.InputVersion = "identity.records.v1"
	return secret, nil
}

func (s *Store) evidenceRoots(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, r *identity.Record, at time.Time) error {
	if len(r.EvidenceIDs) == 0 {
		return nil
	}
	lookup, e := s.lookupKey(ctx, tx, scope, subject.Region, true)
	if e != nil {
		return e
	}
	defer clear(lookup)
	for _, evidenceID := range r.EvidenceIDs {
		var digest, verification string
		e := tx.QueryRow(ctx, `SELECT e.plaintext_digest,e.verification_id FROM idenqa.evidence_assets e
 JOIN idenqa.identity_subject_verifications l ON l.tenant_id=e.tenant_id AND l.verification_id=e.verification_id
 JOIN idenqa.verification_sessions v ON v.tenant_id=e.tenant_id AND v.id=e.verification_id
 JOIN idenqa.processing_authorities a ON a.tenant_id=v.tenant_id AND a.id=v.authority_id
 WHERE e.tenant_id=$1 AND e.id=$2 AND l.subject_id=$3 AND e.region=$4 AND e.state='available' AND e.integrity='verified' AND e.created_at<=$5
 AND e.evidence_type=ANY(a.evidence_types) FOR SHARE OF e,a`, scope.ID().String(), evidenceID, subject.ID, subject.Region, at).Scan(&digest, &verification)
		if errors.Is(e, pgx.ErrNoRows) {
			return identity.ErrNotFound
		}
		if e != nil {
			return e
		}
		if _, _, e = authorityProjection(ctx, tx, scope, subject.ID, verification, subject.Region, at); e != nil {
			return e
		}
		group, e := token(lookup, "identity.evidence_lineage.v1", scope.ID().String(), subject.Region, digest)
		if e != nil {
			return e
		}
		r.LineageRoots = union(r.LineageRoots, []string{"evidence." + group, "capture." + verification})
	}
	return nil
}
