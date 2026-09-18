package postgres

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/fraud"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

// eligibleEvidence requires current authority, purpose, region, evidence scope
// and response. Neither an SDK advertisement nor a tenant token grants authority.
const eligibleEvidence = ` FROM idenqa.evidence_assets e
 JOIN idenqa.verification_sessions s ON s.tenant_id=e.tenant_id AND s.id=e.verification_id
 JOIN idenqa.processing_authorities a ON a.tenant_id=s.tenant_id AND a.id=s.authority_id
 WHERE e.tenant_id=$1 AND e.region=$2 AND e.state='available' AND e.integrity='verified'
 AND a.state='active' AND a.valid_from<=$3 AND a.expires_at>$3 AND a.expires_at>clock_timestamp()
 AND 'idenqa.purpose.fraud_prevention'=ANY(a.requirement_purposes) AND e.evidence_type=ANY(a.evidence_types) AND e.region=ANY(a.regions)
 AND (SELECT action FROM idenqa.subject_responses WHERE tenant_id=a.tenant_id AND authority_id=a.id AND recorded_at<=$3 ORDER BY recorded_at DESC,id DESC LIMIT 1)
 IN ('consent',CASE WHEN NOT a.consent_required THEN 'acknowledge' ELSE 'consent' END)
 AND e.created_at<=$3`

func (s *Store) ingest(ctx context.Context, tx pg.Transaction, scope tenant.Scope, command fraud.Command) (fraud.Result, error) {
	input := command.Input
	if input == nil || input.Validate() != nil {
		return fraud.Result{}, fraud.ErrInvalid
	}
	c, v, e := configuration(ctx, tx, scope, command.At)
	if e != nil {
		return fraud.Result{}, e
	}
	if v == 0 || !c.Enabled {
		return fraud.Result{}, fraud.ErrConflict
	}
	permitted := false
	for _, source := range c.Sources {
		if source.KeyID != command.Retry.Principal().String() || source.Namespace != input.Namespace {
			continue
		}
		permitted = true
		for _, a := range input.Attributes {
			if !slices.Contains(source.Kinds, a.Kind) || (a.Kind == "portrait" && !source.PortraitPermitted) {
				permitted = false
			}
		}
	}
	if !permitted {
		return fraud.Result{}, fraud.ErrInvalid
	}
	var observed time.Time
	e = tx.QueryRow(ctx, `SELECT e.created_at`+eligibleEvidence+` AND e.id=$4 AND e.verification_id=$5 FOR SHARE OF e,a`, scope.ID().String(), c.Region, command.At, input.EvidenceID, input.VerificationID).Scan(&observed)
	if errors.Is(e, pgx.ErrNoRows) {
		return fraud.Result{}, fraud.ErrNotFound
	}
	if e != nil {
		return fraud.Result{}, e
	}
	if !observed.Add(time.Duration(c.RetentionSeconds) * time.Second).After(command.At) {
		return fraud.Result{}, fraud.ErrConflict
	}
	key, e := s.key(ctx, tx, scope, c.Region, false)
	if e != nil {
		return fraud.Result{}, e
	}
	defer clear(key)
	var digests []string
	for _, attribute := range input.Attributes {
		token := fraud.Token(key, scope.ID().String(), c.Region, input.Namespace, attribute.Kind, attribute.Value)
		if e := insertLink(ctx, tx, scope, input.VerificationID, input.EvidenceID, attribute.Kind, input.Namespace, token, c.Region, fraud.Token(key, scope.ID().String(), c.Region, input.Namespace, "source_reference", input.SourceReference), "tenant_attested", command.Retry.Principal().String(), v, observed, observed.Add(time.Duration(c.RetentionSeconds)*time.Second)); e != nil {
			return fraud.Result{}, e
		}
		digests = append(digests, token)
	}
	return fraud.Result{Version: v, Digest: fraud.Digest(digests)}, nil
}
func insertLink(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verification, evidence, kind, namespace, token, region, reference, class, actor string, version int64, observed, expires time.Time) error {
	tag, e := tx.Exec(ctx, `INSERT INTO idenqa.fraud_links(tenant_id,verification_id,evidence_id,kind,namespace,token,region,source_reference,source_class,actor_key_id,configuration_version,observed_at,expires_at)VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,''),$11,$12,$13) ON CONFLICT DO NOTHING`, scope.ID().String(), verification, evidence, kind, namespace, token, region, reference, class, actor, version, observed, expires)
	if e != nil {
		return e
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var same bool
	e = tx.QueryRow(ctx, `SELECT token=$6 AND verification_id=$7 AND region=$8 AND source_class=$9 AND coalesce(actor_key_id,'')=$10 FROM idenqa.fraud_links WHERE tenant_id=$1 AND evidence_id=$2 AND namespace=$3 AND kind=$4 AND source_reference=$5`, scope.ID().String(), evidence, namespace, kind, reference, token, verification, region, class, actor).Scan(&same)
	if e != nil {
		return e
	}
	if !same {
		return fraud.ErrConflict
	}
	return nil
}

// indexEvidence lazily reconciles bounded authoritative evidence within the
// configured horizon, including evidence written before fraud was enabled.
// An overflow disables negative conclusions rather than silently truncating.
func (s *Store) indexEvidence(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c fraud.Configuration, v int64, at time.Time) (bool, error) {
	key, e := s.key(ctx, tx, scope, c.Region, false)
	if e != nil {
		return false, e
	}
	defer clear(key)
	rows, e := tx.Query(ctx, `SELECT e.verification_id,e.id,e.evidence_type,e.acquisition_method,e.plaintext_digest,e.created_at`+eligibleEvidence+` AND e.created_at >= $4 AND e.created_at + make_interval(secs=>$5::double precision)>clock_timestamp() ORDER BY e.created_at,e.id LIMIT 2001 FOR SHARE OF e,a`, scope.ID().String(), c.Region, at, at.Add(-time.Duration(c.WindowSeconds)*time.Second), c.RetentionSeconds)
	if e != nil {
		return false, e
	}
	type item struct {
		verification, evidence, kind, method, digest string
		at                                           time.Time
	}
	var items []item
	for rows.Next() {
		var i item
		if e = rows.Scan(&i.verification, &i.evidence, &i.kind, &i.method, &i.digest, &i.at); e != nil {
			rows.Close()
			return false, e
		}
		items = append(items, i)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return false, e
	}
	if len(items) > 2000 {
		return false, nil
	}
	for _, i := range items {
		kinds := []string{}
		if i.method == "idenqa.method.live_camera" {
			kinds = append(kinds, "capture")
		}
		if i.kind == "idenqa.evidence.document_image" {
			kinds = append(kinds, "document")
		}
		for _, kind := range kinds {
			token := fraud.Token(key, scope.ID().String(), c.Region, "core", kind, i.digest)
			if e := insertLink(ctx, tx, scope, i.verification, i.evidence, kind, "core", token, c.Region, i.evidence, "core", "", v, i.at, i.at.Add(time.Duration(c.RetentionSeconds)*time.Second)); e != nil {
				return false, e
			}
		}
	}
	return true, nil
}
