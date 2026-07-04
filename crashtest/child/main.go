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

// Command child is the crash-test subject: a four-step workflow whose every
// step appends its tag to an effects file, self-killed with SIGKILL after a
// configured number of live steps. The parent test restarts it in recover mode
// and asserts each effect happened exactly once.
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
)

type killer struct {
	after int64
	seen  atomic.Int64
}

func (k *killer) OnStart(rerun.Run)             {}
func (k *killer) OnFinish(string, rerun.Status) {}
func (k *killer) OnStep(string, rerun.Log) {
	if k.after >= 0 && k.seen.Add(1) > k.after {
		syscall.Kill(os.Getpid(), syscall.SIGKILL)
		select {} // never reached; SIGKILL is not deliverable to a handler
	}
}

func effect(tag string) {
	f, err := os.OpenFile(os.Getenv("RERUN_EFFECTS"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	fmt.Fprintln(f, tag)
}

func main() {
	after, _ := strconv.ParseInt(os.Getenv("RERUN_KILL_AFTER"), 10, 64)
	e := rerun.New(sqlite.New(os.Getenv("RERUN_DB")),
		rerun.WithObserver(&killer{after: after}))
	e.Handle("crashy", func(w *rerun.W) error {
		for _, tag := range []string{"alpha", "beta", "gamma", "delta"} {
			if _, err := rerun.Do(w, tag, func(context.Context) (string, error) {
				effect(tag)
				return tag, nil
			}); err != nil {
				return err
			}
		}
		rerun.Return(w, "done")
		return nil
	})
	ctx := context.Background()
	if os.Getenv("RERUN_MODE") == "start" {
		if err := e.Start(ctx, "crashy", "run-1"); err != nil {
			panic(err)
		}
	} else if err := e.Recover(ctx); err != nil {
		panic(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := rerun.Result[string](ctx, e, "run-1"); err == nil {
			os.Exit(0)
		}
		time.Sleep(10 * time.Millisecond)
	}
	os.Exit(3) // did not finish: parent will run recover again
}
