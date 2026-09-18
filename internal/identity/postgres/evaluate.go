package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"

	"github.com/Mujhtech/idenqa/internal/identity"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypg "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// ProjectWithin pins identity interpretation in the existing authoritative policy transaction.
func (s *Store) ProjectWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, p policypg.Projection) ([]policy.Fact, error) {
	c, version, configDigest, e := configuration(ctx, tx, scope, p.Region, p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	if version == 0 || len(c.Requirements) == 0 {
		return nil, nil
	}
	receipt := identity.Receipt{VerificationID: p.VerificationID.String(), ConfigurationVersion: version, ConfigurationDigest: configDigest, EvaluatedAt: p.EvaluatedAt, Findings: []identity.Finding{}}
	var subjectID string
	e = tx.QueryRow(ctx, `SELECT subject_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND verification_id=$2`, scope.ID().String(), p.VerificationID.String()).Scan(&subjectID)
	complete := false
	var records []identity.Record
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if e == nil && c.Region == p.Region {
		subject, wrapped, _, err := loadSubject(ctx, tx, scope, subjectID, p.Region, false)
		if err != nil {
			return nil, err
		}
		receipt.SubjectID = subject.ID
		receipt.SubjectVersion = subject.Version
		if subject.State == "active" {
			if _, _, err = authorityProjection(ctx, tx, scope, subject.ID, p.VerificationID.String(), p.Region, p.EvaluatedAt); err != nil && !errors.Is(err, identity.ErrNotFound) {
				return nil, err
			}
			if err == nil {
				key, err := s.unwrap(ctx, wrapped, "identity.subject.v1", scope.ID().String(), subject.Region, subject.ID)
				if err != nil {
					return nil, err
				}
				defer clear(key)
				names := []string{}
				for _, rule := range c.Requirements {
					names = append(names, rule.FactName)
				}
				rows, err := tx.Query(ctx, `SELECT r.id FROM idenqa.identity_records r WHERE r.tenant_id=$1 AND r.subject_id=$2 AND r.kind='fact' AND r.name=ANY($3::text[]) AND r.recorded_at<=$4
 AND NOT EXISTS(SELECT 1 FROM idenqa.identity_records successor WHERE successor.tenant_id=r.tenant_id AND successor.supersedes=r.id AND successor.recorded_at<=$4) ORDER BY r.sequence,r.id LIMIT 2001`, scope.ID().String(), subject.ID, names, p.EvaluatedAt)
				if err != nil {
					return nil, err
				}
				var refs []string
				for rows.Next() {
					var ref string
					if err = rows.Scan(&ref); err != nil {
						rows.Close()
						return nil, err
					}
					refs = append(refs, ref)
				}
				err = rows.Err()
				rows.Close()
				if err != nil {
					return nil, err
				}
				complete = len(refs) <= identity.MaximumRecords
				if complete {
					for _, ref := range refs {
						r, value, err := s.loadRecord(ctx, tx, scope, subject, ref, key, p.EvaluatedAt, true)
						if err != nil {
							return nil, err
						}
						if r.Available {
							r.LineageRoots, err = currentProviderRoots(ctx, tx, scope, r, c)
							if err != nil {
								return nil, err
							}
							r.Value = &value.Value
						}
						records = append(records, r)
					}
				}
			}
		}
	}
	for _, rule := range c.Requirements {
		receipt.Findings = append(receipt.Findings, identity.Evaluate(rule, records, p.EvaluatedAt, complete))
	}
	digest, e := identity.Digest(receipt)
	if e != nil {
		return nil, e
	}
	b, e := json.Marshal(receipt)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_receipts(tenant_id,verification_id,subject_id,digest,receipt,recorded_at)VALUES($1,$2,NULLIF($3,''),$4,$5,$6) ON CONFLICT DO NOTHING`, scope.ID().String(), p.VerificationID.String(), receipt.SubjectID, digest, b, p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	facts := make([]policy.Fact, 0, len(receipt.Findings))
	for _, finding := range receipt.Findings {
		key, err := policy.NewFactKey(finding.Key)
		if err != nil {
			return nil, err
		}
		facts = append(facts, policy.Fact{Key: key, State: policy.RequirementState(finding.State), Source: policy.FactSource{Kind: policy.FactSourceIdentity, Identity: &policy.IdentitySource{ReceiptDigest: digest}}, ObservedAt: p.EvaluatedAt, ReasonCodes: []string{finding.Reason}})
	}
	return facts, nil
}

// New configuration may make an earlier source more correlated, but never erase
// recorded lineage. Revoked/unknown bindings share a conservative unknown root.
func currentProviderRoots(ctx context.Context, tx pg.Transaction, scope tenant.Scope, r identity.Record, c identity.Configuration) ([]string, error) {
	ids := union(r.AncestorIDs, []string{r.ID})
	rows, e := tx.Query(ctx, `SELECT metadata FROM idenqa.identity_records WHERE tenant_id=$1 AND subject_id=$2 AND id=ANY($3::text[]) AND kind='observation'`, scope.ID().String(), r.SubjectID, ids)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	roots := slices.Clone(r.LineageRoots)
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			return nil, e
		}
		var leaf identity.Record
		if e = json.Unmarshal(b, &leaf); e != nil {
			return nil, e
		}
		if leaf.SourceClass != "provider" {
			continue
		}
		found := false
		for _, binding := range c.ProviderSources {
			if binding.RunnerID == leaf.SourceName && binding.PackageDigest == leaf.PackageDigest {
				found = true
				for _, g := range binding.Groups {
					roots = union(roots, []string{"upstream." + g})
				}
			}
		}
		if !found {
			roots = union(roots, []string{"external.unclassified"})
		}
	}
	return roots, rows.Err()
}
