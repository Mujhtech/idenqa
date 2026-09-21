package idenqa

import "testing"

const testCLIKey = "key_01ARZ3NDEKTSV4RRFFQ69G5FAV"

func TestValidateKMSOperation(t *testing.T) {
	t.Parallel()

	valid := func() *kmsOptions {
		return &kmsOptions{
			envFile: ".env", tenantID: "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV", domain: "identity.identifier.v1",
			actorKey: testCLIKey, reason: "rotate identifier keys", confirmation: true,
		}
	}
	tests := []struct {
		name      string
		operation string
		mutate    func(*kmsOptions)
		wantErr   bool
	}{
		{name: "create valid", operation: "create"},
		{name: "rotate requires version", operation: "rotate", wantErr: true},
		{name: "rotate valid", operation: "rotate", mutate: func(options *kmsOptions) { options.expectedVersion = 2 }},
		{name: "disable mismatched version", operation: "disable", mutate: func(options *kmsOptions) {
			options.version, options.expectedVersion = 1, 2
		}, wantErr: true},
		{name: "disable valid", operation: "disable", mutate: func(options *kmsOptions) { options.version, options.expectedVersion = 1, 1 }},
		{name: "missing actor", operation: "create", mutate: func(options *kmsOptions) { options.actorKey = "" }, wantErr: true},
		{name: "invalid domain", operation: "create", mutate: func(options *kmsOptions) { options.domain = "Invalid" }, wantErr: true},
		{name: "unconfirmed", operation: "create", mutate: func(options *kmsOptions) { options.confirmation = false }, wantErr: true},
		{name: "short reason", operation: "create", mutate: func(options *kmsOptions) { options.reason = "short" }, wantErr: true},
		{name: "show read only", operation: "show", mutate: func(options *kmsOptions) { options.actorKey, options.confirmation, options.reason = "", false, "" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := valid()
			if test.mutate != nil {
				test.mutate(options)
			}
			err := validateKMSOperation(test.operation, options)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateKMSOperation() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
}

func TestValidateSupportTransitions(t *testing.T) {
	t.Parallel()

	valid := func() *supportOptions {
		return &supportOptions{
			envFile: ".env", tenantID: "ten_01ARZ3NDEKTSV4RRFFQ69G5FAV", actorKey: testCLIKey,
			identifier: "spt_01ARZ3NDEKTSV4RRFFQ69G5FAV", expectedVersion: 1, reason: "revoke delegated grant", confirmation: true,
		}
	}
	tests := []struct {
		name    string
		mutate  func(*supportOptions)
		wantErr bool
	}{
		{name: "valid"},
		{name: "missing identifier", mutate: func(options *supportOptions) { options.identifier = "" }, wantErr: true},
		{name: "missing version", mutate: func(options *supportOptions) { options.expectedVersion = 0 }, wantErr: true},
		{name: "unconfirmed", mutate: func(options *supportOptions) { options.confirmation = false }, wantErr: true},
		{name: "short reason", mutate: func(options *supportOptions) { options.reason = "short" }, wantErr: true},
		{name: "invalid actor", mutate: func(options *supportOptions) { options.actorKey = "not-a-key" }, wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			options := valid()
			if test.mutate != nil {
				test.mutate(options)
			}
			err := validateSupportVersioned(options)
			if (err != nil) != test.wantErr {
				t.Fatalf("validateSupportVersioned() error = %v, wantErr %t", err, test.wantErr)
			}
		})
	}
	if err := validateSupportActor(&supportOptions{tenantID: "invalid", actorKey: testCLIKey}); err == nil {
		t.Fatal("validateSupportActor accepted an invalid tenant")
	}
}
