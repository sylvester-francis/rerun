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

//go:build !windows

// Package crashtest proves the durability claim with real SIGKILLs: for every
// step boundary, kill the process there, recover, and require every side effect
// to have happened exactly once. This is the test the whole library exists to
// pass.
//
// The killer fires from OnStep, which runs after the journal append commits, so
// the effect-then-journal window is closed at each boundary and exactly-once is
// the required outcome. (The at-least-once window between an effect and its
// append is real but cannot be deterministically targeted with SIGKILL from
// process-internal hooks; it is documented, not asserted.)
package crashtest

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func buildChild(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "child")
	cmd := exec.Command("go", "build", "-o", bin, "./child")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build child: %v\n%s", err, out)
	}
	return bin
}

func runChild(t *testing.T, bin, mode, db, effects string, killAfter int) error {
	t.Helper()
	cmd := exec.Command(bin)
	cmd.Env = append(os.Environ(),
		"RERUN_DB="+db,
		"RERUN_EFFECTS="+effects,
		"RERUN_MODE="+mode,
		fmt.Sprintf("RERUN_KILL_AFTER=%d", killAfter),
	)
	return cmd.Run()
}

func TestCrash_EveryBoundary_ExactlyOnceEffects(t *testing.T) {
	bin := buildChild(t)
	steps := []string{"alpha", "beta", "gamma", "delta"}
	// The killer fires when the OnStep count exceeds killAfter, i.e. after that
	// many journal appends have committed. killAfter 1..len(steps) kill inside the
	// run; len(steps)+1 fires past the last append (the result), so the start run
	// completes cleanly. Every case must recover to exactly-once effects.
	for killAfter := 1; killAfter <= len(steps)+1; killAfter++ {
		t.Run(fmt.Sprintf("kill-after-%d-steps", killAfter), func(t *testing.T) {
			dir := t.TempDir()
			db := filepath.Join(dir, "crash.db")
			effects := filepath.Join(dir, "effects.log")

			err := runChild(t, bin, "start", db, effects, killAfter)
			if killAfter <= len(steps) && err == nil {
				t.Fatal("child was supposed to be killed but exited cleanly")
			}
			for range 3 {
				if err = runChild(t, bin, "recover", db, effects, -1); err == nil {
					break
				}
			}
			if err != nil {
				t.Fatalf("run never completed after recovery attempts: %v", err)
			}

			counts := map[string]int{}
			f, err := os.Open(effects)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			sc := bufio.NewScanner(f)
			for sc.Scan() {
				counts[sc.Text()]++
			}
			for _, tag := range steps {
				if counts[tag] != 1 {
					t.Fatalf("effect %q happened %d times, want exactly 1 (counts: %v)", tag, counts[tag], counts)
				}
			}
		})
	}
}
