package tenantexport

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"math"
	"testing"
)

func TestLineCapacityRejectsOverflow(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name              string
		encoded, nameSize int
		want              int
		invalid           bool
	}{
		{name: "normal", encoded: 2, nameSize: 6, want: 24},
		{name: "allocation boundary", encoded: math.MaxInt - 22, nameSize: 6, want: math.MaxInt},
		{name: "envelope overflow", encoded: math.MaxInt - 15, invalid: true},
		{name: "collection overflow", encoded: math.MaxInt - 22, nameSize: 7, invalid: true},
		{name: "both lengths maximal", encoded: math.MaxInt, nameSize: math.MaxInt, invalid: true},
		{name: "invalid encoded length", encoded: -1, invalid: true},
		{name: "invalid collection length", encoded: 2, nameSize: -1, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := lineCapacity(test.encoded, test.nameSize)
			if test.invalid {
				if !errors.Is(err, ErrLimit) {
					t.Fatalf("capacity=%d error=%v", got, err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("capacity=%d error=%v want=%d", got, err, test.want)
			}
		})
	}
}

func TestExportEnvelopesPreserveExactBytesAndDigest(t *testing.T) {
	t.Parallel()
	fields := struct {
		ID string `json:"id"`
	}{ID: "subject-id"}
	want := []byte("{\"record\":\"subject\",\"id\":\"subject-id\"}\n")
	wantDigest := sha256.Sum256(want)
	for _, subject := range []bool{false, true} {
		var emitted []byte
		digest := sha256.New()
		stream := &subjectStream{emit: func(line []byte) error { emitted = bytes.Clone(line); return nil }}
		var err error
		if subject {
			err = (&SubjectExporter{}).emitLine(stream, "subject", fields, digest)
		} else {
			err = emitLine(stream.emit, "subject", fields, digest)
		}
		if err != nil || !bytes.Equal(emitted, want) || !bytes.Equal(digest.Sum(nil), wantDigest[:]) {
			t.Fatalf("subject=%v bytes=%q error=%v", subject, emitted, err)
		}
		if subject && stream.bytes != int64(len(want)) {
			t.Fatalf("subject bytes=%d", stream.bytes)
		}
	}
}
