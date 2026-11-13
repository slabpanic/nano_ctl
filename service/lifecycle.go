package service

import (
	"context"
	"errors"
	"fmt"
)

type hardwareLifecycle interface {
	Open(context.Context) error
	Close(context.Context) error
	CloseExecutor()
}

// The server process owns the SDK session. All startup and shutdown paths,
// including listener failures, release it before stopping the SDK executor.
func WithHardware(ctx context.Context, hw hardwareLifecycle, serve func(context.Context) error) (err error) {
	defer hw.CloseExecutor()
	defer func() {
		if closeErr := hw.Close(context.Background()); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close hardware session: %w", closeErr))
		}
	}()
	if err := hw.Open(ctx); err != nil {
		return fmt.Errorf("open hardware session on startup: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil
	}
	return serve(ctx)
}
