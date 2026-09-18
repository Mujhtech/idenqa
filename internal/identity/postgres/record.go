package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/identity"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// authorityProjection never treats possession of an API key as processing authority.
func authorityProjection(ctx context.Context, tx pg.Transaction, scope tenant.Scope, subject, verification, region string, at time.Time) (string, string, error) {
	var authority, response string
	e := tx.QueryRow(ctx, `SELECT a.id,r.id FROM idenqa.identity_subject_verifications l
 JOIN idenqa.verification_sessions s ON s.tenant_id=l.tenant_id AND s.id=l.verification_id
 JOIN idenqa.processing_authorities a ON a.tenant_id=s.tenant_id AND a.id=s.authority_id
 JOIN LATERAL(SELECT id,action FROM idenqa.subject_responses WHERE tenant_id=a.tenant_id AND authority_id=a.id AND recorded_at<=$5 ORDER BY recorded_at DESC,id DESC LIMIT 1)r ON true
 WHERE l.tenant_id=$1 AND l.subject_id=$2 AND l.verification_id=$3 AND s.region=$4
 AND a.state='active' AND a.valid_from<=$5 AND a.expires_at>$5 AND a.expires_at>clock_timestamp()
 AND 'idenqa.purpose.identity_verification'=ANY(a.requirement_purposes) AND $4=ANY(a.regions)
 AND r.action IN ('consent',CASE WHEN NOT a.consent_required THEN 'acknowledge' ELSE 'consent' END)
 FOR SHARE OF a`, scope.ID().String(), subject, verification, region, at).Scan(&authority, &response)
	if errors.Is(e, pgx.ErrNoRows) {
		e = identity.ErrNotFound
	}
	return authority, response, e
}
func (s *Store) appendRecord(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c identity.Command, subject identity.Subject, wrapped []byte) (identity.Result, error) {
	if c.Record == nil || c.Record.Validate(c.At) != nil {
		return identity.Result{}, identity.ErrInvalid
	}
	input := *c.Record
	prefix := map[string]string{"observation": "obs", "fact": "fct", "claim": "clm", "identifier": "idi"}[input.Kind]
	generated, e := s.ids.New(id.Prefix(prefix))
	if e != nil {
		return identity.Result{}, e
	}
	r := identity.Record{ActorKeyID: c.Actor.String(), ID: generated.String(), SubjectID: subject.ID, Kind: input.Kind, Name: input.Name, SchemaVersion: 1, Sequence: subject.Version + 1, SeriesID: generated.String(), Supersedes: input.Supersedes, Normalization: input.Normalization, VerificationID: input.VerificationID, EvidenceIDs: []string{}, SourceRecordIDs: slices.Clone(input.SourceRecordIDs), AncestorIDs: []string{}, LineageRoots: []string{}, SourceClasses: []string{}, CollectedAt: input.CollectedAt, ObservedAt: input.ObservedAt, RecordedAt: c.At, ValidFrom: input.ValidFrom, ValidUntil: input.ValidUntil, FreshUntil: input.FreshUntil, RetainUntil: input.RetainUntil, ConfidenceBPS: input.ConfidenceBPS, Unit: input.Unit, Identifier: input.Identifier}
	r.AuthorityID, r.AcknowledgementID, e = authorityProjection(ctx, tx, scope, subject.ID, input.VerificationID, subject.Region, c.At)
	if e != nil {
		return identity.Result{}, e
	}
	key, e := s.unwrap(ctx, wrapped, "identity.subject.v1", scope.ID().String(), subject.Region, subject.ID)
	if e != nil {
		return identity.Result{}, e
	}
	defer clear(key)
	var secret identity.ProtectedValue
	if r.Kind == "observation" {
		r.EvidenceIDs = slices.Clone(input.EvidenceIDs)
		if input.OriginObservationID != "" {
			secret, e = s.importObservation(ctx, tx, scope, subject, &r, input.OriginObservationID, c.At)
		} else {
			secret = identity.ProtectedValue{Value: *input.Value, Original: input.Original}
			r.SourceClass = "tenant_attested"
			r.SourceName = input.SourceName
			r.InputVersion = input.InputVersion
			r.SourceClasses = []string{"tenant_attested"}
			// All tenant assertions share a root, even across credentials and source labels.
			r.LineageRoots = []string{"tenant.assertions"}
		}
	} else {
		secret, e = s.derive(ctx, tx, scope, subject, &r, key, c.At)
	}
	if e != nil {
		return identity.Result{}, e
	}
	r.ValueType = secret.Value.Type
	if e = s.evidenceRoots(ctx, tx, scope, subject, &r, c.At); e != nil {
		return identity.Result{}, e
	}
	if r.Supersedes != "" {
		previous, _, e := s.loadRecord(ctx, tx, scope, subject, r.Supersedes, key, c.At, false)
		if e != nil {
			return identity.Result{}, e
		}
		if !previous.Current || previous.Kind != r.Kind || previous.Name != r.Name || previous.ValueType != r.ValueType || previous.Unit != r.Unit || r.ObservedAt.Before(previous.ObservedAt) {
			return identity.Result{}, identity.ErrConflict
		}
		if (previous.Identifier == nil) != (r.Identifier == nil) {
			return identity.Result{}, identity.ErrInvalid
		}
		if r.Identifier != nil && (previous.Identifier.Namespace != r.Identifier.Namespace || previous.Identifier.Issuer != r.Identifier.Issuer) {
			return identity.Result{}, identity.ErrConflict
		}
		r.SeriesID = previous.SeriesID
		// Correction cannot mint a new independent source by changing labels or IDs.
		r.LineageRoots = union(r.LineageRoots, previous.LineageRoots)
	}
	if len(r.LineageRoots) > identity.MaximumSources || len(r.AncestorIDs) > identity.MaximumSources || len(r.EvidenceIDs) > identity.MaximumSources {
		return identity.Result{}, identity.ErrUnavailable
	}
	if r.Identifier != nil {
		if secret.Value.Type != "string" || len(secret.Value.Text) > 200 {
			return identity.Result{}, identity.ErrInvalid
		}
		// Verification status is source-attributed; arbitrary identity values do not
		// become verified merely because another check on the subject succeeded.
		metadata := *r.Identifier
		metadata.VerificationStateSource = "tenant_attested"
		metadata.VerificationActorID = c.Actor.String()
		r.Identifier = &metadata
	}
	if r.EvidenceIDs == nil {
		r.EvidenceIDs = []string{}
	}
	if r.SourceRecordIDs == nil {
		r.SourceRecordIDs = []string{}
	}
	b, e := json.Marshal(r)
	if e != nil {
		return identity.Result{}, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_records(tenant_id,id,subject_id,verification_id,kind,name,sequence,series_id,supersedes,metadata,recorded_at,retain_until)VALUES($1,$2,$3,$4,$5,$6,$7,$8,NULLIF($9,''),$10,$11,$12)`, scope.ID().String(), r.ID, subject.ID, r.VerificationID, r.Kind, r.Name, r.Sequence, r.SeriesID, r.Supersedes, b, c.At, r.RetainUntil)
	if e != nil {
		return identity.Result{}, e
	}
	plain, e := json.Marshal(secret)
	if e != nil {
		return identity.Result{}, e
	}
	defer clear(plain)
	aad, e := contextBytes("identity.record."+r.Kind+".v1", scope.ID().String(), subject.Region, r.ID)
	if e != nil {
		return identity.Result{}, e
	}
	sealed, e := sealValue(key, aad, plain)
	if e != nil {
		return identity.Result{}, e
	}
	if _, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_record_values(tenant_id,record_id,subject_id,ciphertext)VALUES($1,$2,$3,$4)`, scope.ID().String(), r.ID, subject.ID, sealed); e != nil {
		return identity.Result{}, e
	}
	for _, source := range r.SourceRecordIDs {
		if _, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_record_edges(tenant_id,subject_id,record_id,source_id)VALUES($1,$2,$3,$4)`, scope.ID().String(), subject.ID, r.ID, source); e != nil {
			return identity.Result{}, e
		}
	}
	for _, evidence := range r.EvidenceIDs {
		if _, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_record_evidence(tenant_id,subject_id,verification_id,record_id,evidence_id)SELECT $1,$2,verification_id,$3,id FROM idenqa.evidence_assets WHERE tenant_id=$1 AND id=$4`, scope.ID().String(), subject.ID, r.ID, evidence); e != nil {
			return identity.Result{}, e
		}
	}
	if _, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_current(tenant_id,subject_id,kind,name,series_id,record_id)VALUES($1,$2,$3,$4,$5,$6) ON CONFLICT(tenant_id,subject_id,series_id)DO UPDATE SET record_id=EXCLUDED.record_id`, scope.ID().String(), subject.ID, r.Kind, r.Name, r.SeriesID, r.ID); e != nil {
		return identity.Result{}, e
	}
	if r.Identifier != nil {
		lookup, e := s.lookupKey(ctx, tx, scope, subject.Region, true)
		if e != nil {
			return identity.Result{}, e
		}
		t, e := token(lookup, "identity.identifier.v1", scope.ID().String(), subject.Region, r.Identifier.Namespace, r.Identifier.Issuer, r.Normalization, secret.Value.Text)
		clear(lookup)
		if e != nil {
			return identity.Result{}, e
		}
		if _, e = tx.Exec(ctx, `INSERT INTO idenqa.identity_identifier_tokens(tenant_id,subject_id,record_id,region,namespace,issuer,key_version,token)VALUES($1,$2,$3,$4,$5,$6,1,$7)`, scope.ID().String(), subject.ID, r.ID, subject.Region, r.Identifier.Namespace, r.Identifier.Issuer, t); e != nil {
			return identity.Result{}, e
		}
	}
	if e = bumpSubject(ctx, tx, scope, &subject, c.At); e != nil {
		return identity.Result{}, e
	}
	// Mutation replay persists only metadata, never a decrypted value or display mask.
	r.Current = true
	r.Available = true
	return identity.Result{Subject: &subject, Record: &r}, nil
}
func union(a, b []string) []string {
	result := append(slices.Clone(a), b...)
	slices.Sort(result)
	return slices.Compact(result)
}
