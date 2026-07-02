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

// Command skeleton is the simplest rerun example: a two-step workflow runs
// forward and prints the journal each step leaves behind.
package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func main() {
	ctx := context.Background()
	st := internal.NewMemStore()
	e := rerun.New(st)

	e.Handle("greeter", func(w *rerun.W) error {
		name, _ := rerun.Do(w, "make-name", func(context.Context) (string, error) {
			fmt.Println("  [live] make-name")
			return "Ada", nil
		})
		_, err := rerun.Do(w, "greeting", func(context.Context) (string, error) {
			g := "hello " + name
			fmt.Println("  [live] greeting: " + g)
			return g, nil
		})
		return err
	})

	fmt.Println("running workflow r1:")
	if err := e.Start(ctx, "greeter", "r1"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	r := waitDone(st, "r1")
	fmt.Println("status:", r.Status)

	logs, _ := st.LoadLogs(ctx, "r1")
	fmt.Println("journal:")
	for _, l := range logs {
		fmt.Printf("  seq=%d tag=%q payload=%s\n", l.Seq, l.Tag, l.Payload)
	}
}

func waitDone(st *internal.MemStore, id string) rerun.Run {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := st.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	panic("rerun: run " + id + " did not finish in time")
}
