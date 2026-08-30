package realtime

import (
	"context"
	"errors"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/id"
	core "github.com/Mujhtech/idenqa/internal/realtime"
)

func TestServerMessageQueueRejectsQueueOverflowWithoutAdvancingSequence(t *testing.T) {
	t.Parallel()

	loop, _, ticket, _ := newEstablishedLoopFixture(t, core.DefaultLimits())
	connection := newEstablishedConnectionStub()
	queue := newServerMessageQueue(connection, loop.identifiers, loop.clock, loop.limits, ticket)
	for index := 0; index < loop.limits.OutboundQueueDepth(); index++ {
		if err := queue.enqueue(t.Context(), core.CaptureProgress{CompletedSteps: 0, TotalSteps: 1},
			id.Command{}, id.Message{}, id.Message{}, nil); err != nil {
			t.Fatalf("enqueue(%d) error = %v", index, err)
		}
	}
	if err := queue.enqueue(t.Context(), core.CaptureProgress{CompletedSteps: 0, TotalSteps: 1},
		id.Command{}, id.Message{}, id.Message{}, nil); !errors.Is(err, ErrSlowConsumer) {
		t.Fatalf("enqueue(over capacity) error = %v, want ErrSlowConsumer", err)
	}
	if queue.nextSequence != uint64(core.DefaultOutboundQueueDepth+2) {
		t.Fatalf("next sequence after rejected enqueue = %d", queue.nextSequence)
	}
}

func TestServerMessageQueueReleasesAcknowledgedCommandAllowance(t *testing.T) {
	t.Parallel()

	config := core.DefaultLimitConfig()
	config.UnacknowledgedLimit = 2
	limits, err := core.NewLimits(config)
	if err != nil {
		t.Fatal(err)
	}
	loop, clientIDs, ticket, _ := newEstablishedLoopFixture(t, limits)
	connection := newEstablishedConnectionStub()
	queue := newServerMessageQueue(connection, loop.identifiers, loop.clock, limits, ticket)
	ctx, cancel := context.WithCancel(t.Context())
	result := make(chan error, 1)
	go func() {
		result <- queue.run(ctx)
	}()
	for range 2 {
		commandID, commandErr := clientIDs.NewCommand()
		if commandErr != nil {
			t.Fatal(commandErr)
		}
		if err := queue.sendAndWait(ctx, core.CommandAcknowledgement{
			Disposition: core.CommandDispositionRejected, Code: unsupportedCommandCode,
		}, commandID, id.Message{}, id.Message{}); err != nil {
			t.Fatal(err)
		}
	}
	thirdCommand, err := clientIDs.NewCommand()
	if err != nil {
		t.Fatal(err)
	}
	acknowledgement := core.CommandAcknowledgement{
		Disposition: core.CommandDispositionRejected, Code: unsupportedCommandCode,
	}
	if err := queue.enqueue(ctx, acknowledgement, thirdCommand,
		id.Message{}, id.Message{}, nil); !errors.Is(err, ErrSlowConsumer) {
		t.Fatalf("enqueue(over acknowledgement limit) error = %v", err)
	}
	if _, acknowledged := queue.acknowledge(3, 0); !acknowledged {
		t.Fatal("acknowledge(3) rejected")
	}
	if err := queue.enqueue(ctx, acknowledgement, thirdCommand,
		id.Message{}, id.Message{}, nil); err != nil {
		t.Fatalf("enqueue(after acknowledgement) error = %v", err)
	}
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatalf("run() error = %v", err)
	}
}
