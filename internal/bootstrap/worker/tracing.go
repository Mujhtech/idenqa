package worker

import (
	"context"
	"errors"

	"github.com/Mujhtech/idenqa/internal/delivery"
	deliverytask "github.com/Mujhtech/idenqa/internal/delivery/task"
	"github.com/Mujhtech/idenqa/internal/platform/observability"
)

type tracedSender struct {
	sender deliverytask.Sender
	tracer observability.Tracer
}

func (sender tracedSender) Send(ctx context.Context, target string, signature delivery.Signature, body []byte) (delivery.SafeDiagnostic, bool, bool, error) {
	ctx, complete := observability.StartSpan(ctx, sender.tracer, "delivery.send")
	var spanError error
	defer observability.EndSpan(complete, &spanError)
	diagnostic, succeeded, retry, err := sender.sender.Send(ctx, target, signature, body)
	spanError = err
	if spanError == nil && !succeeded {
		spanError = errors.New("delivery rejected")
	}
	return diagnostic, succeeded, retry, err
}
