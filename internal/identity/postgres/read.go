package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func (s *Store) loadRecord(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, recordID string, key []byte, at time.Time, strict bool) (identity.Record, identity.ProtectedValue, error) {
	var r identity.Record
	var secret identity.ProtectedValue
	var b, sealed []byte
	var current bool
	e := tx.QueryRow(ctx, `SELECT r.metadata,v.ciphertext,NOT EXISTS(SELECT 1 FROM idenqa.identity_records successor WHERE successor.tenant_id=r.tenant_id AND successor.supersedes=r.id)
 FROM idenqa.identity_records r LEFT JOIN idenqa.identity_record_values v ON v.tenant_id=r.tenant_id AND v.record_id=r.id
 WHERE r.tenant_id=$1 AND r.subject_id=$2 AND r.id=$3`, scope.ID().String(), subject.ID, recordID).Scan(&b, &sealed, &current)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, secret, identity.ErrNotFound
	}
	if e != nil {
		return r, secret, e
	}
	if e = json.Unmarshal(b, &r); e != nil {
		return r, secret, e
	}
	if r.ID != recordID || r.SubjectID != subject.ID || len(r.AncestorIDs) > identity.MaximumSources || r.SchemaVersion != 1 {
		return r, secret, identity.ErrUnavailable
	}
	r.Current = current
	r.Freshness = "unknown"
	if !at.Before(r.ValidUntil) {
		r.Freshness = "expired"
	} else if r.FreshUntil != nil {
		r.Freshness = "fresh"
		if !at.Before(*r.FreshUntil) {
			r.Freshness = "stale"
		}
	}
	if len(sealed) == 0 || subject.State != "active" || subject.ErasedAt != nil {
		return r, secret, nil
	}
	r.Available, e = recordEligible(ctx, tx, scope, subject, r, at, strict)
	if e != nil {
		return r, secret, e
	}
	if !r.Available || len(key) == 0 {
		return r, secret, nil
	}
	aad, e := contextBytes("identity.record."+r.Kind+".v1", scope.ID().String(), subject.Region, r.ID)
	if e != nil {
		return r, secret, e
	}
	plain, e := openValue(key, aad, sealed)
	if e != nil {
		return r, secret, e
	}
	defer clear(plain)
	if e = json.Unmarshal(plain, &secret); e != nil {
		return r, secret, identity.ErrUnavailable
	}
	if secret.Value.Validate() != nil || secret.Value.Type != r.ValueType {
		return r, secret, identity.ErrUnavailable
	}
	if r.Identifier != nil {
		r.Identifier.Masked = identity.MaskIdentifier(secret.Value.Text)
	}
	return r, secret, nil
}

func recordEligible(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, r identity.Record, at time.Time, strict bool) (bool, error) {
	ids := union(r.AncestorIDs, []string{r.ID})
	var count int
	e := tx.QueryRow(ctx, `SELECT count(*) FROM idenqa.identity_records r
 JOIN idenqa.identity_record_values value ON value.tenant_id=r.tenant_id AND value.record_id=r.id
 JOIN idenqa.verification_sessions s ON s.tenant_id=r.tenant_id AND s.id=r.verification_id
 JOIN idenqa.processing_authorities a ON a.tenant_id=s.tenant_id AND a.id=s.authority_id
 WHERE r.tenant_id=$1 AND r.subject_id=$2 AND r.id=ANY($3::text[]) AND r.recorded_at<=$4
 AND r.retain_until>$4 AND r.retain_until>clock_timestamp() AND s.region=$5
 AND a.id=r.metadata->>'authority_id' AND a.state='active' AND a.valid_from<=$4 AND a.expires_at>$4 AND a.expires_at>clock_timestamp()
 AND 'idenqa.purpose.identity_verification'=ANY(a.requirement_purposes) AND $5=ANY(a.regions)
 AND (SELECT action FROM idenqa.subject_responses WHERE tenant_id=a.tenant_id AND authority_id=a.id AND recorded_at<=$4 ORDER BY recorded_at DESC,id DESC LIMIT 1)
 IN ('consent',CASE WHEN NOT a.consent_required THEN 'acknowledge' ELSE 'consent' END)
 AND (NOT $6 OR (coalesce(r.metadata->>'origin_outcome','')<>'inconclusive' AND (r.metadata->>'valid_from')::timestamptz<=$4 AND (r.metadata->>'valid_until')::timestamptz>$4
 AND NOT EXISTS(SELECT 1 FROM idenqa.identity_records newer WHERE newer.tenant_id=r.tenant_id AND newer.supersedes=r.id)))
 AND NOT EXISTS(SELECT 1 FROM idenqa.identity_record_evidence l JOIN idenqa.evidence_assets e ON e.tenant_id=l.tenant_id AND e.id=l.evidence_id
 JOIN idenqa.verification_sessions ev ON ev.tenant_id=e.tenant_id AND ev.id=e.verification_id
 JOIN idenqa.processing_authorities ea ON ea.tenant_id=ev.tenant_id AND ea.id=ev.authority_id
 WHERE l.tenant_id=r.tenant_id AND l.record_id=r.id AND (e.state<>'available' OR e.integrity<>'verified' OR e.region<>$5 OR NOT(e.evidence_type=ANY(ea.evidence_types)) OR ea.state<>'active' OR ea.valid_from>$4 OR ea.expires_at<=$4 OR ea.expires_at<=clock_timestamp()
 OR NOT('idenqa.purpose.identity_verification'=ANY(ea.requirement_purposes)) OR NOT($5=ANY(ea.regions))
 OR COALESCE((SELECT action FROM idenqa.subject_responses WHERE tenant_id=ea.tenant_id AND authority_id=ea.id AND recorded_at<=$4 ORDER BY recorded_at DESC,id DESC LIMIT 1),'') NOT IN('consent',CASE WHEN NOT ea.consent_required THEN 'acknowledge' ELSE 'consent' END)))`, scope.ID().String(), subject.ID, ids, at, subject.Region, strict).Scan(&count)
	return count == len(ids), e
}

// Read returns a bounded snapshot and audits every successful explicit value reveal.
func (s *Store) Read(ctx context.Context, scope tenant.Scope, q identity.Query) (identity.Result, error) {
	var result identity.Result
	e := s.pool.WithinTransaction(ctx, pg.TransactionOptions{Isolation: pg.IsolationRepeatableRead}, func(ctx context.Context, tx pg.Transaction) error {
		if e := setScope(ctx, tx, scope); e != nil {
			return e
		}
		var e error
		switch q.Kind {
		case "configuration":
			var c identity.Configuration
			var v int64
			var digest string
			if q.Reference != "" {
				version, err := strconv.ParseInt(q.Reference, 10, 64)
				if err != nil || version < 1 {
					return identity.ErrInvalid
				}
				var b []byte
				e = tx.QueryRow(ctx, `SELECT version,configuration,digest FROM idenqa.identity_configurations WHERE tenant_id=$1 AND version=$2 AND region=$3`, scope.ID().String(), version, q.Region).Scan(&v, &b, &digest)
				if errors.Is(e, pgx.ErrNoRows) {
					return identity.ErrNotFound
				}
				if e != nil {
					return e
				}
				if e = json.Unmarshal(b, &c); e != nil {
					return e
				}
				actual, err := identity.Digest(c)
				if err != nil {
					return err
				}
				if actual != digest || c.Validate() != nil {
					return identity.ErrUnavailable
				}
			} else {
				c, v, digest, e = configuration(ctx, tx, scope, q.Region, q.At)
			}
			if e != nil {
				return e
			}
			if v == 0 || c.Region != q.Region {
				return identity.ErrNotFound
			}
			result = identity.Result{Version: v, Configuration: &c, Digest: digest}
		case "receipt":
			var b []byte
			e = tx.QueryRow(ctx, `SELECT r.receipt FROM idenqa.identity_receipts r JOIN idenqa.verification_sessions v ON v.tenant_id=r.tenant_id AND v.id=r.verification_id WHERE r.tenant_id=$1 AND r.digest=$2 AND v.region=$3`, scope.ID().String(), q.Reference, q.Region).Scan(&b)
			if errors.Is(e, pgx.ErrNoRows) {
				return identity.ErrNotFound
			}
			if e != nil {
				return e
			}
			var receipt identity.Receipt
			if e = json.Unmarshal(b, &receipt); e != nil {
				return e
			}
			digest, err := identity.Digest(receipt)
			if err != nil {
				return err
			}
			if digest != q.Reference {
				return identity.ErrUnavailable
			}
			result = identity.Result{Receipt: &receipt, Digest: digest}
		case "subjects", "external_lookup", "identifier_lookup":
			result, e = s.subjects(ctx, tx, scope, q)
		default:
			result, e = s.readSubject(ctx, tx, scope, q)
		}
		if e != nil {
			return e
		}
		if q.Reveal {
			// The audit digest binds only the requested references, never returned values.
			digest, err := identity.Digest([]string{q.Kind, q.SubjectID, q.Reference, q.After})
			if err != nil {
				return err
			}
			aggregate := q.SubjectID
			if aggregate == "" {
				aggregate = "identity.read"
			}
			return s.event(ctx, tx, scope, q.Actor.String(), aggregate, 1, "identity.reveal", digest, q.At)
		}
		return nil
	})
	if e != nil {
		return identity.Result{}, fmt.Errorf("read identity resource: %w", e)
	}
	return result, nil
}

func (s *Store) readSubject(ctx context.Context, tx pg.Transaction, scope tenant.Scope, q identity.Query) (identity.Result, error) {
	subject, wrapped, external, e := loadSubject(ctx, tx, scope, q.SubjectID, q.Region, false)
	if e != nil {
		return identity.Result{}, e
	}
	result := identity.Result{Subject: &subject}
	if q.Kind == "subject" {
		if q.Reveal && len(external) > 0 {
			if subject.State != "active" {
				return result, identity.ErrConflict
			}
			key, e := s.unwrap(ctx, wrapped, "identity.subject.v1", scope.ID().String(), subject.Region, subject.ID)
			if e != nil {
				return result, e
			}
			defer clear(key)
			aad, e := contextBytes("identity.external.v1", scope.ID().String(), subject.Region, subject.ID)
			if e != nil {
				return result, e
			}
			plain, e := openValue(key, aad, external)
			if e != nil {
				return result, e
			}
			defer clear(plain)
			reference := string(plain)
			subject.ExternalReference = &reference
		}
		return result, nil
	}
	if q.Kind == "verifications" {
		result.VerificationIDs = []string{}
		rows, e := tx.Query(ctx, `SELECT verification_id FROM idenqa.identity_subject_verifications WHERE tenant_id=$1 AND subject_id=$2 AND verification_id>$3 ORDER BY verification_id LIMIT $4`, scope.ID().String(), subject.ID, q.After, q.Limit+1)
		if e != nil {
			return result, e
		}
		defer rows.Close()
		for rows.Next() {
			var v string
			if e = rows.Scan(&v); e != nil {
				return result, e
			}
			result.VerificationIDs = append(result.VerificationIDs, v)
		}
		if e = rows.Err(); e != nil {
			return result, e
		}
		if len(result.VerificationIDs) > q.Limit {
			result.VerificationIDs = result.VerificationIDs[:q.Limit]
			result.NextCursor = result.VerificationIDs[q.Limit-1]
		}
		return result, nil
	}
	var key []byte
	if subject.State == "active" && len(wrapped) > 0 {
		key, e = s.unwrap(ctx, wrapped, "identity.subject.v1", scope.ID().String(), subject.Region, subject.ID)
		if e != nil {
			return result, e
		}
		defer clear(key)
	}
	if q.Kind == "record" {
		r, value, e := s.loadRecord(ctx, tx, scope, subject, q.Reference, key, q.At, false)
		if e != nil {
			return result, e
		}
		if q.Reveal && r.Available {
			r.Value = &value.Value
			r.Original = value.Original
		}
		result.Record = &r
		return result, nil
	}
	sequence := int64(0)
	if q.After != "" {
		e = tx.QueryRow(ctx, `SELECT sequence FROM idenqa.identity_records WHERE tenant_id=$1 AND subject_id=$2 AND id=$3`, scope.ID().String(), subject.ID, q.After).Scan(&sequence)
		if errors.Is(e, pgx.ErrNoRows) {
			return result, identity.ErrNotFound
		}
		if e != nil {
			return result, e
		}
	}
	rows, e := tx.Query(ctx, `SELECT r.id FROM idenqa.identity_records r WHERE r.tenant_id=$1 AND r.subject_id=$2 AND r.sequence>$3 AND ($4='' OR r.kind=$4)
 AND (NOT $5 OR NOT EXISTS(SELECT 1 FROM idenqa.identity_records successor WHERE successor.tenant_id=r.tenant_id AND successor.supersedes=r.id)) ORDER BY r.sequence,r.id LIMIT $6`, scope.ID().String(), subject.ID, sequence, q.RecordKind, q.Current, q.Limit+1)
	if e != nil {
		return result, e
	}
	var refs []string
	for rows.Next() {
		var ref string
		if e = rows.Scan(&ref); e != nil {
			rows.Close()
			return result, e
		}
		refs = append(refs, ref)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return result, e
	}
	if len(refs) > q.Limit {
		refs = refs[:q.Limit]
		result.NextCursor = refs[q.Limit-1]
	}
	result.Records = []identity.Record{}
	for _, ref := range refs {
		r, value, e := s.loadRecord(ctx, tx, scope, subject, ref, key, q.At, false)
		if e != nil {
			return result, e
		}
		if q.Reveal && r.Available {
			r.Value = &value.Value
			r.Original = value.Original
		}
		result.Records = append(result.Records, r)
	}
	return result, nil
}

func (s *Store) subjects(ctx context.Context, tx pg.Transaction, scope tenant.Scope, q identity.Query) (identity.Result, error) {
	var lookup string
	var e error
	if q.Kind == "external_lookup" || q.Kind == "identifier_lookup" {
		key, err := s.lookupKey(ctx, tx, scope, q.Region, false)
		if errors.Is(err, identity.ErrNotFound) {
			return identity.Result{Subjects: []identity.Subject{}}, nil
		}
		if err != nil {
			return identity.Result{}, err
		}
		defer clear(key)
		if q.Kind == "external_lookup" {
			lookup, e = token(key, "identity.external.v1", scope.ID().String(), q.Region, q.ExternalReference)
		} else {
			value, err := identity.Normalize(identity.Value{Type: "string", Text: q.Identifier.Value}, q.Identifier.Normalization)
			if err != nil {
				return identity.Result{}, err
			}
			lookup, e = token(key, "identity.identifier.v1", scope.ID().String(), q.Region, q.Identifier.Namespace, q.Identifier.Issuer, q.Identifier.Normalization, value.Text)
		}
		if e != nil {
			return identity.Result{}, e
		}
	}
	namespace, issuer := "", ""
	if q.Identifier != nil {
		namespace = q.Identifier.Namespace
		issuer = q.Identifier.Issuer
	}
	rows, e := tx.Query(ctx, `SELECT s.id FROM idenqa.identity_subjects s WHERE s.tenant_id=$1 AND s.region=$2 AND s.id>$3
 AND ($4<>'external_lookup' OR (s.state='active' AND s.external_token=$5))
 AND ($4<>'identifier_lookup' OR (s.state='active' AND EXISTS(SELECT 1 FROM idenqa.identity_identifier_tokens t
 JOIN idenqa.identity_records r ON r.tenant_id=t.tenant_id AND r.id=t.record_id
 JOIN idenqa.identity_record_values v ON v.tenant_id=r.tenant_id AND v.record_id=r.id
 WHERE t.tenant_id=s.tenant_id AND t.subject_id=s.id AND t.region=s.region AND t.namespace=$6 AND t.issuer=$7 AND t.token=$5
 AND r.retain_until>$8 AND NOT EXISTS(SELECT 1 FROM idenqa.identity_records successor WHERE successor.tenant_id=r.tenant_id AND successor.supersedes=r.id))))
 ORDER BY s.id LIMIT $9`, scope.ID().String(), q.Region, q.After, q.Kind, lookup, namespace, issuer, q.At, q.Limit+1)
	if e != nil {
		return identity.Result{}, e
	}
	var ids []string
	for rows.Next() {
		var subjectID string
		if e = rows.Scan(&subjectID); e != nil {
			rows.Close()
			return identity.Result{}, e
		}
		ids = append(ids, subjectID)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return identity.Result{}, e
	}
	result := identity.Result{Subjects: []identity.Subject{}}
	if len(ids) > q.Limit {
		ids = ids[:q.Limit]
		result.NextCursor = ids[q.Limit-1]
	}
	for _, subjectID := range ids {
		subject, _, _, err := loadSubject(ctx, tx, scope, subjectID, q.Region, false)
		if err != nil {
			return result, err
		}
		if q.Kind == "identifier_lookup" {
			ok, err := identifierEligible(ctx, tx, scope, subject, q, lookup)
			if err != nil {
				return result, err
			}
			if !ok {
				continue
			}
		}
		result.Subjects = append(result.Subjects, subject)
	}
	return result, nil
}

func identifierEligible(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject identity.Subject, q identity.Query, lookup string) (bool, error) {
	rows, e := tx.Query(ctx, `SELECT r.metadata FROM idenqa.identity_identifier_tokens t JOIN idenqa.identity_records r ON r.tenant_id=t.tenant_id AND r.id=t.record_id
 WHERE t.tenant_id=$1 AND t.subject_id=$2 AND t.namespace=$3 AND t.issuer=$4 AND t.token=$5 ORDER BY r.sequence DESC LIMIT 101`, scope.ID().String(), subject.ID, q.Identifier.Namespace, q.Identifier.Issuer, lookup)
	if e != nil {
		return false, e
	}
	var records []identity.Record
	for rows.Next() {
		var b []byte
		if e = rows.Scan(&b); e != nil {
			rows.Close()
			return false, e
		}
		var r identity.Record
		if e = json.Unmarshal(b, &r); e != nil {
			rows.Close()
			return false, e
		}
		records = append(records, r)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	if len(records) > 100 {
		return false, identity.ErrUnavailable
	}
	for _, r := range records {
		ok, err := recordEligible(ctx, tx, scope, subject, r, q.At, true)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}
