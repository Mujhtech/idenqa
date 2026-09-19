package idenqa

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/buildinfo"
	"github.com/Mujhtech/idenqa/internal/cli"
	"github.com/Mujhtech/idenqa/internal/config"
	localkms "github.com/Mujhtech/idenqa/internal/platform/kms/local"
	"github.com/Mujhtech/idenqa/internal/platform/postgres"
	taskheadgate "github.com/Mujhtech/idenqa/internal/platform/task/headgate"
)

type fakeDoctorDatabase struct {
	pingErr     error
	headgate    taskheadgate.MigrationReport
	headgateErr error
	closed      bool
}

func (database *fakeDoctorDatabase) Ping(context.Context) error { return database.pingErr }

func (database *fakeDoctorDatabase) CheckHeadgate(
	context.Context,
	string,
) (taskheadgate.MigrationReport, error) {
	return database.headgate, database.headgateErr
}

func (database *fakeDoctorDatabase) Close() { database.closed = true }

type fakeDoctorBackend struct {
	database     doctorDatabase
	openErr      error
	migration    postgres.MigrationReport
	migrationErr error
}

func (backend fakeDoctorBackend) OpenDatabase(context.Context, config.API) (doctorDatabase, error) {
	return backend.database, backend.openErr
}

func (backend fakeDoctorBackend) ApplicationMigration(
	context.Context,
	config.API,
) (postgres.MigrationReport, error) {
	return backend.migration, backend.migrationErr
}

func setDoctorEnvironment(t *testing.T, directory, keyringFile string) {
	t.Helper()
	t.Setenv("IDENQA_ENVIRONMENT", "test")
	t.Setenv("IDENQA_DATABASE_URL", "postgres://idenqa:doctor-secret@127.0.0.1:5432/idenqa?sslmode=disable")
	t.Setenv("IDENQA_EVIDENCE_LOCAL_DIRECTORY", directory)
	t.Setenv("IDENQA_EVIDENCE_LOCAL_KEYRING_FILE", keyringFile)
	t.Setenv("IDENQA_API_KEY", "")
	t.Setenv("IDENQA_PROVIDER_RUNTIME_FILE", "")
	t.Setenv("IDENQA_MODEL_RUNTIME_FILE", "")
	t.Setenv("IDENQA_REVIEW_AUTHORITY_FILE", "")
}

func createDoctorKeyring(t *testing.T) (string, string) {
	t.Helper()
	directory := t.TempDir()
	keyringFile := filepath.Join(directory, "keyring.json")
	keyring, err := localkms.Create(keyringFile)
	if err != nil {
		t.Fatalf("create keyring: %v", err)
	}
	if err := keyring.Close(); err != nil {
		t.Fatalf("close keyring: %v", err)
	}
	return directory, keyringFile
}

func runDoctor(t *testing.T, options *doctorOptions, backend doctorBackend) (doctorReport, string, error) {
	t.Helper()
	command := newDoctorCommand()
	command.SetContext(t.Context())
	var output bytes.Buffer
	command.SetOut(&output)
	err := executeDoctor(command, options, backend)
	var report doctorReport
	if decodeErr := json.Unmarshal(output.Bytes(), &report); decodeErr != nil {
		t.Fatalf("decode doctor JSON report %q: %v", output.String(), decodeErr)
	}
	return report, output.String(), err
}

func doctorCheckByName(t *testing.T, report doctorReport, name string) doctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("doctor report has no %q check: %+v", name, report.Checks)
	return doctorCheck{}
}

func TestDoctorReportsCleanInstallBaselineAndRedactsSecrets(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	setDoctorEnvironment(t, directory, keyringFile)
	keyFile := filepath.Join(directory, "api-key")
	if err := os.WriteFile(keyFile, []byte("doctor-credential\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/readyz":
			_, _ = writer.Write([]byte("ok\n"))
		case "/v1/tenant":
			if request.Header.Get("Authorization") != "Bearer doctor-credential" {
				t.Error("doctor credential was not presented")
			}
			_, _ = writer.Write([]byte(`{"id":"ten_01ARZ3NDEKTSV4RRFFQ69G5FAV","state":"active"}`))
		default:
			t.Errorf("unexpected doctor request %s", request.URL.Path)
		}
	}))
	defer api.Close()

	backend := fakeDoctorBackend{
		database: &fakeDoctorDatabase{
			headgate:    taskheadgate.MigrationReport{State: "empty", Latest: 3},
			headgateErr: errors.New("headgate schema is incompatible"),
		},
		migration: postgres.MigrationReport{Current: 63, Latest: 63},
	}
	report, output, err := runDoctor(t, &doctorOptions{
		apiURL: api.URL, keyFile: keyFile, jsonOutput: true,
	}, backend)
	if err != nil {
		t.Fatalf("doctor clean baseline failed: %v", err)
	}
	if !report.OK || report.Failures != 0 || report.Warnings != 1 {
		t.Fatalf("report = %+v", report)
	}
	for _, name := range []string{"configuration", "database", "migrations", "object-storage", "key-provider", "runner", "api-reachability", "api-credential"} {
		if check := doctorCheckByName(t, report, name); check.Status != doctorStatusPass {
			t.Fatalf("%s status = %q, want pass", name, check.Status)
		}
	}
	if check := doctorCheckByName(t, report, "headgate"); check.Status != doctorStatusWarn {
		t.Fatalf("headgate status = %q, want warn", check.Status)
	}
	for _, secret := range []string{"doctor-secret", "doctor-credential"} {
		if strings.Contains(output, secret) {
			t.Fatalf("diagnostic report leaked %q", secret)
		}
	}
}

func TestDoctorFailsWhenPostgreSQLIsUnavailable(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	setDoctorEnvironment(t, directory, keyringFile)
	backend := fakeDoctorBackend{openErr: postgres.ErrUnavailable}
	report, _, err := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
	if err == nil {
		t.Fatal("doctor accepted an unavailable PostgreSQL")
	}
	if check := doctorCheckByName(t, report, "database"); check.Status != doctorStatusFail {
		t.Fatalf("database status = %q, want fail", check.Status)
	}
	if check := doctorCheckByName(t, report, "migrations"); check.Status != doctorStatusSkip {
		t.Fatalf("migrations status = %q, want skip", check.Status)
	}
}

func TestDoctorClassifiesApplicationMigrationStates(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	setDoctorEnvironment(t, directory, keyringFile)
	tests := []struct {
		name       string
		report     postgres.MigrationReport
		err        error
		wantStatus string
	}{
		{name: "current", report: postgres.MigrationReport{Current: 63, Latest: 63}, wantStatus: doctorStatusPass},
		{name: "pending", report: postgres.MigrationReport{Current: 60, Latest: 63, Pending: true}, wantStatus: doctorStatusWarn},
		{name: "dirty", report: postgres.MigrationReport{Current: 61, Latest: 63}, err: postgres.ErrMigrationDirty, wantStatus: doctorStatusFail},
		{name: "newer", report: postgres.MigrationReport{Current: 64, Latest: 63}, err: postgres.ErrMigrationNewer, wantStatus: doctorStatusFail},
		{name: "unavailable", err: postgres.ErrUnavailable, wantStatus: doctorStatusFail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := &fakeDoctorDatabase{headgate: taskheadgate.MigrationReport{State: "versioned", Current: 3, Latest: 3, Healthy: true}}
			backend := fakeDoctorBackend{database: database, migration: test.report, migrationErr: test.err}
			report, _, _ := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
			if check := doctorCheckByName(t, report, "migrations"); check.Status != test.wantStatus {
				t.Fatalf("migrations status = %q, want %q (%s)", check.Status, test.wantStatus, check.Detail)
			}
			if !database.closed {
				t.Fatal("doctor did not release the PostgreSQL connection")
			}
		})
	}
}

func TestDoctorClassifiesHeadgateSchemaStates(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	setDoctorEnvironment(t, directory, keyringFile)
	tests := []struct {
		name       string
		report     taskheadgate.MigrationReport
		err        error
		wantStatus string
	}{
		{name: "current", report: taskheadgate.MigrationReport{State: "versioned", Current: 3, Latest: 3, Healthy: true}, wantStatus: doctorStatusPass},
		{name: "pending", report: taskheadgate.MigrationReport{State: "versioned", Current: 2, Latest: 3, Pending: true}, err: errors.New("headgate schema is incompatible"), wantStatus: doctorStatusWarn},
		{name: "empty", report: taskheadgate.MigrationReport{State: "empty", Latest: 3}, err: errors.New("headgate schema is incompatible"), wantStatus: doctorStatusWarn},
		{name: "unversioned", report: taskheadgate.MigrationReport{State: "unversioned", Latest: 3}, err: errors.New("headgate schema is incompatible"), wantStatus: doctorStatusFail},
		{name: "newer", report: taskheadgate.MigrationReport{State: "versioned", Current: 4, Latest: 3, Healthy: true}, wantStatus: doctorStatusFail},
		{name: "failed", err: taskheadgateMigrationFailure(), wantStatus: doctorStatusFail},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			backend := fakeDoctorBackend{
				database:  &fakeDoctorDatabase{headgate: test.report, headgateErr: test.err},
				migration: postgres.MigrationReport{Current: 63, Latest: 63},
			}
			report, _, _ := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
			if check := doctorCheckByName(t, report, "headgate"); check.Status != test.wantStatus {
				t.Fatalf("headgate status = %q, want %q (%s)", check.Status, test.wantStatus, check.Detail)
			}
		})
	}
}

func taskheadgateMigrationFailure() error { return errors.New("headgate schema check failed") }

func TestDoctorReportsLocalStorageAndRunnerConfiguration(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	backend := fakeDoctorBackend{
		database:  &fakeDoctorDatabase{headgate: taskheadgate.MigrationReport{State: "versioned", Current: 3, Latest: 3, Healthy: true}},
		migration: postgres.MigrationReport{Current: 63, Latest: 63},
	}
	t.Run("missing local evidence paths warn", func(t *testing.T) {
		setDoctorEnvironment(t, "", "")
		report, _, err := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
		if err != nil {
			t.Fatalf("doctor failed on warnings: %v", err)
		}
		if check := doctorCheckByName(t, report, "object-storage"); check.Status != doctorStatusWarn {
			t.Fatalf("object-storage status = %q, want warn", check.Status)
		}
		if check := doctorCheckByName(t, report, "key-provider"); check.Status != doctorStatusWarn {
			t.Fatalf("key-provider status = %q, want warn", check.Status)
		}
	})
	t.Run("missing directory fails", func(t *testing.T) {
		setDoctorEnvironment(t, filepath.Join(directory, "absent"), keyringFile)
		report, _, err := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
		if err == nil {
			t.Fatal("doctor accepted an absent evidence directory")
		}
		if check := doctorCheckByName(t, report, "object-storage"); check.Status != doctorStatusFail {
			t.Fatalf("object-storage status = %q, want fail", check.Status)
		}
	})
	t.Run("invalid keyring fails", func(t *testing.T) {
		invalid := filepath.Join(directory, "invalid-keyring.json")
		if err := os.WriteFile(invalid, []byte(`{"schema_version":1}`), 0o600); err != nil {
			t.Fatal(err)
		}
		setDoctorEnvironment(t, directory, invalid)
		report, _, err := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
		if err == nil {
			t.Fatal("doctor accepted an invalid keyring")
		}
		if check := doctorCheckByName(t, report, "key-provider"); check.Status != doctorStatusFail {
			t.Fatalf("key-provider status = %q, want fail", check.Status)
		}
	})
	t.Run("mounted runner file is validated", func(t *testing.T) {
		setDoctorEnvironment(t, directory, keyringFile)
		providerFile := filepath.Join(directory, "provider.json")
		if err := os.WriteFile(providerFile, []byte(`{"not":"a provider runtime"}`), 0o600); err != nil {
			t.Fatal(err)
		}
		t.Setenv("IDENQA_PROVIDER_RUNTIME_FILE", providerFile)
		report, _, err := runDoctor(t, &doctorOptions{jsonOutput: true}, backend)
		if err == nil {
			t.Fatal("doctor accepted an invalid runner configuration")
		}
		if check := doctorCheckByName(t, report, "runner"); check.Status != doctorStatusFail {
			t.Fatalf("runner status = %q, want fail", check.Status)
		}
	})
}

func TestDoctorChecksAPICredentialStates(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	setDoctorEnvironment(t, directory, keyringFile)
	backend := fakeDoctorBackend{
		database:  &fakeDoctorDatabase{headgate: taskheadgate.MigrationReport{State: "versioned", Current: 3, Latest: 3, Healthy: true}},
		migration: postgres.MigrationReport{Current: 63, Latest: 63},
	}
	tests := []struct {
		name         string
		credential   bool
		readyStatus  int
		tenantStatus int
		wantState    string
		wantCred     string
	}{
		{name: "accepted credential", credential: true, readyStatus: 200, tenantStatus: 200, wantState: doctorStatusPass, wantCred: doctorStatusPass},
		{name: "rejected credential", credential: true, readyStatus: 200, tenantStatus: 401, wantState: doctorStatusPass, wantCred: doctorStatusFail},
		{name: "insufficient scope", credential: true, readyStatus: 200, tenantStatus: 403, wantState: doctorStatusPass, wantCred: doctorStatusWarn},
		{name: "unready API", credential: false, readyStatus: 503, tenantStatus: 200, wantState: doctorStatusFail, wantCred: doctorStatusSkip},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/readyz":
					writer.WriteHeader(test.readyStatus)
				case "/v1/tenant":
					writer.WriteHeader(test.tenantStatus)
					_, _ = writer.Write([]byte(`{}`))
				}
			}))
			defer api.Close()
			options := &doctorOptions{apiURL: api.URL, jsonOutput: true}
			if test.credential {
				keyFile := filepath.Join(t.TempDir(), "api-key")
				if err := os.WriteFile(keyFile, []byte("doctor-credential\n"), 0o600); err != nil {
					t.Fatal(err)
				}
				options.keyFile = keyFile
			}
			report, _, _ := runDoctor(t, options, backend)
			if check := doctorCheckByName(t, report, "api-reachability"); check.Status != test.wantState {
				t.Fatalf("api-reachability status = %q, want %q", check.Status, test.wantState)
			}
			if check := doctorCheckByName(t, report, "api-credential"); check.Status != test.wantCred {
				t.Fatalf("api-credential status = %q, want %q", check.Status, test.wantCred)
			}
		})
	}
}

func TestDoctorHumanSummaryReflectsWarningsAndFailures(t *testing.T) {
	directory, keyringFile := createDoctorKeyring(t)
	setDoctorEnvironment(t, directory, keyringFile)
	backend := fakeDoctorBackend{
		database:  &fakeDoctorDatabase{headgate: taskheadgate.MigrationReport{State: "empty", Latest: 3}, headgateErr: errors.New("headgate schema is incompatible")},
		migration: postgres.MigrationReport{Current: 63, Latest: 63},
	}
	command := newDoctorCommand()
	command.SetContext(t.Context())
	var output bytes.Buffer
	command.SetOut(&output)
	if err := executeDoctor(command, &doctorOptions{}, backend); err != nil {
		t.Fatalf("doctor warning-only run failed: %v", err)
	}
	if !strings.Contains(output.String(), "doctor ok checks=7 warnings=1 failures=0") {
		t.Fatalf("summary = %q", output.String())
	}

	backend = fakeDoctorBackend{openErr: postgres.ErrUnavailable}
	command = newDoctorCommand()
	command.SetContext(t.Context())
	output.Reset()
	command.SetOut(&output)
	if err := executeDoctor(command, &doctorOptions{}, backend); err == nil {
		t.Fatal("doctor failed run returned no error")
	}
	if !strings.Contains(output.String(), "doctor failed") {
		t.Fatalf("summary = %q", output.String())
	}
}

func TestDoctorCommandExitCodesForUsageAndConfiguration(t *testing.T) {
	t.Setenv("IDENQA_API_KEY", "")
	t.Setenv("IDENQA_DATABASE_URL", "")
	t.Setenv("IDENQA_DATABASE_ADMIN_URL", "")
	t.Setenv("IDENQA_EVIDENCE_LOCAL_DIRECTORY", "")
	t.Setenv("IDENQA_EVIDENCE_LOCAL_KEYRING_FILE", "")

	var stdout, stderr bytes.Buffer
	code := cli.ExecuteArgs(
		t.Context(), newRootCommand(buildinfo.Info{}),
		[]string{"doctor", "--env-file", "", "--api-url", "ftp://example.test"},
		&stdout, &stderr,
	)
	if code != 2 {
		t.Fatalf("usage error exit code = %d, want 2; stderr = %q", code, stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = cli.ExecuteArgs(
		t.Context(), newRootCommand(buildinfo.Info{}),
		[]string{"doctor", "--env-file", ""},
		&stdout, &stderr,
	)
	if code != 1 {
		t.Fatalf("configuration failure exit code = %d, want 1; stderr = %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "configuration") || !strings.Contains(stdout.String(), "doctor failed") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}
