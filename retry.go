// Copyright 2026 Sylvester Francis
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package rerun

import (
	"context"
	"fmt"
	"time"
)

// RetryPolicy configures Retry: how many attempts to make and how long to wait
// before each retry (Backoff is called with the zero-based attempt that just
// failed). A nil Backoff means retry immediately.
type RetryPolicy struct {
	MaxAttempts int
	Backoff     func(attempt int) time.Duration
}

// FixedBackoff waits the same duration before every retry.
func FixedBackoff(d time.Duration) func(int) time.Duration {
	return func(int) time.Duration { return d }
}

// ExpBackoff doubles the wait each attempt, starting at base and capped at max.
func ExpBackoff(base, max time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		d := base
		for i := 0; i < attempt && d < max; i++ {
			d *= 2
		}
		if d <= 0 || d > max {
			return max
		}
		return d
	}
}

// Retry runs fn until it succeeds or MaxAttempts is reached. Each attempt is its
// own journaled step (tag:attempt-N), and the wait between attempts uses the
// durable Sleep — so the backoff itself survives a crash, and on replay the
// whole attempt-and-wait sequence is reproduced from the journal without calling
// fn again. It returns the first success, or the last error wrapped after the
// final attempt.
func Retry[T any](w *W, tag string, p RetryPolicy, fn func(context.Context) (T, error)) (T, error) {
	if p.MaxAttempts < 1 {
		p.MaxAttempts = 1
	}
	var v T
	var err error
	for attempt := 0; attempt < p.MaxAttempts; attempt++ {
		v, err = Do(w, fmt.Sprintf("%s:attempt-%d", tag, attempt), fn)
		if err == nil {
			return v, nil
		}
		if attempt < p.MaxAttempts-1 && p.Backoff != nil {
			if d := p.Backoff(attempt); d > 0 {
				if serr := Sleep(w, d); serr != nil {
					return v, serr
				}
			}
		}
	}
	return v, fmt.Errorf("rerun: %q failed after %d attempts: %w", tag, p.MaxAttempts, err)
}
