package main

// golang time functions can be computationally expensive and overly precise
// for this application a time resolution of 1 millisecond will suffice and
// is potentially being checked thousands of times per second.  Instead use
// a singleton object which maintains an approximation of the current time
// in ms to reduce time-related CPU load while still allowing workers to
// check their runtime.

import (
	"time"
)

type Clock struct {
	millis int64
	ticker *time.Ticker
}

func NewClock() *Clock {
	c := new(Clock)
	c.millis = time.Now().UnixNano() / int64(time.Millisecond)
	c.ticker = time.NewTicker(1 * time.Millisecond)

	// update the time in milliseconds every 1 millisecond
	go func() {
		for {
			select {
			case <-c.ticker.C:
				c.millis++
			}
		}
	}()

	return c
}

func (c *Clock) GetMillis() int64 {
	return c.millis
}

func (c *Clock) Since(t int64) int64 {
	return c.millis - t
}
