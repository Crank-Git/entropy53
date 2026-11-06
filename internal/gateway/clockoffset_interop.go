//go:build interop

package gateway

import (
	"sync/atomic"
	"time"
)

// offsetClock reads the real clock plus an offset.
// The interop tests use it, so that a real round from the vectors can lie in the future.
// A normal build omits this file, so the gateway binary has no clock setting.
// The offset is atomic, because a test moves the clock while the gateway runs.
type offsetClock struct{ offset atomic.Int64 }

func (c *offsetClock) Now() time.Time {
	return time.Now().Add(time.Duration(c.offset.Load()))
}

func (c *offsetClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// SetClockOffset makes the answers of l read a clock that stands offset away from the real time.
// The first call must come before Run, because it installs the clock. Later calls are safe while Run serves.
func (l *Listeners) SetClockOffset(offset time.Duration) {
	c, ok := l.clock.(*offsetClock)
	if !ok {
		c = new(offsetClock)
		l.clock = c
		l.h.clock = c
	}
	c.offset.Store(int64(offset))
}

// SetClockOffset makes u read a clock that stands offset away from the real time.
// The first call must come before the first call to Round. Later calls are safe while u serves.
func (u *Upstreams) SetClockOffset(offset time.Duration) {
	c, ok := u.clock.(*offsetClock)
	if !ok {
		c = new(offsetClock)
		u.clock = c
	}
	c.offset.Store(int64(offset))
}
