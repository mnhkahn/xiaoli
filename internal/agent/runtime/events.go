package runtime

import (
	"context"

	agentevent "github.com/mnhkahn/xiaoli/internal/event"
)

type runEventSessionKeyType struct{}

var runEventSessionKey = runEventSessionKeyType{}

func withRunEventSession(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, runEventSessionKey, sessionID)
}

func runEventSessionID(ctx context.Context) string {
	sessionID, _ := ctx.Value(runEventSessionKey).(string)
	return sessionID
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
