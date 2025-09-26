package caldav

import (
	"context"
	"time"
)

type CleanupService struct {
	handlers *Handlers
	interval time.Duration
}

func NewCleanupService(handlers *Handlers, interval time.Duration) *CleanupService {
	return &CleanupService{
		handlers: handlers,
		interval: interval,
	}
}

func (c *CleanupService) Start(ctx context.Context) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.runCleanup(ctx)
		}
	}
}

func (c *CleanupService) runCleanup(ctx context.Context) {
	c.handlers.logger.Debug().Msg("running scheduled cleanup")

	// Clean up old processed scheduling objects (older than 7 days)
	cutoff := time.Now().AddDate(0, 0, -7)
	if err := c.cleanupOldSchedulingObjects(ctx, cutoff); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to cleanup old scheduling objects")
	}

	// Clean up old attendee responses (older than 30 days for completed events)
	if err := c.cleanupOldAttendeeResponses(ctx, cutoff); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to cleanup old attendee responses")
	}

	// Clean up old free/busy info (older than 30 days)
	if err := c.cleanupOldFreeBusyInfo(ctx, cutoff); err != nil {
		c.handlers.logger.Error().Err(err).Msg("failed to cleanup old free/busy info")
	}
}

func (c *CleanupService) cleanupOldSchedulingObjects(ctx context.Context, cutoff time.Time) error {
	return c.handlers.store.DeleteOldSchedulingObjects(ctx, cutoff)
}

func (c *CleanupService) cleanupOldAttendeeResponses(ctx context.Context, cutoff time.Time) error {
	return c.handlers.store.DeleteOldAttendeeResponses(ctx, cutoff)
}

func (c *CleanupService) cleanupOldFreeBusyInfo(ctx context.Context, cutoff time.Time) error {
	return c.handlers.store.DeleteOldFreeBusyInfo(ctx, cutoff)
}
