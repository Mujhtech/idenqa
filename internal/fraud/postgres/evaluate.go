package postgres

import (
	"context"
	"encoding/json"
	"slices"
	"time"

	"github.com/Mujhtech/idenqa/internal/fraud"
	pg "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypg "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// ProjectWithin evaluates inside the policy source's transaction. The receipt
// and every policy fact refer to exactly this cutoff and configuration revision.
func (s *Store) ProjectWithin(ctx context.Context, tx pg.Transaction, scope tenant.Scope, p policypg.Projection) ([]policy.Fact, error) {
	c, v, e := configuration(ctx, tx, scope, p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	if v == 0 {
		return nil, nil
	}
	receipt := fraud.Receipt{VerificationID: p.VerificationID.String(), ConfigurationVersion: v, ConfigurationDigest: fraud.Digest(c), At: p.EvaluatedAt}
	coverage := false
	var authorized bool
	if c.Enabled && c.Region == p.Region {
		if e = tx.QueryRow(ctx, `SELECT EXISTS(SELECT e.id`+eligibleEvidence+` AND e.verification_id=$4)`, scope.ID().String(), c.Region, p.EvaluatedAt, p.VerificationID.String()).Scan(&authorized); e != nil {
			return nil, e
		}
	}
	if c.Enabled && authorized && c.Region == p.Region {
		coverage, e = s.indexEvidence(ctx, tx, scope, c, v, p.EvaluatedAt)
		if e != nil {
			return nil, e
		}
	}
	links, complete, e := loadLinks(ctx, tx, scope, c, p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	if !complete {
		coverage = false
		links = nil
	}
	if !coverage {
		links = nil
	}
	for _, signal := range fraud.Signals {
		f := fraud.Finding{Signal: signal, State: "inconclusive", Reason: "fraud.input_unavailable", Sources: []string{}, Groups: []string{}}
		if c.Enabled && authorized && c.Region == p.Region {
			if coverage {
				f = graphFinding(signal, c, p, links)
			}
			if slices.Contains([]string{"network_anomaly", "provider_inconsistency", "identity_inconsistency", "failed_liveness", "high_risk_model"}, signal) {
				f = mappedFinding(signal, c, p)
			}
			if signal == "provider_inconsistency" && slices.ContainsFunc(c.Mappings, func(m fraud.Mapping) bool { return m.Signal == signal && m.RiskOutcome == "disagreement" }) {
				if !slices.ContainsFunc(c.Mappings, func(m fraud.Mapping) bool { return m.Signal == signal && m.RiskOutcome != "disagreement" }) {
					f = providerDisagreement(c, p)
				} else {
					f = mergePositive(f, providerDisagreement(c, p))
				}
			}
			if signal == "identity_inconsistency" && slices.ContainsFunc(c.Sources, func(s fraud.Source) bool {
				return slices.Contains(s.Kinds, "identifier") || slices.Contains(s.Kinds, "address")
			}) {
				other := attributeInconsistency(c, p, links)
				if !slices.ContainsFunc(c.Mappings, func(m fraud.Mapping) bool { return m.Signal == signal }) {
					f = other
				} else {
					f = mergePositive(f, other)
				}
			}
			if signal == "failed_liveness" {
				f = fraud.Finding{Signal: signal, State: "inconclusive", Reason: "fraud.input_unavailable"}
				if coverage {
					f, e = repeatedLiveness(ctx, tx, scope, c, p, links)
					if e != nil {
						return nil, e
					}
				}
			}
			if signal == "network_anomaly" {
				if slices.ContainsFunc(c.Sources, func(s fraud.Source) bool { return slices.Contains(s.Kinds, "network") }) {
					graph := graphFinding(signal, c, p, links)
					if !slices.ContainsFunc(c.Mappings, func(m fraud.Mapping) bool { return m.Signal == signal }) {
						f = graph
					} else {
						f = mergePositive(f, graph)
					}
				}
			}
			if signal == "session_timing" {
				f, e = timingFinding(ctx, tx, scope, c, p)
				if e != nil {
					return nil, e
				}
			}
		}
		receipt.Findings = append(receipt.Findings, f)
	}
	receipt.Digest = fraud.Digest(receipt)
	b, e := json.Marshal(receipt)
	if e != nil {
		return nil, e
	}
	_, e = tx.Exec(ctx, `INSERT INTO idenqa.fraud_receipts(tenant_id,verification_id,digest,configuration_version,receipt,recorded_at)VALUES($1,$2,$3,$4,$5,$6)ON CONFLICT DO NOTHING`, scope.ID().String(), p.VerificationID.String(), receipt.Digest, v, b, p.EvaluatedAt)
	if e != nil {
		return nil, e
	}
	facts := make([]policy.Fact, 0, len(receipt.Findings))
	for _, f := range receipt.Findings {
		facts = append(facts, policy.Fact{Key: policy.FactKey("fraud." + f.Signal), State: policy.RequirementState(f.State), ObservedAt: p.EvaluatedAt, ReasonCodes: []string{f.Reason}, Source: policy.FactSource{Kind: policy.FactSourceFraud, Fraud: &policy.FraudSource{ReceiptDigest: receipt.Digest}}})
	}
	return facts, nil
}

type link struct {
	verification, evidence, kind, namespace, token, reference, class, actor string
	at                                                                      time.Time
}

func loadLinks(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c fraud.Configuration, at time.Time) ([]link, bool, error) {
	rows, e := tx.Query(ctx, `SELECT l.verification_id,l.evidence_id,l.kind,l.namespace,l.token,l.source_reference,l.source_class,coalesce(l.actor_key_id,''),l.observed_at FROM idenqa.fraud_links l WHERE l.tenant_id=$1 AND l.region=$2 AND l.expires_at>$3 AND l.expires_at>clock_timestamp() AND l.observed_at BETWEEN $4 AND $3 AND l.evidence_id IN (SELECT e.id`+eligibleEvidence+`) ORDER BY l.verification_id,l.evidence_id,l.namespace,l.kind,l.source_reference LIMIT 2001`, scope.ID().String(), c.Region, at, at.Add(-time.Duration(c.WindowSeconds)*time.Second))
	if e != nil {
		return nil, false, e
	}
	defer rows.Close()
	var links []link
	total := 0
	for rows.Next() {
		total++
		var l link
		if e = rows.Scan(&l.verification, &l.evidence, &l.kind, &l.namespace, &l.token, &l.reference, &l.class, &l.actor, &l.at); e != nil {
			return nil, false, e
		}
		if l.class != "core" && !slices.ContainsFunc(c.Sources, func(s fraud.Source) bool {
			return s.KeyID == l.actor && s.Namespace == l.namespace && slices.Contains(s.Kinds, l.kind) && (l.kind != "portrait" || s.PortraitPermitted)
		}) {
			continue
		}
		links = append(links, l)
	}
	return links, total <= 2000, rows.Err()
}
func graphFinding(signal string, c fraud.Configuration, p policypg.Projection, links []link) fraud.Finding {
	f := fraud.Finding{Signal: signal, State: "inconclusive", Reason: "fraud.input_unavailable", Groups: []string{}, Sources: []string{}}
	kinds := map[string][]string{"device_reuse": {"device"}, "identifier_reuse": {"identifier"}, "document_reuse": {"document"}, "portrait_reuse": {"portrait"}, "capture_replay": {"capture"}, "verification_velocity": {"subject", "device", "identifier"}, "network_anomaly": {"network"}}
	allowed, ok := kinds[signal]
	threshold := c.Thresholds[signal]
	if !ok || threshold == 0 {
		return f
	}
	current := map[string]bool{}
	for _, l := range links {
		if l.verification == p.VerificationID.String() && slices.Contains(allowed, l.kind) {
			current[l.kind+"/"+l.namespace+"/"+l.token] = true
			f.Sources = append(f.Sources, l.evidence)
		}
	}
	if len(current) == 0 {
		return f
	}
	matches := map[string]bool{}
	for _, l := range links {
		if l.verification != p.VerificationID.String() && current[l.kind+"/"+l.namespace+"/"+l.token] {
			matches[l.verification] = true
			f.Sources = append(f.Sources, l.evidence)
			f.Groups = append(f.Groups, l.kind+"."+l.namespace)
		}
	}
	f.Count = len(matches)
	f.State = "satisfied"
	f.Reason = "fraud.no_pattern_in_window"
	if f.Count >= threshold {
		f.State = "not_satisfied"
		f.Reason = "fraud.review_required"
	}
	canonicalize(&f)
	return f
}
func mappedFinding(signal string, c fraud.Configuration, p policypg.Projection) fraud.Finding {
	f := fraud.Finding{Signal: signal, State: "inconclusive", Reason: "fraud.input_unavailable", Groups: []string{}, Sources: []string{}}
	threshold := c.Thresholds[signal]
	if threshold == 0 {
		return f
	}
	groups := map[string]bool{}
	related := correlation{}
	seen := false
	unknown := false
	for _, m := range c.Mappings {
		if m.Signal != signal || m.RiskOutcome == "disagreement" {
			continue
		}
		matched := false
		for _, check := range p.Checks {
			if check.Attempt.RunnerKind != m.RunnerKind || check.Attempt.RunnerID != m.RunnerID || check.Attempt.PackageDigest != m.PackageDigest {
				continue
			}
			for _, o := range check.Observations {
				if o.SignalName != m.Observation {
					continue
				}
				matched = true
				f.Sources = append(f.Sources, o.ObservationID.String())
				if o.SignalOutcome == "inconclusive" {
					unknown = true
					continue
				}
				seen = true
				if o.SignalOutcome == m.RiskOutcome {
					groups[m.Group] = true
					related.join("group."+m.Group, "check."+check.CheckID.String())
					related.join("group."+m.Group, "request."+check.Attempt.RequestDigest)
				}
			}
		}
		if !matched {
			unknown = true
		}
	}
	roots := map[string]bool{}
	for g := range groups {
		roots[related.root("group."+g)] = true
	}
	f.Count = len(roots)
	for g := range groups {
		f.Groups = append(f.Groups, g)
	}
	if f.Count >= threshold {
		f.State = "not_satisfied"
		f.Reason = "fraud.review_required"
	} else if seen && !unknown {
		f.State = "satisfied"
		f.Reason = "fraud.no_pattern_in_window"
	}
	canonicalize(&f)
	return f
}
func timingFinding(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c fraud.Configuration, p policypg.Projection) (fraud.Finding, error) {
	f := fraud.Finding{Signal: "session_timing", State: "inconclusive", Reason: "fraud.input_unavailable", Sources: []string{}, Groups: []string{}}
	if c.MaximumSessionSeconds == 0 {
		return f, nil
	}
	var start time.Time
	var end *time.Time
	e := tx.QueryRow(ctx, `SELECT created_at,capture_completed_at FROM idenqa.verification_sessions WHERE tenant_id=$1 AND id=$2`, scope.ID().String(), p.VerificationID.String()).Scan(&start, &end)
	if e != nil {
		return f, e
	}
	if end == nil || end.After(p.EvaluatedAt) {
		return f, nil
	}
	f.Sources = []string{p.VerificationID.String()}
	f.State = "satisfied"
	f.Reason = "fraud.no_pattern_in_window"
	elapsed := end.Sub(start).Seconds()
	if elapsed < float64(c.MinimumSessionSeconds) || elapsed > float64(c.MaximumSessionSeconds) {
		f.State = "not_satisfied"
		f.Count = 1
		f.Reason = "fraud.review_required"
	}
	return f, nil
}
func canonicalize(f *fraud.Finding) {
	slices.Sort(f.Sources)
	f.Sources = slices.Compact(f.Sources)
	slices.Sort(f.Groups)
	f.Groups = slices.Compact(f.Groups)
}

// repeatedLiveness counts distinct verification journeys, never retries or
// multiple observations of the same capture. Cross-journey correlation requires
// an explicitly configured subject/device/identifier link.
func repeatedLiveness(ctx context.Context, tx pg.Transaction, scope tenant.Scope, c fraud.Configuration, p policypg.Projection, links []link) (fraud.Finding, error) {
	f := fraud.Finding{Signal: "failed_liveness", State: "inconclusive", Reason: "fraud.input_unavailable", Sources: []string{}, Groups: []string{}}
	threshold := c.Thresholds[f.Signal]
	if threshold == 0 {
		return f, nil
	}
	tokens := map[string]bool{}
	for _, l := range links {
		if l.verification == p.VerificationID.String() && slices.Contains([]string{"subject", "device", "identifier"}, l.kind) {
			tokens[l.kind+"/"+l.namespace+"/"+l.token] = true
		}
	}
	related := []string{p.VerificationID.String()}
	for _, l := range links {
		if tokens[l.kind+"/"+l.namespace+"/"+l.token] {
			related = append(related, l.verification)
		}
	}
	slices.Sort(related)
	related = slices.Compact(related)
	rows, e := tx.Query(ctx, `SELECT o.id,o.verification_id,o.runner_kind,o.runner_id,o.package_digest,o.signal_name,o.signal_outcome FROM idenqa.verification_observations o JOIN idenqa.verification_attempts a ON a.tenant_id=o.tenant_id AND a.id=o.attempt_id WHERE o.tenant_id=$1 AND o.verification_id=ANY($2::text[]) AND o.recorded_at BETWEEN $3 AND $4 AND a.state='completed' ORDER BY o.verification_id,o.id LIMIT 2001`, scope.ID().String(), related, p.EvaluatedAt.Add(-time.Duration(c.WindowSeconds)*time.Second), p.EvaluatedAt)
	if e != nil {
		return f, e
	}
	defer rows.Close()
	failures := map[string]bool{}
	seen := false
	unknown := false
	total := 0
	for rows.Next() {
		var observation, verification, kind, runner, digest, name, outcome string
		if e = rows.Scan(&observation, &verification, &kind, &runner, &digest, &name, &outcome); e != nil {
			return f, e
		}
		total++
		for _, m := range c.Mappings {
			if m.Signal != f.Signal || m.RunnerKind != kind || m.RunnerID != runner || m.PackageDigest != digest || m.Observation != name {
				continue
			}
			f.Sources = append(f.Sources, observation)
			if outcome == "inconclusive" {
				unknown = true
			} else {
				seen = true
				if outcome == m.RiskOutcome {
					failures[verification] = true
				}
			}
		}
	}
	if e = rows.Err(); e != nil {
		return f, e
	}
	if total > 2000 {
		return f, nil
	}
	f.Count = len(failures)
	if f.Count >= threshold {
		f.State = "not_satisfied"
		f.Reason = "fraud.review_required"
	} else if seen && !unknown {
		f.State = "satisfied"
		f.Reason = "fraud.no_pattern_in_window"
	}
	if len(tokens) > 0 {
		f.Groups = []string{"correlated.verification_journeys"}
	} else {
		f.Groups = []string{"current.verification_journey"}
	}
	canonicalize(&f)
	return f, nil
}
func mergePositive(primary, other fraud.Finding) fraud.Finding {
	if primary.State == "not_satisfied" {
		return primary
	}
	if other.State == "not_satisfied" {
		return other
	}
	if primary.State == "satisfied" && other.State != "satisfied" {
		return other
	}
	return primary
}

// Correlation forms equivalence classes from declared groups and actual check/
// request lineage, so relabelling one observation cannot manufacture independence.
type correlation map[string]string

func (c correlation) root(key string) string {
	for c[key] != "" && c[key] != key {
		key = c[key]
	}
	return key
}
func (c correlation) join(a, b string) {
	a, b = c.root(a), c.root(b)
	if a != b {
		if a < b {
			c[b] = a
		} else {
			c[a] = b
		}
	}
}

// providerDisagreement compares equivalent normalized requirements in declared
// comparison groups. At least two different providers must supply conclusive
// results; retries from one provider cannot create disagreement.
func providerDisagreement(c fraud.Configuration, p policypg.Projection) fraud.Finding {
	f := fraud.Finding{Signal: "provider_inconsistency", State: "inconclusive", Reason: "fraud.input_unavailable", Sources: []string{}, Groups: []string{}}
	if c.Thresholds[f.Signal] == 0 {
		return f
	}
	type outcome struct{ provider, state, source string }
	groups := map[string][]outcome{}
	required := map[string]map[string]bool{}
	for _, m := range c.Mappings {
		if m.Signal != f.Signal || m.RiskOutcome != "disagreement" {
			continue
		}
		if required[m.Group] == nil {
			required[m.Group] = map[string]bool{}
		}
		required[m.Group][m.RunnerID] = true
		for _, check := range p.Checks {
			if check.Attempt.RunnerKind != "provider" || check.Attempt.RunnerID != m.RunnerID || check.Attempt.PackageDigest != m.PackageDigest {
				continue
			}
			for _, o := range check.Observations {
				if o.SignalName == m.Observation && o.SignalOutcome != "inconclusive" {
					groups[m.Group] = append(groups[m.Group], outcome{m.RunnerID, o.SignalOutcome, o.ObservationID.String()})
				}
			}
		}
	}
	complete := len(required) > 0
	for group := range required {
		providers := map[string]bool{}
		different := false
		for _, a := range groups[group] {
			providers[a.provider] = true
			f.Sources = append(f.Sources, a.source)
			for _, b := range groups[group] {
				if a.provider != b.provider && a.state != b.state {
					different = true
				}
			}
		}
		if len(providers) < len(required[group]) || len(providers) < 2 {
			complete = false
		}
		if different {
			f.Count++
			f.Groups = append(f.Groups, group)
		}
	}
	if f.Count >= c.Thresholds[f.Signal] {
		f.State = "not_satisfied"
		f.Reason = "fraud.review_required"
	} else if complete {
		f.State = "satisfied"
		f.Reason = "fraud.no_pattern_in_window"
	}
	canonicalize(&f)
	return f
}

// Attribute comparisons stay inside the verification and canonical namespace.
// They do not merge verification-local subjects or establish which claim is true.
func attributeInconsistency(c fraud.Configuration, p policypg.Projection, links []link) fraud.Finding {
	f := fraud.Finding{Signal: "identity_inconsistency", State: "inconclusive", Reason: "fraud.input_unavailable", Sources: []string{}, Groups: []string{}}
	if c.Thresholds[f.Signal] == 0 {
		return f
	}
	groups := map[string][]link{}
	for _, l := range links {
		if l.verification == p.VerificationID.String() && slices.Contains([]string{"identifier", "address"}, l.kind) {
			groups[l.kind+"."+l.namespace] = append(groups[l.kind+"."+l.namespace], l)
		}
	}
	compared := false
	for group, values := range groups {
		different := false
		for _, a := range values {
			for _, b := range values {
				if a.evidence != b.evidence || a.actor != b.actor {
					compared = true
					f.Sources = append(f.Sources, a.evidence, b.evidence)
					if a.token != b.token {
						different = true
					}
				}
			}
		}
		if different {
			f.Count++
			f.Groups = append(f.Groups, group)
		}
	}
	if f.Count >= c.Thresholds[f.Signal] {
		f.State = "not_satisfied"
		f.Reason = "fraud.review_required"
	} else if compared {
		f.State = "satisfied"
		f.Reason = "fraud.no_pattern_in_window"
	}
	canonicalize(&f)
	return f
}
