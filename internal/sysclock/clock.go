// Package sysclock is the system adapter of core.Clock.
package sysclock

import "time"

// Clock returns the real time, in UTC.
type Clock struct{}

// Now implements core.Clock.
func (Clock) Now() time.Time { return time.Now().UTC() }
