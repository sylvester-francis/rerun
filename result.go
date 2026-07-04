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
)

const (
	resultTag = reservedPrefix + "result" // a positional step carrying the run's result
	errorTag  = reservedPrefix + "error"  // metadata at errSeq carrying the terminal error
	errSeq    = -2
)

// Return records the run's result. Call it once, at a deterministic point
// (typically the workflow's last act). It is journaled like any step — a
// doStep under the hood (Do itself would reject the reserved result tag) — so
// replay reproduces it without re-running.
func Return[T any](w *W, v T) {
	doStep(w, resultTag, func(context.Context) (T, error) { return v, nil })
}

// Result reads the value a run recorded with Return, decoded as T. If the run
// failed, it returns the terminal error the workflow returned; if the run has
// recorded no result yet (still running, or never called Return), it returns an
// error saying so. It reads through the store, so it works from any process.
func Result[T any](ctx context.Context, e *Engine, runID string) (T, error) {
	var v T
	logs, err := e.store.LoadLogs(ctx, runID)
	if err != nil {
		return v, fmt.Errorf("rerun: result for run %s: %w", runID, err)
	}
	for _, l := range logs {
		if l.Tag == errorTag {
			return v, fmt.Errorf("rerun: run %s failed: %s", runID, l.Err)
		}
	}
	for _, l := range logs {
		if l.Tag == resultTag {
			if uerr := e.codec.Unmarshal(l.Payload, &v); uerr != nil {
				return v, fmt.Errorf("rerun: decode result for run %s: %w", runID, uerr)
			}
			return v, nil
		}
	}
	return v, fmt.Errorf("rerun: run %s has no recorded result", runID)
}
