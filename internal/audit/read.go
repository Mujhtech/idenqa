package audit

import (
	"github.com/Mujhtech/idenqa/internal/platform/observability"

	"context"
	"errors"
	"fmt"

	"github.com/Mujhtech/idenqa/internal/access"
	"github.com/Mujhtech/idenqa/internal/tenant"
)

// RecordRepository reads bounded pages from one tenant's immutable audit chain.
type RecordRepository interface {
	List(context.Context, tenant.Scope, uint64, int) ([]Record, error)
}

// Reader is the authorised metadata-only audit inspection boundary.
type Reader struct {
	tracer     observability.Tracer
	repository RecordRepository
}

// NewReader constructs the audit inspection boundary.
func NewReader(repository RecordRepository) (*Reader, error) {
	if repository == nil {
		return nil, ErrInvalid
	}
	return &Reader{repository: repository}, nil
}

// List returns a newest-first page. before is an exclusive sequence cursor;
// zero starts at the current tenant chain head.
func (reader *Reader) List(ctx context.Context, authority access.Context, before uint64, limit int) (spanResult0 []Record, spanErr error) {
	ctx, completeSpan := observability.StartSpan(ctx, reader.operationTracer(), "audit.Reader.List")
	defer observability.EndSpan(completeSpan, &spanErr)

	if reader == nil || reader.repository == nil || authority.TenantScope().ID().IsZero() || limit < 1 || limit > 100 {
		return nil, ErrInvalid
	}
	if err := authority.Require(access.PermissionAuditExport); err != nil {
		return nil, err
	}
	records, err := reader.repository.List(ctx, authority.TenantScope(), before, limit)
	if err != nil {
		return nil, fmt.Errorf("list audit records: %w", err)
	}
	for index, record := range records {
		if err := validateRecordFields(record); err != nil {
			return nil, errors.Join(ErrChain, fmt.Errorf("record %d: %w", index, err))
		}
		if index > 0 && records[index-1].Sequence <= record.Sequence {
			return nil, ErrChain
		}
	}
	return records, nil
}

// WithTracer injects operation tracing during composition, before concurrent use.
func (reader *Reader) WithTracer(tracer observability.Tracer) *Reader {
	if reader != nil {
		reader.tracer = tracer
	}
	return reader
}

func (reader *Reader) operationTracer() observability.Tracer {
	if reader == nil {
		return nil
	}
	return reader.tracer
}
