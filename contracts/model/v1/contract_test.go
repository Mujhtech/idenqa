package model_test

import (
	"reflect"
	"testing"

	modelv1 "github.com/Mujhtech/idenqa/contracts/model/v1"
)

func TestEnvelopesCannotCarryRawEvidenceOrDynamicSecretValues(t *testing.T) {
	t.Parallel()

	for _, value := range []reflect.Type{reflect.TypeFor[modelv1.Request](), reflect.TypeFor[modelv1.Result]()} {
		for index := range value.NumField() {
			field := value.Field(index).Type
			if field.Kind() == reflect.Interface || field.Kind() == reflect.Map ||
				(field.Kind() == reflect.Slice && field.Elem().Kind() == reflect.Uint8) {
				t.Fatalf("unsafe payload field %s has type %s", value.Field(index).Name, field)
			}
		}
	}
}

func TestContractCompatibility(t *testing.T) {
	t.Parallel()

	if !modelv1.CurrentVersion.Accepts(modelv1.Version{Major: 1}) ||
		modelv1.CurrentVersion.Accepts(modelv1.Version{Major: 2}) ||
		modelv1.CurrentVersion.Accepts(modelv1.Version{Major: 1, Minor: 1}) {
		t.Fatal("model contract compatibility is not major-stable and minor-monotonic")
	}
}
