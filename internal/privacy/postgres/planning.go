package postgres

import (
	"context"
	"encoding/json"

	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/privacy"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// EvidenceTargetsWithin derives bounded exact object references within an owning
// aggregate transaction. Empty evidence is valid; partial target sets are not.
func EvidenceTargetsWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, verification, region string, limit int) ([]privacy.Target, error) {
	if limit < 1 || limit > 256 {
		return nil, privacy.ErrInvalid
	}
	var otherRegion bool
	if e := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM idenqa.evidence_assets WHERE tenant_id=$1 AND verification_id=$2 AND region<>$3 AND state<>'deleted')`, scope.ID().String(), verification, region).Scan(&otherRegion); e != nil {
		return nil, e
	}
	if otherRegion {
		// A regional deletion must never silently omit linked evidence elsewhere.
		return nil, privacy.ErrInvalid
	}
	rows, e := tx.Query(ctx, `SELECT id,retention_class,object_key,object_version,ciphertext_size,ciphertext_checksum,version FROM idenqa.evidence_assets
 WHERE tenant_id=$1 AND verification_id=$2 AND region=$3 AND state<>'deleted' ORDER BY created_at,id LIMIT $4`, scope.ID().String(), verification, region, limit+1)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	var targets []privacy.Target
	for rows.Next() {
		r := evidenceReference{TenantID: scope.ID().String(), Region: region}
		var class string
		if e = rows.Scan(&r.EvidenceID, &class, &r.Object.Key, &r.Object.Version, &r.Object.Size, &r.Object.Checksum, &r.Version); e != nil {
			return nil, e
		}
		if _, e = objectstore.NewObject(r.Object); e != nil {
			return nil, privacy.ErrInvalid
		}
		b, e := json.Marshal(r)
		if e != nil {
			return nil, e
		}
		kind := string(privacy.DataClassRawEvidence)
		if class == string(privacy.DataClassDerivedEvidence) {
			kind = string(privacy.DataClassDerivedEvidence)
		}
		targets = append(targets, privacy.Target{Kind: kind, Reference: string(b), Region: region})
	}
	if e = rows.Err(); e != nil {
		return nil, e
	}
	if len(targets) > limit {
		return nil, privacy.ErrInvalid
	}
	return targets, nil
}
