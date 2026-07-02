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

// Version pins a run to a code path so in-flight runs survive a deploy that
// changes the workflow. On the first run it journals max, the newest version
// this build knows; on replay it returns the journaled value, so a run started
// before the change keeps taking its original branch while new runs take the
// new one. Branch on the returned int, and give every change its own changeID.
//
// It panics if the journaled version falls outside [min, max]: that means a
// rollback to code too old to honor a version some run already committed to,
// and failing loud beats silently taking the wrong branch.
func Version(w *W, changeID string, min, max int) int {
	v, _ := Do(w, "version:"+changeID, func(context.Context) (int, error) {
		return max, nil
	})
	if v < min || v > max {
		panic(fmt.Sprintf(
			"rerun: run %s pinned to version %d for %q, unsupported by this build [%d,%d]",
			w.RunID, v, changeID, min, max,
		))
	}
	return v
}
