//go:build integration

package integration_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/access"
	auditpostgres "github.com/Mujhtech/idenqa/internal/audit/postgres"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	idenqapostgres "github.com/Mujhtech/idenqa/internal/platform/postgres"
	"github.com/Mujhtech/idenqa/internal/policy"
	policypostgres "github.com/Mujhtech/idenqa/internal/policy/postgres"
	"github.com/Mujhtech/idenqa/internal/tenant"
	"github.com/Mujhtech/idenqa/internal/tenantexport"
	tenantexportpostgres "github.com/Mujhtech/idenqa/internal/tenantexport/postgres"
)

// TestTenantExportStreamsCompleteIsolatedPortableData proves the bounded NDJSON
// export covers every collection for exactly one tenant, carries a valid
// digest, reproduces byte-canonical decision bundles, and never leaks
// ciphertext, endpoint secrets, provider payloads, or other-tenant rows. All
// reads run under the restricted runtime role so tenant scope and row-level
// security both apply.
func TestTenantExportStreamsCompleteIsolatedPortableData(t *testing.T) {
	database := createIsolatedDatabase(t)
	ctx := t.Context()
	migrator, err := idenqapostgres.OpenMigrator(ctx, migrationConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := migrator.Up(ctx); err != nil {
		t.Fatal(err)
	}
	if err := migrator.Close(); err != nil {
		t.Fatal(err)
	}
	admin, err := idenqapostgres.Open(ctx, poolConfig(database.url))
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	ids, err := id.NewSystemGenerator()
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)

	firstTenant, firstVerification := seedExecutionVerification(t, admin, ids, now)
	secondTenant, secondVerification := seedExecutionVerification(t, admin, ids, now)
	firstScope, err := tenant.NewScope(firstTenant)
	if err != nil {
		t.Fatal(err)
	}
	secondScope, err := tenant.NewScope(secondTenant)
	if err != nil {
		t.Fatal(err)
	}
	decisionStore, err := policypostgres.New(admin, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	firstDecision := newIntegrationDecision(t, ids, firstTenant, firstVerification, id.Decision{}, now)
	secondDecision := newIntegrationDecision(t, ids, secondTenant, secondVerification, id.Decision{}, now)
	if err := decisionStore.Append(ctx, firstScope, firstDecision); err != nil {
		t.Fatalf("append first decision: %v", err)
	}
	if err := decisionStore.Append(ctx, secondScope, secondDecision); err != nil {
		t.Fatalf("append second decision: %v", err)
	}
	auditStore, err := auditpostgres.New(admin)
	if err != nil {
		t.Fatal(err)
	}
	auditEventID, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auditStore.Append(ctx, firstScope, auditpostgres.Event{
		EventID: auditEventID.String(), EventType: "verification.decision.v1",
		AggregateID: firstVerification.String(), ActorID: "worker.policy",
		EventDigest: strings.Repeat("a", 64), OccurredAt: now,
	}); err != nil {
		t.Fatalf("append audit record: %v", err)
	}

	keyID, err := ids.NewAPIKey()
	if err != nil {
		t.Fatal(err)
	}
	policyID, err := ids.NewPolicy()
	if err != nil {
		t.Fatal(err)
	}
	checkID, err := ids.NewCheck()
	if err != nil {
		t.Fatal(err)
	}
	transitionEventID, err := ids.NewEvent()
	if err != nil {
		t.Fatal(err)
	}
	attemptID, err := ids.NewAttempt()
	if err != nil {
		t.Fatal(err)
	}
	endpointID, err := ids.NewWebhookEndpoint()
	if err != nil {
		t.Fatal(err)
	}
	caseID, err := ids.NewReviewCase()
	if err != nil {
		t.Fatal(err)
	}
	findingID, err := ids.NewFinding()
	if err != nil {
		t.Fatal(err)
	}
	subjectID, err := ids.NewSubject()
	if err != nil {
		t.Fatal(err)
	}
	recordID, err := ids.NewObservation()
	if err != nil {
		t.Fatal(err)
	}
	evidenceID, err := ids.NewEvidence()
	if err != nil {
		t.Fatal(err)
	}
	deletionID, err := ids.NewDeletion()
	if err != nil {
		t.Fatal(err)
	}
	holdID, err := ids.NewLegalHold()
	if err != nil {
		t.Fatal(err)
	}

	digest := "sha256:" + sixtyFour('a')
	hexDigest := sixtyFour('b')
	secretSentinel := []byte("WEBHOOK-SECRET-SENTINEL")
	valueSentinel := []byte("IDENTITY-VALUE-SENTINEL")
	providerSentinel := "IDENTITY-PROVIDER-PAYLOAD-SENTINEL"
	wrappedKeySentinel := []byte("EVIDENCE-WRAPPED-KEY-SENTINEL")
	tokenSentinel := sixtyFour('d')

	err = admin.WithinTransaction(ctx, idenqapostgres.TransactionOptions{}, func(ctx context.Context, tx idenqapostgres.Transaction) error {
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.api_keys
			(id,tenant_id,label,digest,pepper_version,requested_scopes,resolved_scopes,version,created_at,updated_at)
			VALUES ($1,$2,'export fixture',$3,1,ARRAY['tenant:read'],ARRAY['tenant:read'],1,$4,$4)`,
			keyID.String(), firstTenant.String(), bytes.Repeat([]byte{7}, 32), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.subjects (id,tenant_id,verification_id,created_at)
			VALUES ($1,$2,$3,$4)`, subjectID.String(), firstTenant.String(), firstVerification.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.policies (tenant_id,id,activation_version,active_revision,created_at,updated_at)
			VALUES ($1,$2,1,1,$3,$3)`, firstTenant.String(), policyID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.policy_revisions
			(tenant_id,policy_id,revision,schema_major,schema_minor,digest,evaluator_major,evaluator_minor,evaluator_digest,canonical,created_at)
			VALUES ($1,$2,1,1,0,$3,1,0,$4,'{}',$5)`,
			firstTenant.String(), policyID.String(), hexDigest, sixtyFour('c'), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.policy_activations
			(tenant_id,policy_id,activation_version,revision,previous_revision,actor_key_id,activated_at)
			VALUES ($1,$2,1,1,NULL,$3,$4)`, firstTenant.String(), policyID.String(), keyID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.verification_transitions
			(tenant_id,event_id,verification_id,from_state,to_state,expected_version,resulting_version,decision_id,actor_id,command_digest,occurred_at)
			VALUES ($1,$2,$3,'created','collecting',1,2,NULL,$4,$5,$6)`,
			firstTenant.String(), transitionEventID.String(), firstVerification.String(), keyID.String(), hexDigest, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.verification_checks
			(id,tenant_id,verification_id,name,state,outcome,version,created_at,updated_at)
			VALUES ($1,$2,$3,'document_authenticity','completed','passed',1,$4,$4)`,
			checkID.String(), firstTenant.String(), firstVerification.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.verification_attempts
			(id,tenant_id,verification_id,check_id,attempt_number,fence,runner_kind,runner_id,runner_version,
			 package_digest,contract_major,contract_minor,request_digest,configuration_digest,state,
			 started_at,deadline,finished_at,failure_class,failure_code,retry_disposition,retry_after_milliseconds,result_digest)
			VALUES ($1,$2,$3,$4,1,1,'provider','synthetic.runner','1.0.0',$5,1,0,$5,$5,'completed',$6,$7,$8,NULL,NULL,NULL,NULL,$5)`,
			attemptID.String(), firstTenant.String(), firstVerification.String(), checkID.String(),
			hexDigest, now, now.Add(time.Minute), now.Add(30*time.Second)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_endpoints
			(tenant_id,id,url,secret_version,version,created_at,updated_at,event_types)
			VALUES ($1,$2,'https://hooks.example.test/tenant-export',1,1,$3,$3,ARRAY['verification.completed'])`,
			firstTenant.String(), endpointID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.webhook_secrets
			(tenant_id,endpoint_id,version,provider,reference,key_version,algorithm,ciphertext,created_at)
			VALUES ($1,$2,1,'integration','keyring','v1','TEST',$3,$4)`,
			firstTenant.String(), endpointID.String(), secretSentinel, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_cases
			(tenant_id,id,verification_id,challenged_decision_id,region,required_certification,oversight,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,'ng-1','certificate','single','open',1,$5,$5)`,
			firstTenant.String(), caseID.String(), firstVerification.String(), firstDecision.ID().String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.review_findings
			(tenant_id,case_id,id,reviewer_id,resolution,reason_code,evidence_grant_ids,recorded_at)
			VALUES ($1,$2,$3,'reviewer.alex','satisfy','document_match','[]'::jsonb,$4)`,
			firstTenant.String(), caseID.String(), findingID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.identity_subjects
			(tenant_id,id,region,state,version,created_at,updated_at)
			VALUES ($1,$2,'ng-1','active',1,$3,$3)`,
			firstTenant.String(), subjectID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.identity_subject_verifications
			(tenant_id,subject_id,verification_id,actor_key_id,linked_at)
			VALUES ($1,$2,$3,$4,$5)`,
			firstTenant.String(), subjectID.String(), firstVerification.String(), keyID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.identity_records
			(tenant_id,id,subject_id,verification_id,kind,name,sequence,series_id,supersedes,metadata,recorded_at,retain_until)
			VALUES ($1,$2,$3,$4,'observation','document.full_name',1,$2,NULL,$5,$6,$7)`,
			firstTenant.String(), recordID.String(), subjectID.String(), firstVerification.String(),
			[]byte(`{"provider_payload":"`+providerSentinel+`"}`), now, now.Add(24*time.Hour)); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.identity_record_values
			(tenant_id,record_id,subject_id,ciphertext) VALUES ($1,$2,$3,$4)`,
			firstTenant.String(), recordID.String(), subjectID.String(), valueSentinel); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.identity_identifier_tokens
			(tenant_id,subject_id,record_id,region,namespace,issuer,key_version,token)
			VALUES ($1,$2,$3,'ng-1','document','ng-nin',1,$4)`,
			firstTenant.String(), subjectID.String(), recordID.String(), tokenSentinel); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.evidence_assets
			(id,tenant_id,subject_id,verification_id,requirement_key,evidence_type,artefact,acquisition_method,assurances,
			 registry_schema_version,registry_revision,registry_digest,region,retention_class,content_revision,
			 object_key,object_version,ciphertext_size,ciphertext_checksum,envelope_format_version,content_algorithm,key_purpose,
			 key_provider,key_reference,key_version,key_algorithm,wrapped_key,context_schema_version,context_digest,plaintext_digest,
			 media_type,integrity,state,version,created_at,updated_at)
			VALUES ($1,$2,$3,$4,'selfie','selfie','image','upload',ARRAY[]::text[],1,1,$5,'ng-1','raw_evidence',1,
			 $6,'v1',16,$5,1,'test.aead','evidence.content','test.provider','key.reference','v1','test.wrap',$7,1,$5,$5,
			 'image/jpeg','verified','available',1,$8,$8)`,
			evidenceID.String(), firstTenant.String(), subjectID.String(), firstVerification.String(),
			digest, "tenants/"+firstTenant.String()+"/evidence/"+evidenceID.String()+"/content/1",
			wrappedKeySentinel, now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.fraud_configurations
			(tenant_id,version,configuration,digest,actor_key_id,recorded_at)
			VALUES ($1,1,$2,$3,$4,$5)`, firstTenant.String(),
			[]byte(`{"enabled":true,"region":"ng-1","window_seconds":3600,"retention_seconds":7200,"minimum_session_seconds":0,"maximum_session_seconds":600,"thresholds":{},"sources":[],"mappings":[]}`),
			hexDigest, keyID.String(), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.deletion_requests
			(tenant_id,id,aggregate_id,region,state,backup_expires_at,backup_retention_seconds,version,requested_at,updated_at)
			VALUES ($1,$2,$3,'ng-1','requested',$4,0,1,$5,$5)`,
			firstTenant.String(), deletionID.String(), firstVerification.String(), now.Add(35*24*time.Hour), now); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO idenqa.legal_holds
			(tenant_id,id,aggregate_id,authority,reason,starts_at,review_at,created_at)
			VALUES ($1,$2,$3,'court-order','pending proceedings',$4,$5,$6)`,
			firstTenant.String(), holdID.String(), firstVerification.String(),
			now.Add(-time.Hour), now.Add(24*time.Hour), now.Add(-2*time.Hour)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed export fixture: %v", err)
	}

	runtimeConfig := poolConfig(database.url)
	runtimeConfig.Role = database.createRuntimeRole(t)
	runtime, err := idenqapostgres.Open(ctx, runtimeConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	runtimeDecisionStore, err := policypostgres.New(runtime, integrationProtector{})
	if err != nil {
		t.Fatal(err)
	}
	exportStore, err := tenantexportpostgres.New(runtime, runtimeDecisionStore)
	if err != nil {
		t.Fatal(err)
	}
	exporter, err := tenantexport.NewExporter(tenantexport.Sources{
		Tenant:                  exportStore,
		CaptureProfiles:         exportStore,
		Policies:                exportStore,
		PolicyRevisions:         exportStore,
		PolicyActivations:       exportStore,
		Verifications:           exportStore,
		VerificationTransitions: exportStore,
		VerificationChecks:      exportStore,
		VerificationAttempts:    exportStore,
		Decisions:               exportStore,
		AuditRecords:            exportStore,
		WebhookEndpoints:        exportStore,
		ReviewCases:             exportStore,
		ReviewFindings:          exportStore,
		IdentitySubjects:        exportStore,
		IdentityRecords:         exportStore,
		EvidenceAssets:          exportStore,
		FraudConfiguration:      exportStore,
		PrivacyDeletions:        exportStore,
		PrivacyHolds:            exportStore,
	}, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	authority := integrationExportAuthority{scope: firstScope}
	var output bytes.Buffer
	if err := exporter.Export(ctx, authority, nil, func(line []byte) error {
		_, err := output.Write(line)
		return err
	}); err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	material := output.Bytes()
	lines := bytes.Split(bytes.TrimSuffix(material, []byte("\n")), []byte("\n"))
	if len(lines) < 3 {
		t.Fatalf("export produced %d lines", len(lines))
	}
	first := decodeExportLine(t, lines[0])
	if exportText(t, first, "record") != "header" || exportText(t, first, "tenant_id") != firstTenant.String() {
		t.Fatalf("header = %s", lines[0])
	}
	footer := decodeExportLine(t, lines[len(lines)-1])
	if exportText(t, footer, "record") != "footer" {
		t.Fatalf("footer = %s", lines[len(lines)-1])
	}
	hash := sha256.New()
	for _, line := range lines[:len(lines)-1] {
		_, _ = hash.Write(line)
		_, _ = hash.Write([]byte{'\n'})
	}
	if want := "sha256:" + hex.EncodeToString(hash.Sum(nil)); exportText(t, footer, "digest") != want {
		t.Fatalf("footer digest = %s, want %s", footer["digest"], want)
	}
	counts := map[string]int64{}
	if err := json.Unmarshal([]byte(footer["counts"]), &counts); err != nil {
		t.Fatalf("decode footer counts: %v", err)
	}
	for _, collection := range tenantexport.AllCollections() {
		if counts[string(collection)] < 1 {
			t.Fatalf("collection %s count = %d, want at least 1", collection, counts[string(collection)])
		}
	}
	if counts["decisions"] != 1 || counts["audit_records"] != 1 || counts["tenant"] != 1 {
		t.Fatalf("counts = %v", counts)
	}

	for _, foreign := range []string{secondTenant.String(), secondVerification.String(), secondDecision.ID().String()} {
		if bytes.Contains(material, []byte(foreign)) {
			t.Fatalf("export contains other-tenant identifier %q", foreign)
		}
	}
	for _, sentinel := range [][]byte{secretSentinel, valueSentinel, wrappedKeySentinel, []byte(providerSentinel), []byte(tokenSentinel)} {
		if bytes.Contains(material, sentinel) {
			t.Fatalf("export contains protected sentinel %q", sentinel)
		}
	}

	expectedBundle, _, err := policy.NewDecisionBundle(firstDecision)
	if err != nil {
		t.Fatal(err)
	}
	decisionLine := -1
	for index, line := range lines {
		fields := decodeExportLine(t, line)
		if exportText(t, fields, "record") == "decisions" {
			decisionLine = index
			if exportText(t, fields, "decision_id") != firstDecision.ID().String() {
				t.Fatalf("decision record = %s", line)
			}
			if string(fields["bundle"]) != string(expectedBundle.Canonical()) {
				t.Fatalf("decision bundle is not byte-canonical for %s", firstDecision.ID())
			}
		}
	}
	if decisionLine < 0 {
		t.Fatal("export contained no decision record")
	}
}

type integrationExportAuthority struct{ scope tenant.Scope }

func (authority integrationExportAuthority) TenantScope() tenant.Scope { return authority.scope }

func (integrationExportAuthority) Require(permission access.Permission) error {
	if permission != access.PermissionTenantExport {
		return access.ErrInsufficientScope
	}
	return nil
}

func decodeExportLine(t *testing.T, line []byte) map[string]json.RawMessage {
	t.Helper()

	fields := map[string]json.RawMessage{}
	if err := json.Unmarshal(line, &fields); err != nil {
		t.Fatalf("decode export line %q: %v", line, err)
	}
	return fields
}

func exportText(t *testing.T, fields map[string]json.RawMessage, name string) string {
	t.Helper()

	value := ""
	if err := json.Unmarshal(fields[name], &value); err != nil {
		t.Fatalf("decode export field %q: %v", name, err)
	}
	return value
}
