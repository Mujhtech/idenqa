package task

import (
	"errors"
	"testing"

	platformtask "github.com/Mujhtech/idenqa/internal/platform/task"
)

func TestDecodeAcceptsOnlyReferencePayloadsWithAttempt(t *testing.T) {
	t.Parallel()

	const deliveryID = "dlv_01ARZ3NDEKTSV4RRFFQ69G5FAV"

	tests := []struct {
		name    string
		payload string
		wantErr bool
	}{
		{name: "first attempt", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":1}`},
		{name: "last attempt", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":20}`},
		{name: "retired payload without attempt", payload: `{"delivery_id":"` + deliveryID + `"}`, wantErr: true},
		{name: "zero attempt", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":0}`, wantErr: true},
		{name: "negative attempt", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":-1}`, wantErr: true},
		{name: "attempt above bound", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":21}`, wantErr: true},
		{name: "unknown field", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":1,"legacy":true}`, wantErr: true},
		{name: "trailing document", payload: `{"delivery_id":"` + deliveryID + `","attempt_number":1}{}`, wantErr: true},
		{name: "invalid delivery identifier", payload: `{"delivery_id":"not-a-delivery","attempt_number":1}`, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			parsed, err := Decode([]byte(test.payload))
			if test.wantErr {
				if !errors.Is(err, platformtask.ErrInvalid) {
					t.Fatalf("Decode() error = %v, want ErrInvalid", err)
				}

				return
			}
			if err != nil || parsed.String() != deliveryID {
				t.Fatalf("Decode() = %s, %v, want %s", parsed, err, deliveryID)
			}
		})
	}
}
