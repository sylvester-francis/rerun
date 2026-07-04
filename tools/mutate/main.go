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

// Command mutate is a dependency-free mutation tester for rerun's core.
//
// It introduces one known fault at a time into the source, runs the unit suite,
// and reports whether each mutant was killed (a test failed) or survived (the
// suite stayed green). Known-equivalent mutants are expected to survive; every
// other mutant is expected to be killed. The command exits non-zero if any
// outcome does not match expectation, so it gates CI exactly as the unit suite
// does. Each mutation is applied to a fresh copy of the source and restored
// immediately afterward.
//
// Run from the module root:  go run ./tools/mutate   (or: make mutate)
package main

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type kind int

const (
	mustKill kind = iota
	equivalent
)

type mutant struct {
	name     string
	file     string
	old, new string
	expect   kind
	reason   string // catching test, or why it is equivalent
}

// Each mutant is a real fault from rerun's mutant table, chosen to fail fast.
var mutants = []mutant{
	{
		name: "guard && -> ||", file: "workflow.go",
		old:    "if w.replay && w.seq < len(w.logs)",
		new:    "if w.replay || w.seq < len(w.logs)",
		expect: mustKill, reason: "TestPartialReplay_ResumesMidway",
	},
	{
		name: "guard off-by-one < -> <=", file: "workflow.go",
		old:    "w.seq < len(w.logs)",
		new:    "w.seq <= len(w.logs)",
		expect: mustKill, reason: "TestPartialReplay_ResumesMidway",
	},
	{
		name: "determinism check != -> ==", file: "workflow.go",
		old:    "if l.Tag != tag {",
		new:    "if l.Tag == tag {",
		expect: mustKill, reason: "TestReplayStep_DeterminismPanic",
	},
	{
		name: "drop journaled step error", file: "workflow.go",
		old:    "\t\terrStr = err.Error()\n",
		new:    "",
		expect: mustKill, reason: "TestDo_ReplayPreservesError",
	},
	{
		name: "success marked failed", file: "run.go",
		old:    "e.store.Finish(pctx, r.ID, Done)",
		new:    "e.store.Finish(pctx, r.ID, Failed)",
		expect: mustKill, reason: "TestWorkflow_SuccessSetsDone",
	},
	{
		name: "remove w.replay = false", file: "workflow.go",
		old:    "\tw.replay = false\n\tv, err := fn(w.ctx)",
		new:    "\tv, err := fn(w.ctx)",
		expect: equivalent, reason: "redundant: seq < len(logs) already gates replay",
	},
}

// suitePasses runs the core unit suite and reports whether it stayed green.
func suitePasses() bool {
	cmd := exec.Command("go", "test", "-count=1", "-timeout", "60s", ".")
	cmd.Stdout, cmd.Stderr = nil, nil
	return cmd.Run() == nil
}

func main() {
	if !suitePasses() {
		fmt.Println("baseline suite is red; fix the suite before mutation testing")
		os.Exit(2)
	}

	fmt.Printf("%-28s %-9s %-9s %-9s %s\n", "MUTANT", "OUTCOME", "EXPECTED", "STATUS", "CATCHER / REASON")
	killed, mismatches := 0, 0

	for _, m := range mutants {
		src, err := os.ReadFile(m.file)
		if err != nil {
			fmt.Printf("cannot read %s: %v\n", m.file, err)
			os.Exit(2)
		}
		orig := string(src)
		if strings.Count(orig, m.old) != 1 {
			fmt.Printf("mutant %q: anchor not unique in %s (found %d)\n",
				m.name, m.file, strings.Count(orig, m.old))
			os.Exit(2)
		}

		if err := os.WriteFile(m.file, []byte(strings.Replace(orig, m.old, m.new, 1)), 0644); err != nil {
			fmt.Printf("cannot write %s: %v\n", m.file, err)
			os.Exit(2)
		}
		survived := suitePasses()
		_ = os.WriteFile(m.file, []byte(orig), 0644) // restore immediately

		if !survived {
			killed++
		}
		expectSurvive := m.expect == equivalent
		status := "ok"
		if survived != expectSurvive {
			status, mismatches = "MISMATCH", mismatches+1
		}
		outcome, expected := "killed", "killed"
		if survived {
			outcome = "survived"
		}
		if expectSurvive {
			expected = "survived"
		}
		fmt.Printf("%-28s %-9s %-9s %-9s %s\n", m.name, outcome, expected, status, m.reason)
	}

	equiv := 0
	for _, m := range mutants {
		if m.expect == equivalent {
			equiv++
		}
	}
	fmt.Printf("\n%d mutants: %d killed, %d survived (%d equivalent expected)\n",
		len(mutants), killed, len(mutants)-killed, equiv)
	if mismatches > 0 {
		fmt.Printf("FAIL: %d mutant(s) did not match expectation\n", mismatches)
		os.Exit(1)
	}
	fmt.Println("PASS: every mutant matched expectation")
}
