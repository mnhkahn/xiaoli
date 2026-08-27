package runtime

import (
	"context"

	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

type runEventSessionKeyType struct{}

var runEventSessionKey = runEventSessionKeyType{}

type modelUsageReporterKeyType struct{}

type modelUsageReporter struct {
	publisher agentevent.Publisher
	sessionID string
}

var modelUsageReporterKey = modelUsageReporterKeyType{}

func withRunEventSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, runEventSessionKey, sessionID)
}

func runEventSessionID(ctx context.Context) string {
	sessionID, _ := ctx.Value(runEventSessionKey).(string)
	return sessionID
}

func withModelUsageReporter(ctx context.Context, publisher agentevent.Publisher, sessionID string) context.Context {
	return context.WithValue(ctx, modelUsageReporterKey, modelUsageReporter{publisher: publisher, sessionID: sessionID})
}

func publishModelUsageEvent(ctx context.Context, eventType string, data map[string]any) {
	reporter, _ := ctx.Value(modelUsageReporterKey).(modelUsageReporter)
	if reporter.publisher == nil {
		return
	}
	_ = publishRunEvent(ctx, reporter.publisher, eventType, reporter.sessionID, data)
}

func publishRunEvent(ctx context.Context, publisher agentevent.Publisher, eventType, sessionID string, data any) error {
	if publisher == nil {
		return nil
	}
	return publisher.Publish(ctx, agentevent.Event{
		Type:      eventType,
		SessionID: sessionID,
		Data:      data,
	})
}
