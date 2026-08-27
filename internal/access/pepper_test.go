package access_test

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"testing"

	"github.com/Mujhtech/idenqa/internal/access"
)

func TestPepperSetDigestAndVerification(t *testing.T) {
	t.Parallel()

	presented := testPresentedKey(t, 0xff)
	pepper := bytes.Repeat([]byte{0x11}, 32)
	set, err := access.NewPepperSet(2, map[access.PepperVersion][]byte{
		1: bytes.Repeat([]byte{0x10}, 32),
		2: pepper,
	})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	digest, version, err := set.Digest(presented)
	if err != nil {
		t.Fatalf("Digest() error = %v", err)
	}
	if version != 2 || set.ActiveVersion() != 2 {
		t.Fatalf("pepper version = %d, active = %d", version, set.ActiveVersion())
	}
	valid, err := set.Verify(presented, version, digest)
	if err != nil || !valid {
		t.Fatalf("Verify() = %t, %v", valid, err)
	}

	mac := hmac.New(sha256.New, pepper)
	message := []byte("idq-api-key\x00v1\x00" + presented.TenantHint().String() + "\x00" + presented.ID().String() + "\x00")
	message = append(message, bytes.Repeat([]byte{0xff}, 32)...)
	if _, err := mac.Write(message); err != nil {
		t.Fatalf("reference HMAC Write() error = %v", err)
	}
	if !hmac.Equal(digest.Bytes(), mac.Sum(nil)) {
		t.Fatal("Digest() does not implement the selected domain-separated HMAC contract")
	}

	wrong := testPresentedKey(t, 0xfe)
	valid, err = set.Verify(wrong, version, digest)
	if err != nil || valid {
		t.Fatalf("wrong-key Verify() = %t, %v", valid, err)
	}
	if _, err := set.Verify(presented, 3, digest); err == nil {
		t.Error("Verify() with unknown pepper version error = nil")
	}
}

func TestPepperSetSupportsRotationAndDefensiveCopies(t *testing.T) {
	t.Parallel()

	presented := testPresentedKey(t, 0xaa)
	versionOne := bytes.Repeat([]byte{1}, 32)
	oldSet, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{1: versionOne})
	if err != nil {
		t.Fatalf("old NewPepperSet() error = %v", err)
	}
	oldDigest, oldVersion, err := oldSet.Digest(presented)
	if err != nil {
		t.Fatalf("old Digest() error = %v", err)
	}

	inputVersionOne := bytes.Repeat([]byte{1}, 32)
	rotated, err := access.NewPepperSet(2, map[access.PepperVersion][]byte{
		1: inputVersionOne,
		2: bytes.Repeat([]byte{2}, 32),
	})
	if err != nil {
		t.Fatalf("rotated NewPepperSet() error = %v", err)
	}
	inputVersionOne[0] = 99
	valid, err := rotated.Verify(presented, oldVersion, oldDigest)
	if err != nil || !valid {
		t.Fatalf("rotated Verify(old) = %t, %v", valid, err)
	}
}

func TestPepperSetDummyVerification(t *testing.T) {
	t.Parallel()

	set, err := access.NewPepperSet(1, map[access.PepperVersion][]byte{
		1: bytes.Repeat([]byte{1}, 32),
	})
	if err != nil {
		t.Fatalf("NewPepperSet() error = %v", err)
	}
	if err := set.DummyVerify(access.PresentedKey{}); err != nil {
		t.Fatalf("DummyVerify(malformed) error = %v", err)
	}
	if err := set.DummyVerify(testPresentedKey(t, 0xdd)); err != nil {
		t.Fatalf("DummyVerify(unknown) error = %v", err)
	}

	var nilSet *access.PepperSet
	if err := nilSet.DummyVerify(access.PresentedKey{}); err == nil {
		t.Error("nil DummyVerify() error = nil")
	}
}

func TestDigestDefensiveCopyAndRedaction(t *testing.T) {
	t.Parallel()

	input := bytes.Repeat([]byte{7}, sha256.Size)
	digest, err := access.ParseDigest(input)
	if err != nil {
		t.Fatalf("ParseDigest() error = %v", err)
	}
	input[0] = 8
	first := digest.Bytes()
	first[1] = 9
	second := digest.Bytes()
	if second[0] != 7 || second[1] != 7 {
		t.Fatal("digest changed through caller-owned bytes")
	}
	if got := fmt.Sprintf("%s|%#v", digest, digest); got != "[REDACTED]|[REDACTED]" {
		t.Fatalf("formatted digest = %q", got)
	}
	if _, err := access.ParseDigest([]byte{1}); err == nil {
		t.Error("ParseDigest(short) error = nil")
	}
}

func TestPepperSetRejectsInvalidConfiguration(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		active  access.PepperVersion
		peppers map[access.PepperVersion][]byte
	}{
		{name: "zero active", peppers: map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{1}, 32)}},
		{name: "empty", active: 1},
		{name: "zero version", active: 1, peppers: map[access.PepperVersion][]byte{0: bytes.Repeat([]byte{1}, 32), 1: bytes.Repeat([]byte{1}, 32)}},
		{name: "short material", active: 1, peppers: map[access.PepperVersion][]byte{1: {1}}},
		{name: "missing active", active: 2, peppers: map[access.PepperVersion][]byte{1: bytes.Repeat([]byte{1}, 32)}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if _, err := access.NewPepperSet(test.active, test.peppers); err == nil {
				t.Error("NewPepperSet() error = nil")
			}
		})
	}

	var nilSet *access.PepperSet
	if nilSet.ActiveVersion() != 0 {
		t.Error("nil ActiveVersion() is non-zero")
	}
	if _, _, err := nilSet.Digest(access.PresentedKey{}); err == nil {
		t.Error("nil Digest() error = nil")
	}
}
