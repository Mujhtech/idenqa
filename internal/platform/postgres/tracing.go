package postgres

import (
	"context"
	"sync"

	"github.com/Mujhtech/idenqa/internal/platform/observability"
	"github.com/jackc/pgx/v5"
)

// queryTracer is installed when the pool is created so existing connections
// share the same process-owned tracing configuration.
type queryTracer struct {
	mutex  sync.RWMutex
	tracer observability.Tracer
}

type querySpanKey struct{}

// WithTracer configures query tracing, including already-open pool connections.
func (pool *Pool) WithTracer(tracer observability.Tracer) *Pool {
	if pool != nil && pool.tracing != nil {
		pool.tracing.mutex.Lock()
		pool.tracing.tracer = tracer
		pool.tracing.mutex.Unlock()
	}
	return pool
}

func (tracer *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	tracer.mutex.RLock()
	operationTracer := tracer.tracer
	tracer.mutex.RUnlock()
	ctx, complete := observability.StartSpan(ctx, operationTracer, "postgres.query")
	return context.WithValue(ctx, querySpanKey{}, complete)
}

func (*queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	if complete, ok := ctx.Value(querySpanKey{}).(func(error)); ok {
		complete(data.Err)
	}
}

var _ pgx.QueryTracer = (*queryTracer)(nil)
