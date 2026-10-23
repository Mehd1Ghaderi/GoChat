package actors

import (
	"fmt"
	"reflect"

	"github.com/anthdm/hollywood/actor"
	"go.uber.org/zap"

	ierrors "github.com/ssenerg/gochat/internal/errors"
)

// Recover keeps a stateless actor alive through a panic, answering the
// request that caused it with an internal error.
func Recover(logger *zap.Logger) actor.MiddlewareFunc {
	return func(next actor.ReceiveFunc) actor.ReceiveFunc {
		return func(c *actor.Context) {
			defer func() {
				if v := recover(); v != nil {
					logger.Error("actor panicked",
						zap.String("pid", c.PID().String()),
						zap.String("message", reflect.TypeOf(c.Message()).String()),
						zap.Any("panic", v), zap.Stack("stack"))
					Reply(c, ierrors.Internal(fmt.Errorf("panic: %v", v)))
				}
			}()
			next(c)
		}
	}
}

// LogEvents logs the engine events that signal trouble.
func LogEvents(e *actor.Engine, logger *zap.Logger) *actor.PID {
	pid := e.SpawnFunc(func(c *actor.Context) {
		switch ev := c.Message().(type) {
		case actor.ActorRestartedEvent:
			logger.Error("actor restarted", zap.String("pid", ev.PID.String()), zap.Any("reason", ev.Reason),
				zap.Int32("restarts", ev.Restarts), zap.ByteString("stack", ev.Stacktrace))
		case actor.ActorMaxRestartsExceededEvent:
			logger.Error("actor exceeded its restarts and stopped", zap.String("pid", ev.PID.String()))
		case actor.ActorDuplicateIdEvent:
			logger.Error("actor id already in use", zap.String("pid", ev.PID.String()))
		case actor.DeadLetterEvent:
			logger.Debug("message to a stopped actor", zap.String("target", ev.Target.String()),
				zap.String("message", reflect.TypeOf(ev.Message).String()))
		}
	}, "events")
	e.Subscribe(pid)
	return pid
}
