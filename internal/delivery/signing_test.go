package delivery

import (
	"testing"
	"time"

	"github.com/Mujhtech/idenqa/internal/platform/id"
)

func TestSignIsByteExactAndDeterministic(t *testing.T) {
	eventID, _ := id.ParseEvent("evt_01ARZ3NDEKTSV4RRFFQ69G5FAV")
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	first, err := Sign([]byte("01234567890123456789012345678901"), eventID, now, []byte("{}"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := Sign([]byte("01234567890123456789012345678901"), eventID, now, []byte("{}"))
	if err != nil || first != second {
		t.Fatalf("signature changed: %#v %#v", first, second)
	}
	changed, _ := Sign([]byte("01234567890123456789012345678901"), eventID, now, []byte("{ }"))
	if changed.Value == first.Value {
		t.Fatal("changed body retained signature")
	}
}
