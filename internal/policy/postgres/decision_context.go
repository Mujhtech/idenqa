package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
	"github.com/Mujhtech/idenqa/internal/fraud"
	"github.com/Mujhtech/idenqa/internal/identity"
	"github.com/Mujhtech/idenqa/internal/model"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	"github.com/Mujhtech/idenqa/internal/provider"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/jackc/pgx/v5"
)

func assuranceDigest(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

// EnableDecisionContext enables the complete production projection. Lightweight
// adapter fixtures can still exercise only the original owned fact projection.
func (s *Source) EnableDecisionContext() { s.completeContext = true }

func loadDecisionContext(ctx context.Context, tx pg.Transaction, scope tenant.Scope, p Projection, facts []policy.Fact) (*policy.DecisionContext, error) {
	c := &policy.DecisionContext{Version: 1, References: []policy.DecisionReference{}, Sources: []policy.AssuranceSource{}}
	var name, digest *string
	var revision *uint32
	e := tx.QueryRow(ctx, `SELECT profile_name,profile_revision,profile_digest FROM idenqa.verification_assurance WHERE tenant_id=$1 AND verification_id=$2`, scope.ID().String(), p.VerificationID.String()).Scan(&name, &revision, &digest)
	if e != nil && !errors.Is(e, pgx.ErrNoRows) {
		return nil, e
	}
	if name != nil && revision != nil && digest != nil {
		c.Profile, c.ProfileDigest, e = readAssuranceProfile(ctx, tx, scope, *name, *revision)
		if e != nil {
			return nil, e
		}
		if c.ProfileDigest != *digest {
			return nil, policy.ErrReproduction
		}
	}
	add := func(kind, id, version, digest string, parents ...string) {
		c.References = append(c.References, policy.DecisionReference{Kind: kind, ID: id, Version: version, Digest: strings.TrimPrefix(digest, "sha256:"), Parents: parents})
	}
	var authorityVersion int64
	var pack, notice, authorityContext string
	if e = tx.QueryRow(ctx, `SELECT version,policy_pack,notice_id,jsonb_build_object('category',category,'purpose',purpose,'jurisdiction',jurisdiction,'policy_pack',policy_pack,'consent_required',consent_required,'requirement_purposes',requirement_purposes,'evidence_types',evidence_types,'recipient_reference',recipient_reference,'regions',regions,'retention_reference',retention_reference,'valid_from',valid_from,'expires_at',expires_at)::text FROM idenqa.processing_authorities WHERE tenant_id=$1 AND id=$2 AND verification_id=$3`, scope.ID().String(), p.Authority.AuthorityID.String(), p.VerificationID.String()).Scan(&authorityVersion, &pack, &notice, &authorityContext); e != nil {
		return nil, e
	}
	add("authority", p.Authority.AuthorityID.String(), strconv.FormatInt(authorityVersion, 10), assuranceDigest([]byte(authorityContext)), notice)
	add("subject_response", p.Authority.AcknowledgementID.String(), "", "", notice)
	add("region", p.Region, "", "", p.Authority.AuthorityID.String())
	add("transfer_policy", pack, "", assuranceDigest([]byte(authorityContext)), p.Authority.AuthorityID.String())
	var profileID, profileDigest string
	var profileRevision int64
	if e = tx.QueryRow(ctx, `SELECT source_profile_id,source_profile_revision,source_profile_digest FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), p.VerificationID.String()).Scan(&profileID, &profileRevision, &profileDigest); e != nil {
		return nil, e
	}
	add("capture_profile", profileID, strconv.FormatInt(profileRevision, 10), profileDigest)
	// Potential capture dependencies are conservative until every runner declares exact inputs.
	evidenceRoots := []string{}
	var oldest time.Time
	type captureInput struct {
		id, kind, method string
		at               time.Time
		assurances       []string
		available        bool
	}
	captures := map[string]captureInput{}
	rows, e := tx.Query(ctx, `SELECT id,content_revision,created_at,evidence_type,acquisition_method,assurances,registry_digest,state='available' AND integrity='verified' FROM idenqa.evidence_assets WHERE tenant_id=$1 AND verification_id=$2 AND created_at<=$3 ORDER BY id LIMIT 129`, scope.ID().String(), p.VerificationID.String(), p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var input captureInput
		var version int64
		var registryDigest string
		if e = rows.Scan(&input.id, &version, &input.at, &input.kind, &input.method, &input.assurances, &registryDigest, &input.available); e != nil {
			rows.Close()
			return nil, e
		}
		input.at = input.at.UTC()
		captures[input.id] = input
		evidenceRoots = append(evidenceRoots, "evidence."+input.id)
		add("evidence", input.id, strconv.FormatInt(version, 10), "")
		if oldest.IsZero() || input.at.Before(oldest) {
			oldest = input.at
		}
		// Only acquisition provenance from currently available, integrity-checked assets qualifies.
		if input.available {
			for _, a := range []string{"freshness", "live_capture", "capture_integrity"} {
				if slices.Contains(input.assurances, "idenqa.assurance."+a) {
					c.Sources = append(c.Sources, policy.AssuranceSource{Reference: input.id + "." + a, Signal: "capture." + a, State: policy.RequirementSatisfied, RunnerKind: "capture", RunnerID: input.method, PackageDigest: strings.TrimPrefix(registryDigest, "sha256:"), ConfigurationDigest: strings.TrimPrefix(profileDigest, "sha256:"), CollectedAt: input.at, Roots: []string{"capture." + p.VerificationID.String(), "evidence." + input.id}, CaptureAssurances: slices.Clone(input.assurances)})
				}
			}
		}
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	if len(evidenceRoots) > 128 {
		return nil, policy.ErrInvalid
	}
	// Liveness and face binding require the actual selfie inputs' provenance. A live
	// document cannot turn an uploaded selfie into fresh or live subject evidence.
	selfieAssurances := func(ids []string) []string {
		var eligible []string
		found := false
		for _, id := range ids {
			input, ok := captures[id]
			if !ok || input.kind != "idenqa.evidence.selfie_image" {
				continue
			}
			if !input.available {
				return nil
			}
			if !found {
				eligible = slices.Clone(input.assurances)
				found = true
			} else {
				eligible = slices.DeleteFunc(eligible, func(a string) bool { return !slices.Contains(input.assurances, a) })
			}
		}
		return eligible
	}
	imported := map[string]identity.Record{}
	identityInputs := map[string][]string{}
	records := map[string]identity.Record{}
	sourceConfigurations := map[int64]bool{}
	recordIDs := []string{}
	for _, f := range facts {
		if f.Source.Identity != nil {
			var body []byte
			e = tx.QueryRow(ctx, `SELECT receipt FROM idenqa.identity_receipts WHERE tenant_id=$1 AND verification_id=$2 AND digest=$3`, scope.ID().String(), p.VerificationID.String(), f.Source.Identity.ReceiptDigest).Scan(&body)
			if e != nil {
				return nil, e
			}
			var receipt identity.Receipt
			if json.Unmarshal(body, &receipt) != nil {
				return nil, policy.ErrReproduction
			}
			d, e := identity.Digest(receipt)
			if e != nil || d != f.Source.Identity.ReceiptDigest {
				return nil, policy.ErrReproduction
			}
			add("identity_receipt", d, "", d)
			add("configuration", "identity", strconv.FormatInt(receipt.ConfigurationVersion, 10), receipt.ConfigurationDigest)
			for _, finding := range receipt.Findings {
				if finding.Key == string(f.Key) {
					identityInputs["identity."+d+"."+finding.Key] = slices.Clone(finding.RecordIDs)
					c.Sources = append(c.Sources, policy.AssuranceSource{Reference: "identity." + d + "." + finding.Key, Signal: finding.Key, State: f.State, RunnerKind: "identity", RunnerID: "idenqa.identity", PackageDigest: policy.BuiltinAssuranceDigest("identity"), ConfigurationDigest: receipt.ConfigurationDigest, CollectedAt: receipt.EvaluatedAt, Roots: []string{"identity." + receipt.SubjectID}})
				}
				recordIDs = append(recordIDs, finding.RecordIDs...)
			}
		}
		if f.Source.Fraud != nil {
			var body []byte
			if e = tx.QueryRow(ctx, `SELECT receipt FROM idenqa.fraud_receipts WHERE tenant_id=$1 AND verification_id=$2 AND digest=$3`, scope.ID().String(), p.VerificationID.String(), f.Source.Fraud.ReceiptDigest).Scan(&body); e != nil {
				return nil, e
			}
			var receipt fraud.Receipt
			if json.Unmarshal(body, &receipt) != nil || receipt.Digest != f.Source.Fraud.ReceiptDigest {
				return nil, policy.ErrReproduction
			}
			expectedDigest := receipt.Digest
			receipt.Digest = ""
			if fraud.Digest(receipt) != expectedDigest {
				return nil, policy.ErrReproduction
			}
			receipt.Digest = expectedDigest
			add("fraud_receipt", receipt.Digest, "", receipt.Digest)
			add("configuration", "fraud", strconv.FormatInt(receipt.ConfigurationVersion, 10), receipt.ConfigurationDigest)
			c.Sources = append(c.Sources, policy.AssuranceSource{Reference: "fraud." + receipt.Digest + "." + string(f.Key), Signal: string(f.Key), State: f.State, RunnerKind: "fraud", RunnerID: "idenqa.fraud", PackageDigest: policy.BuiltinAssuranceDigest("fraud"), ConfigurationDigest: receipt.ConfigurationDigest, CollectedAt: receipt.At, Roots: []string{"fraud." + p.VerificationID.String()}})
			for _, finding := range receipt.Findings {
				for _, source := range finding.Sources {
					if source != "" {
						add("lineage", source, "", "", receipt.Digest)
					}
				}
				for _, group := range finding.Groups {
					add("lineage", group, "", "", receipt.Digest)
				}
			}
		}
	}
	// Current claims and identifiers supplied for this verification are part of
	// its input graph even when no identity rule consumes their values.
	rows, e = tx.Query(ctx, `SELECT r.id FROM idenqa.identity_records r WHERE r.tenant_id=$1 AND r.verification_id=$2 AND r.kind IN ('claim','identifier') AND r.recorded_at<=$3 AND NOT EXISTS(SELECT 1 FROM idenqa.identity_records n WHERE n.tenant_id=r.tenant_id AND n.subject_id=r.subject_id AND n.supersedes=r.id AND n.recorded_at<=$3) ORDER BY r.id LIMIT 2001`, scope.ID().String(), p.VerificationID.String(), p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	for rows.Next() {
		var recordID string
		if e = rows.Scan(&recordID); e != nil {
			rows.Close()
			return nil, e
		}
		recordIDs = append(recordIDs, recordID)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return nil, e
	}
	slices.Sort(recordIDs)
	recordIDs = slices.Compact(recordIDs)
	if len(recordIDs) > identity.MaximumRecords {
		return nil, policy.ErrInvalid
	}
	if len(recordIDs) > 0 {
		recordCount := 0
		rows, e = tx.Query(ctx, `SELECT metadata FROM idenqa.identity_records WHERE tenant_id=$1 AND (id=ANY($2::text[]) OR id IN(SELECT jsonb_array_elements_text(coalesce(metadata->'ancestor_ids','[]'::jsonb)) FROM idenqa.identity_records WHERE tenant_id=$1 AND id=ANY($2::text[]))) ORDER BY id LIMIT 2001`, scope.ID().String(), recordIDs)
		if e != nil {
			return nil, e
		}
		for rows.Next() {
			var b []byte
			if e = rows.Scan(&b); e != nil {
				rows.Close()
				return nil, e
			}
			recordCount++
			if recordCount > identity.MaximumRecords {
				rows.Close()
				return nil, policy.ErrInvalid
			}
			var r identity.Record
			if json.Unmarshal(b, &r) != nil {
				rows.Close()
				return nil, policy.ErrReproduction
			}
			records[r.ID] = r
			if r.SourceConfigurationVersion > 0 {
				sourceConfigurations[r.SourceConfigurationVersion] = true
			}
			add(r.Kind, r.ID, strconv.FormatInt(r.Sequence, 10), "", r.SourceRecordIDs...)
			for _, ancestor := range r.AncestorIDs {
				add("lineage", ancestor, "", "", r.ID)
			}
			for _, root := range r.LineageRoots {
				add("lineage", root, "", "", r.ID)
			}
			if r.OriginObservationID != "" {
				imported[r.OriginObservationID] = r
			}
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return nil, e
		}
	}
	for version := range sourceConfigurations {
		var digest string
		if e = tx.QueryRow(ctx, `SELECT digest FROM idenqa.identity_configurations WHERE tenant_id=$1 AND region=$2 AND version=$3`, scope.ID().String(), p.Region, version).Scan(&digest); e != nil {
			return nil, e
		}
		add("configuration", "identity.source", strconv.FormatInt(version, 10), digest)
	}
	for i, source := range c.Sources {
		ids, ok := identityInputs[source.Reference]
		if !ok {
			continue
		}
		if len(ids) == 0 {
			c.Sources[i].State = policy.RequirementInconclusive
			continue
		}
		roots := slices.Clone(source.Roots)
		expiry := time.Time{}
		for _, id := range ids {
			record, ok := records[id]
			if !ok {
				return nil, policy.ErrReproduction
			}
			if record.CollectedAt.Before(c.Sources[i].CollectedAt) {
				c.Sources[i].CollectedAt = record.CollectedAt
			}
			roots = append(roots, record.LineageRoots...)
			for _, deadline := range []time.Time{record.ValidUntil, record.RetainUntil} {
				if expiry.IsZero() || deadline.Before(expiry) {
					expiry = deadline
				}
			}
			if record.FreshUntil != nil && record.FreshUntil.Before(expiry) {
				expiry = *record.FreshUntil
			}
		}
		slices.Sort(roots)
		c.Sources[i].Roots = slices.Compact(roots)
		c.Sources[i].ExpiresAt = &expiry
	}
	for _, check := range p.Checks {
		a := check.Attempt
		add("check", check.CheckID.String(), strconv.FormatInt(check.Version, 10), "", a.AttemptID.String())
		add("attempt", a.AttemptID.String(), strconv.FormatUint(uint64(a.Number), 10), a.RequestDigest)
		add(a.RunnerKind, a.RunnerID, a.RunnerVersion, a.PackageDigest, a.AttemptID.String())
		add("configuration", a.AttemptID.String(), "", a.ConfigurationDigest)
		add("signal", a.AttemptID.String(), "", a.ResultDigest)
		threshold := ""
		inputIDs := []string{}
		provenanceBound := false
		roots := append([]string{"check." + check.CheckID.String(), "request." + a.RequestDigest}, evidenceRoots...)
		evaluationOnly := a.RunnerKind == "model"
		if a.RunnerKind == "provider" {
			roots = append(roots, "external.unclassified")
		} else {
			roots = append(roots, "capture."+p.VerificationID.String())
		}
		if a.RunnerKind == "provider" {
			var body []byte
			e = tx.QueryRow(ctx, `SELECT request_body FROM idenqa.provider_requests WHERE tenant_id=$1 AND attempt_id=$2 AND verification_id=$3`, scope.ID().String(), a.AttemptID.String(), p.VerificationID.String()).Scan(&body)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return nil, e
			}
			if e == nil {
				var request providerv1.Request
				if json.Unmarshal(body, &request) != nil {
					return nil, policy.ErrReproduction
				}
				d, e := provider.RequestDigest(request)
				if e != nil || d != a.RequestDigest {
					return nil, policy.ErrReproduction
				}
				provenanceBound = true
				add("configuration", request.ProviderID, request.Configuration.CredentialVersion, a.ConfigurationDigest, a.AttemptID.String())
				for _, input := range request.Evidence {
					inputIDs = append(inputIDs, input.EvidenceID)
					add("lineage", input.GrantID, "", "", input.EvidenceID, a.AttemptID.String())
				}
				for _, input := range request.Inputs {
					add("claim", input.Name, "", a.RequestDigest, a.AttemptID.String())
				}
			}
		}
		if a.RunnerKind == "model" {
			var body, selection []byte
			e = tx.QueryRow(ctx, `SELECT request_body,registry_selection FROM idenqa.model_requests WHERE tenant_id=$1 AND attempt_id=$2 AND verification_id=$3`, scope.ID().String(), a.AttemptID.String(), p.VerificationID.String()).Scan(&body, &selection)
			if e != nil && !errors.Is(e, pgx.ErrNoRows) {
				return nil, e
			}
			if e == nil {
				var request modelv1.Request
				if json.Unmarshal(body, &request) != nil {
					return nil, policy.ErrReproduction
				}
				d, e := model.RequestDigest(request)
				if e != nil || d != a.RequestDigest {
					return nil, policy.ErrReproduction
				}
				provenanceBound = true
				add("model", request.Provenance.ModelID, request.Provenance.ModelVersion, request.Provenance.ModelDigest, a.AttemptID.String())
				add("runtime", a.AttemptID.String(), "", request.Provenance.RuntimeDigest)
				add("preprocessing", a.AttemptID.String(), "", request.Provenance.PreprocessingDigest)
				for _, input := range request.Evidence {
					inputIDs = append(inputIDs, input.EvidenceID)
					add("lineage", input.GrantID, "", "", input.EvidenceID, a.AttemptID.String())
				}
				if len(selection) > 0 {
					var selected model.RegistrySelection
					if json.Unmarshal(selection, &selected) != nil || selected.Validate() != nil {
						return nil, policy.ErrReproduction
					}
					threshold = strings.TrimPrefix(selected.ThresholdDigest, "sha256:")
					add("threshold", selected.Name, strconv.FormatInt(selected.ThresholdRevision, 10), threshold)
				}
			}
		}
		// Synthetic or legacy manually imported attempts may have no registered envelope.
		// They remain referenceable but cannot establish typed assurance.
		if !provenanceBound {
			evaluationOnly = true
		}
		captureAssurances := selfieAssurances(inputIDs)

		for _, o := range check.Observations {
			state, e := directRequirementState(o.SignalOutcome)
			if e != nil {
				return nil, e
			}
			at := o.RecordedAt
			if !oldest.IsZero() && oldest.Before(at) {
				at = oldest
			}
			sourceRoots := slices.Clone(roots)
			if r, ok := imported[o.ObservationID.String()]; ok {
				sourceRoots = slices.Clone(r.LineageRoots)
				for _, ev := range r.EvidenceIDs {
					sourceRoots = append(sourceRoots, "evidence."+ev)
				}
				if r.CollectedAt.Before(at) {
					at = r.CollectedAt
				}
			}
			slices.Sort(sourceRoots)
			sourceRoots = slices.Compact(sourceRoots)
			add("observation", o.ObservationID.String(), "", "", a.AttemptID.String())
			add("signal", o.SignalName, "", "", o.ObservationID.String())
			c.Sources = append(c.Sources, policy.AssuranceSource{Reference: o.ObservationID.String(), Signal: o.SignalName, State: state, RunnerKind: a.RunnerKind, RunnerID: a.RunnerID, PackageDigest: a.PackageDigest, ConfigurationDigest: a.ConfigurationDigest, ThresholdDigest: threshold, CollectedAt: at, Roots: sourceRoots, EvaluationOnly: evaluationOnly, CaptureAssurances: captureAssurances})
		}
	}
	return c, nil
}
