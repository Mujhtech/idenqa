package provider_test

import (
	"reflect"
	"testing"

	providerv1 "github.com/Mujhtech/idenqa/contracts/provider/v1"
)

func TestEnvelopesCannotCarryRawEvidenceOrDynamicSecretValues(t *testing.T) {
	t.Parallel()

	assertSafeShape(t, reflect.TypeFor[providerv1.Request](), map[reflect.Type]bool{})
	assertSafeShape(t, reflect.TypeFor[providerv1.Result](), map[reflect.Type]bool{})
}

func TestContractCompatibility(t *testing.T) {
	t.Parallel()

	if !providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 1}) ||
		!providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 1, Minor: 1}) {
		t.Fatal("v1.1 must accept v1.0 and v1.1")
	}
	if providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 2}) ||
		providerv1.CurrentVersion.Accepts(providerv1.Version{Major: 1, Minor: 2}) {
		t.Fatal("v1.1 accepted an incompatible contract")
	}
}

func assertSafeShape(t *testing.T, value reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	if seen[value] {
		return
	}
	seen[value] = true
	if value.Kind() == reflect.Interface || value.Kind() == reflect.Map ||
		(value.Kind() == reflect.Slice && value.Elem().Kind() == reflect.Uint8) {
		t.Fatalf("unsafe payload type reachable from contract: %s", value)
	}
	switch value.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array:
		if value.Elem().PkgPath() == reflect.TypeFor[providerv1.Request]().PkgPath() {
			assertSafeShape(t, value.Elem(), seen)
		}
	case reflect.Struct:
		for index := range value.NumField() {
			field := value.Field(index).Type
			if field.PkgPath() == reflect.TypeFor[providerv1.Request]().PkgPath() ||
				((field.Kind() == reflect.Pointer || field.Kind() == reflect.Slice || field.Kind() == reflect.Array) &&
					field.Elem().PkgPath() == reflect.TypeFor[providerv1.Request]().PkgPath()) {
				assertSafeShape(t, field, seen)
			}
		}
	}
}
