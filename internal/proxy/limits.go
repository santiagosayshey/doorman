package proxy

import (
	"time"

	"golang.org/x/time/rate"

	"github.com/santiagosayshey/doorman/internal/config"
)

// newLimiter turns a config limit into a token bucket: Count tokens that
// refill evenly over Per. "100/h" allows a burst of 100, then one more every
// 36 seconds. It returns nil for "none" and for an unset key limit.
func newLimiter(l config.Limit) *rate.Limiter {
	if l.Count <= 0 { // Unlimited, or not written
		return nil
	}
	every := l.Per / time.Duration(l.Count)
	return rate.NewLimiter(rate.Every(every), l.Count)
}

// allow takes one token from each limiter that exists, or from none of them.
// A request refused by the rule's limit must not use up the key's budget,
// and the other way round, so both are reserved first and both are given
// back if either has to wait. When refused, it returns how long to wait.
func allow(now time.Time, limiters ...*rate.Limiter) (bool, time.Duration) {
	var reservations []*rate.Reservation
	var wait time.Duration
	for _, l := range limiters {
		if l == nil {
			continue
		}
		r := l.ReserveN(now, 1)
		reservations = append(reservations, r)
		if d := r.DelayFrom(now); d > wait {
			wait = d
		}
	}
	if wait > 0 {
		for _, r := range reservations {
			r.CancelAt(now)
		}
		return false, wait
	}
	return true, 0
}
