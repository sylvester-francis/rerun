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

package rerun_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/sqlite"
)

type goldenExpect struct {
	Runs []struct {
		ID        string          `json:"id"`
		Workflow  string          `json:"workflow"`
		Final     string          `json:"final"`
		Result    json.RawMessage `json:"result"`
		ResultErr string          `json:"resultErr"`
	} `json:"runs"`
}

// registerGoldenWorkflows mirrors tools/goldengen exactly. If a change makes
// these bodies impossible to express, that change breaks compatibility.
func registerGoldenWorkflows(e *rerun.Engine) {
	e.Handle("order", func(w *rerun.W) error {
		id, _ := rerun.Input[string](w)
		txn, err := rerun.Do(w, "charge", func(context.Context) (string, error) {
			return "txn-" + id, nil
		})
		if err != nil {
			return err
		}
		rerun.Return(w, txn)
		return nil
	})
	e.Handle("declined", func(w *rerun.W) error {
		_, err := rerun.Do(w, "charge", func(context.Context) (string, error) {
			return "", errors.New("card declined")
		})
		return err
	})
	e.Handle("features", func(w *rerun.W) error {
		v := rerun.Version(w, "golden-change", 1, 1)
		_, _ = rerun.Do(w, "v", func(context.Context) (int, error) { return v, nil })
		if err := rerun.Sleep(w, time.Millisecond); err != nil {
			return err
		}
		out, err := rerun.Retry(w, "flaky",
			rerun.RetryPolicy{MaxAttempts: 2, Backoff: rerun.FixedBackoff(time.Millisecond)},
			func(context.Context) (string, error) { return "ok", nil })
		if err != nil {
			return err
		}
		rerun.Return(w, out)
		return nil
	})
	e.Handle("approval", func(w *rerun.W) error {
		ok, err := rerun.Wait[bool](w, "go")
		if err != nil || !ok {
			return err
		}
		rerun.Return(w, "approved")
		return nil
	})
}

func TestGolden_V01JournalsReplayUnchanged(t *testing.T) {
	src, err := os.Open(filepath.Join("testdata", "golden", "v0_1", "golden.db"))
	must(t, err)
	defer src.Close()
	dbPath := filepath.Join(t.TempDir(), "golden.db")
	dst, err := os.Create(dbPath)
	must(t, err)
	_, err = io.Copy(dst, src)
	must(t, err)
	must(t, dst.Close())

	raw, err := os.ReadFile(filepath.Join("testdata", "golden", "v0_1", "expect.json"))
	must(t, err)
	var exp goldenExpect
	must(t, json.Unmarshal(raw, &exp))

	store := sqlite.New(dbPath)
	e := rerun.New(store)
	registerGoldenWorkflows(e)
	ctx := context.Background()
	must(t, e.Recover(ctx))

	deadline := time.Now().Add(5 * time.Second)
	for _, want := range exp.Runs {
		for {
			var got string
			gotErr := ""
			res, rerr := rerun.Result[json.RawMessage](ctx, e, want.ID)
			switch {
			case rerr == nil:
				got, gotErr = "Done", ""
				if string(res) != string(want.Result) {
					t.Fatalf("run %s: result %s, want %s", want.ID, res, want.Result)
				}
			case strings.Contains(rerr.Error(), "failed:"):
				got = "Failed"
				gotErr = rerr.Error()
			default:
				got = "" // not finished yet
			}
			if got == want.Final {
				if want.ResultErr != "" && !strings.Contains(gotErr, want.ResultErr) {
					t.Fatalf("run %s: error %q, want it to contain %q", want.ID, gotErr, want.ResultErr)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("run %s: never reached %s", want.ID, want.Final)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}
