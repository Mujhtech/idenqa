package delivery

import (
	"context"
	"encoding/json"
	"time"

	webhookv1 "github.com/Mujhtech/idenqa/contracts/webhook/v1"
	"github.com/Mujhtech/idenqa/internal/access"
	platformcrypto "github.com/Mujhtech/idenqa/internal/platform/crypto"
	"github.com/Mujhtech/idenqa/internal/platform/id"
	"github.com/Mujhtech/idenqa/internal/platform/kms"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// MaximumStreamBatch bounds one durable event read or one live stream page.
const MaximumStreamBatch = 256

// StreamCommitLag bounds how far a live cursor may advance behind the highest
// observed sequence, so rows committed by overlapping transactions are still
// reread. Receivers must deduplicate by event ID because rereads can repeat.
const StreamCommitLag = 64

// StreamPosition is one durable insertion sequence; zero means the beginning.
type StreamPosition int64

// WrappedEvent is one stored catalogue event before controlled decryption.
type WrappedEvent struct {
	ID            id.Event
	Type          webhookv1.Type
	SchemaVersion string
	Sequence      StreamPosition
	CreatedAt     time.Time
	Body          []byte
	Wrapping      *kms.WrappedKey
}

// StreamEvent is one canonical envelope with its decrypted payload for an
// authorised tenant reader.
type StreamEvent struct {
	ID            string          `json:"id"`
	Type          string          `json:"type"`
	SchemaVersion string          `json:"schema_version"`
	Region        string          `json:"region"`
	CreatedAt     time.Time       `json:"created_at"`
	Sequence      StreamPosition  `json:"-"`
	Body          json.RawMessage `json:"body"`
}

// StreamRepository supplies tenant-scoped wrapped catalogue events in durable
// insertion order. It never decrypts a body.
type StreamRepository interface {
	Events(context.Context, tenant.Scope, StreamPosition, []string, int) ([]WrappedEvent, error)
	LatestPosition(context.Context, tenant.Scope) (StreamPosition, error)
}

// Stream serves the read-only tenant event feed. Bodies are unwrapped only at
// this authorised boundary under the delivery body purpose.
type Stream struct {
	repository StreamRepository
	unwrapper  platformcrypto.KeyUnwrapper
}

// NewStream constructs the tenant event feed. A nil unwrapper fails closed on
// reads when this composition cannot protect delivery bodies.
func NewStream(repository StreamRepository, unwrapper platformcrypto.KeyUnwrapper) (*Stream, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &Stream{repository: repository, unwrapper: unwrapper}, nil
}

// Events returns one bounded page of decrypted canonical events after the
// position, optionally selected by validated catalogue subscriptions.
func (service *Stream) Events(ctx context.Context, actor access.Context, after StreamPosition, eventTypes []string, limit int) ([]StreamEvent, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return nil, err
	}
	if after < 0 || limit < 1 || limit > MaximumStreamBatch {
		return nil, ErrInvalid
	}
	selection, err := streamSelection(eventTypes)
	if err != nil {
		return nil, err
	}
	if service.unwrapper == nil {
		return nil, ErrSigningUnavailable
	}
	scope := actor.TenantScope()
	rows, err := service.repository.Events(ctx, scope, after, selection, limit)
	if err != nil {
		return nil, err
	}
	result := make([]StreamEvent, 0, len(rows))
	for _, row := range rows {
		if row.Wrapping == nil {
			return nil, ErrInvalid
		}
		plaintext, err := service.unwrapper.Unwrap(ctx, BodyPurpose(), *row.Wrapping, BodyContext(scope.ID().String(), row.ID.String()))
		if err != nil {
			return nil, ErrSigningUnavailable
		}
		event, parseErr := webhookv1.Parse(plaintext)
		clear(plaintext)
		if parseErr != nil {
			return nil, ErrInvalid
		}
		canonical, err := event.Canonical()
		if err != nil {
			return nil, ErrInvalid
		}
		result = append(result, StreamEvent{
			ID:            event.ID,
			Type:          string(event.Type),
			SchemaVersion: event.SchemaVersion,
			Region:        event.Region,
			CreatedAt:     event.CreatedAt.UTC(),
			Sequence:      row.Sequence,
			Body:          canonical,
		})
	}
	return result, nil
}

// LatestPosition returns the current tail so a fresh live stream does not
// replay committed history.
func (service *Stream) LatestPosition(ctx context.Context, actor access.Context) (StreamPosition, error) {
	if err := actor.Require(access.PermissionWebhooksRead); err != nil {
		return 0, err
	}
	return service.repository.LatestPosition(ctx, actor.TenantScope())
}

func streamSelection(eventTypes []string) ([]string, error) {
	if len(eventTypes) == 0 {
		return nil, nil
	}
	selection, err := webhookv1.ValidateSubscriptions(eventTypes)
	if err != nil {
		return nil, ErrInvalid
	}
	if len(selection) == 1 && webhookv1.IsWildcard(selection[0]) {
		return nil, nil
	}
	return selection, nil
}
