package api

import (
	"crypto/rand"

	"github.com/Mujhtech/idenqa/internal/keycustody"
	"github.com/Mujhtech/idenqa/internal/platform/id"
)

// keyCustodyGenerator adapts the process identifier generator into the HMAC
// key material factory consumed by key custody persistence.
func keyCustodyGenerator(identifiers *id.Generator) func() (string, []byte, error) {
	return func() (string, []byte, error) {
		identifier, err := identifiers.New(keycustody.KeyIDPrefix)
		if err != nil {
			return "", nil, err
		}
		material := make([]byte, keycustody.KeySize)
		if _, err := rand.Read(material); err != nil {
			clear(material)
			return "", nil, err
		}

		return identifier.String(), material, nil
	}
}
