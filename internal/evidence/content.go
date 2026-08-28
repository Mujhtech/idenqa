package evidence

import (
	"errors"
	"mime"
	"strings"

	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/objectstore"
)

// ContentRecord is the durable ciphertext reference and envelope metadata for
// one evidence artefact. It never contains raw evidence or plaintext keys.
type ContentRecord struct {
	Object          objectstore.ObjectRecord
	Envelope        platformcrypto.EnvelopeRecord
	PlaintextDigest string
	MediaType       string
}

// Content is validated immutable protected-content metadata.
type Content struct {
	record          ContentRecord
	object          objectstore.Object
	envelope        platformcrypto.Envelope
	plaintextDigest platformcrypto.Digest
}

// NewContent validates protected-content metadata against its authenticated
// encryption context.
func NewContent(record ContentRecord, context platformcrypto.Context) (Content, error) {
	object, err := objectstore.NewObject(record.Object)
	if err != nil {
		return Content{}, err
	}
	envelope, err := platformcrypto.NewEnvelope(record.Envelope)
	if err != nil {
		return Content{}, err
	}
	envelopeRecord := envelope.Record()
	if envelopeRecord.ContextSchemaVersion != context.SchemaVersion() ||
		envelopeRecord.ContextDigest != string(context.Digest()) {
		return Content{}, errors.New("evidence: ciphertext envelope context does not match evidence metadata")
	}
	plaintextDigest, err := platformcrypto.NewDigest(record.PlaintextDigest)
	if err != nil {
		return Content{}, err
	}
	mediaType, _, err := mime.ParseMediaType(record.MediaType)
	if err != nil || mediaType == "" || len(record.MediaType) > 200 ||
		strings.ContainsAny(record.MediaType, "\r\n") {
		return Content{}, errors.New("evidence: media type is invalid")
	}
	record.Object = object.Record()
	record.Envelope = envelope.Record()
	record.PlaintextDigest = string(plaintextDigest)
	record.MediaType = strings.ToLower(mediaType)

	return Content{
		record:          record,
		object:          object,
		envelope:        envelope,
		plaintextDigest: plaintextDigest,
	}, nil
}

// Record returns a defensive durable copy.
func (content Content) Record() ContentRecord {
	record := content.record
	record.Object = content.object.Record()
	record.Envelope = content.envelope.Record()
	return record
}

// Object returns the exact ciphertext-object reference.
func (content Content) Object() objectstore.Object { return content.object }

// Envelope returns the authenticated ciphertext envelope.
func (content Content) Envelope() platformcrypto.Envelope { return content.envelope }

// PlaintextDigest returns restricted integrity metadata. Callers must not log it.
func (content Content) PlaintextDigest() platformcrypto.Digest { return content.plaintextDigest }

// IsZero reports whether protected content has not been initialised.
func (content Content) IsZero() bool { return content.object.IsZero() || content.envelope.IsZero() }
