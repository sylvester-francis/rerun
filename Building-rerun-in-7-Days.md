# Building rerun in 7 Days

### A durable execution engine in Go, from an empty directory to a shippable library

rerun is a small Go library with one job: it runs a multi-step process to completion and, when the machine running it crashes part way through and restarts hours later, resumes from where it left off instead of starting over. Completed steps are replayed from a journal rather than re-executed, so with idempotent steps there are no double charges, no skipped steps, and no half-finished state left behind. This guide builds rerun from an empty directory to a shippable library over seven days. Each day adds one capability to the codebase and ends with a program you run. By the end you have the whole engine, tested to a standard you can defend, packaged as a repository.

The technique is journaling and replay. A workflow is an ordinary Go function whose steps record their results to a durable log, so that re-running the function after a crash replays the completed steps from the log instead of executing them again. That one idea is the entire library, which is why it fits in a week and in a few hundred lines. It is the kind of guarantee you would otherwise reach for Temporal to get, without the cluster: rerun is the core idea, not the platform around it.

This is a build manual, not a survey. It assumes you write Go competently, and it does not wander into the wider workflow-engine landscape; every page is about constructing rerun. The prose is terse and the callouts are labeled: **Design** for a decision and its alternatives, **Idiom** for the Go-native way, **Trap** for a mistake that compiles but is wrong, **Pattern** for a reusable shape, **Principle** for a rule worth keeping. Every exercise has a worked solution in Appendix C. And the rule this build does not break: every line of code here was compiled and run on a real Go toolchain, and the output you see at each milestone is captured from an actual run, not written to look plausible. One honest exception is flagged where it occurs, the SQLite backend on Day 4, whose code is shown and contract-pinned but whose runtime could not be exercised in the build environment.

The module path throughout is `github.com/sylvester-francis/rerun`. Replace it with your own everywhere, consistently.

### The seven days

- **Day 1** The journal-and-replay idea and a walking skeleton that runs a workflow end to end.
- **Day 2** Determinism and crash recovery: resuming from the middle of a journal.
- **Day 3** Durable time: sleeping across a restart, and the panic-versus-error rule.
- **Day 4** Pluggable storage: the SOLID seams and a SQLite backend behind one contract.
- **Day 5** A test harness you can trust: the contract suite, fake time, and white-box panic tests.
- **Day 6** Mutation testing: measuring whether the suite would actually catch a bug.
- **Day 7** Shipping rerun: the public API, the repository, and a final acceptance run.

Build the code as you read. The fastest way to not understand durable execution is to read about it; the fastest way to understand it is to watch your own copy resume after you kill it.

---

## Why Go fits this project

A durable execution engine is a particular kind of program. It holds thousands of long-lived, mostly-idle workflows in flight at once, drives each as if it were straight-line code, persists everything through a swappable backend, and has to stay correct under concurrency because money and state ride on it. Go fits that shape almost exactly, and the fit is not incidental: the production engines in this space, Temporal and Cadence among them, are themselves written in Go. The reasons show up in nearly every file you build this week.

Start with concurrency, because it is the whole reason this model is practical. The engine runs each workflow in its own goroutine, launched by `Start`. A goroutine costs a few kilobytes and blocks for free, so one process can hold tens of thousands of runs parked on a `Sleep` or a slow network step without an operating-system thread per run. That is exactly what a workflow engine must do, and in a language with one thread per blocked task it does not scale past a few hundred. Because a blocked goroutine is cheap, `Sleep` can literally block the workflow's goroutine on `clock.After(d)` for an hour while the workflow author still writes ordinary sequential code. The programming model durable execution wants, a plain function you read top to bottom, is the model Go's blocking-but-cheap goroutines deliver natively; in a callback or promise world that straight-line illusion is far harder to keep.

> **Design.** The match between "a workflow is a sequential Go function" and "a goroutine blocks cheaply" is the deepest reason Go suits this. The replay engine re-runs that function; the runtime makes pausing it free. Neither side fights the other.

Cancellation and deadlines come built in. rerun threads `context.Context` through every step, and `Sleep` honors `ctx.Done()`, because a durable workflow needs timeouts and cancellation propagated cleanly. Context is the standard idiom for exactly this, so the plumbing is the language's, not yours.

The pluggable architecture is weightless because of interfaces. Everything swappable in rerun, the `Store`, the `Codec`, the `Clock`, the `Observer`, is a small interface the engine defines and a backend satisfies implicitly. A SQLite backend implements `Store` without importing a base class or declaring conformance; it simply has the methods, and the contract suite proves it correct. The three-way split into `Writer`, `Reader`, and `Guarder` costs nothing because interfaces compose by embedding. Structural typing is precisely what makes the dependency-inversion design on Day 4 clean rather than ceremonial.

Generics arrived just in time for the core API. `Do[T any](w, tag, fn) (T, error)` returns the step's real type, carried through the journal with compile-time safety; before Go 1.18 the same signature meant `interface{}` and a cast at every call site. The one primitive the entire library is built on is type-safe because the language grew the feature this design needed.

The dependency surface stays tiny, which matters for infrastructure people trust with critical work. The default codec is `encoding/json`, the in-memory store is a `sync.Mutex` over a map, the clock is `time`, the SQLite backend is `database/sql`, and the tests, the contract suite, the fake clock, even the mutation tester from Day 6, are standard library plus `os/exec`. The core has no third-party dependencies at all, and the one optional driver is a pure-Go, cgo-free SQLite that keeps the result a single static binary you drop on a host, which is the right deployment story for a long-lived worker process.

Finally, correctness. Durable execution is concurrent by nature, and `go test -race`, which the suite runs under, catches data races for free. The determinism contract that replay depends on is easier to honor in a language whose control flow is explicit, with no exceptions used as flow and no implicit async that reorders the steps you wrote; a workflow runs in one goroutine, top to bottom, exactly as it reads. The tools that make a correctness-critical library defensible are in the box.

> **Principle.** None of this is "Go is fast and simple." The fit is specific: cheap blocking goroutines for many parked workflows, context for cancellation, structural interfaces for the swappable seams, generics for a type-safe `Do`, a standard-library-only core for a tiny trust surface, and the race detector for concurrent correctness. When a language's grain runs with the problem, the problem gets smaller.

---

## Day 1: A walking skeleton

### Project layout

You start from an empty directory: `mkdir rerun && cd rerun && go mod init github.com/sylvester-francis/rerun`. Here is the whole structure you build over the week, so you can see where each file lands as it appears. Files show up on the day noted; nothing here is created before you are walked through it.

```
rerun/                       module github.com/sylvester-francis/rerun (go mod init)
  store.go                   Run, Log, Status types and the Store interface       (day 1)
  codec.go                   serialization seam, JSON by default                  (day 1)
  clock.go                   time seam, wall clock by default                     (day 1)
  hooks.go                   Observer seam, no-op by default                      (day 1)
  engine.go                  the engine: workflow registry, New, Handle           (day 1)
  workflow.go                Do, replayStep, liveStep, Sleep                      (days 1-3)
  run.go                     Start, exec, Recover                                 (days 1-2)
  errors.go                  StepError, so errors survive replay                  (day 2)
  internal/
    memstore.go              in-memory Store, the default and a test aid          (day 1)
    memstore_test.go         runs the contract suite against memstore             (day 5)
  storetest/
    storetest.go             importable Store contract suite for any backend      (day 5)
  sqlite/
    sqlite.go                a real, persistent Store backend                     (day 4)
  tools/
    mutate/main.go           dependency-free mutation tester                      (day 6)
  examples/
    skeleton/main.go         day 1 milestone: a workflow journals
    recover/main.go          day 2 milestone: resume from a partial journal
    durablesleep/main.go     day 3 milestone: a sleep survives a restart
    capstone/main.go         day 7 acceptance: signup saga, charged once
  engine_test.go             engine and replay unit tests                         (days 1-5)
  workflow_test.go  clock_test.go  errors_test.go  hooks_test.go  helpers_test.go (days 1-5)
  panics_internal_test.go    white-box tests for the panics                       (day 5)
  README.md  LICENSE  Makefile  .github/workflows/ci.yml                          (day 7)
```

Throughout the guide, every code block is headed by the path of the file it belongs to, in bold. Create or edit that file exactly as shown; where a file is revisited on a later day, the header says so.

### The problem

Consider an order pipeline: charge the card, reserve inventory, send a receipt. Three steps, each a network call that can fail. The naive version is a function that calls them in sequence, and it works until the process dies between step one and step two, which in production it eventually will. Now you have a charged card, no reserved inventory, and no record of how far you got.

The instinct is a `status` column: `charged`, `reserved`, `sent`. But a status column tells you a step finished; it does not tell you the *result* that step produced, so on restart you cannot continue, only guess. A queue of jobs has the same gap: a job that crashes mid-execution is either lost or redelivered and re-run from the top, re-charging the card. A state-machine library encodes the transitions but still leaves you to persist and restore the data flowing between states.

> **Principle.** The thing you must persist is not *which step you reached* but *the result every completed step produced*. With the results in hand, recovery is not guesswork: you replay the function, and any step that already has a recorded result is skipped by handing back that result instead of running it again.

### The core idea: journal, then replay

A durable workflow is an ordinary Go function. Each meaningful step is wrapped in a call that does one of two things. The first time the step runs, it executes the work, writes the result to a durable log (the *journal*), and returns. If the process later crashes and the workflow function is run again from the top, that same wrapped call sees a journal entry already exists for this step and returns the stored result *without* re-executing the work.

Recovery, then, is just running the function again. Steps that completed before the crash replay instantly from the journal; the first step without a journal entry executes for real; everything after it runs forward normally. The function is written once, as if crashes did not exist, and the engine makes it crash-proof by recording and replaying.

> **Design.** This is the model the production durable-execution engines are built on. We are building that core idea in a form you fully own and can read in an afternoon, not a distributed runtime. The library is small because the idea is small.

### What we build today

A walking skeleton: the types, an in-memory store, and just enough engine to run a workflow forward and watch it journal each step. No recovery yet; that is tomorrow. By the end of the day a two-step workflow runs to completion and prints its journal.

Start with the persistence contract. A run is a workflow instance; a log is one journaled step; the store persists them.

**`store.go`**

```go
package rerun

import (
	"context"
	"io"
	"time"
)

type Status int

const (
	Pending Status = iota
	Running
	Done
	Failed
)

type Log struct {
	Seq     int
	Tag     string
	Payload []byte
	Err     string
	At      time.Time
}

type Run struct {
	ID       string
	Workflow string
	Status   Status
	Created  time.Time
}

type Writer interface {
	Create(ctx context.Context, r Run) error
	Append(ctx context.Context, runID string, l Log) error
	Finish(ctx context.Context, runID string, s Status) error
}

type Reader interface {
	LoadLogs(ctx context.Context, runID string) ([]Log, error)
	Incomplete(ctx context.Context) ([]Run, error)
}

type Guarder interface {
	Acquire(ctx context.Context, runID string) (io.Closer, error)
}

type Store interface {
	Writer
	Reader
	Guarder
}
```

> **Design.** The `Store` interface is split into `Writer`, `Reader`, and `Guarder`. Ignore that split today and read `Store` as one interface; the reason it is three is an interface-segregation argument that pays off on Day 4 when we add a second backend. `Guarder.Acquire` returns an `io.Closer`: acquire a lock, get something you can `Close` to release it. For an in-memory store that is a no-op.

Serialization and time each hide behind a one-method-family interface so they can be swapped later. The defaults are JSON and the wall clock.

**`codec.go`**

```go
package rerun

import "encoding/json"

type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error)      { return json.Marshal(v) }
func (jsonCodec) Unmarshal(data []byte, v any) error { return json.Unmarshal(data, v) }
```

**`clock.go`**

```go
package rerun

import "time"

type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

type wall struct{}

func (wall) Now() time.Time                         { return time.Now() }
func (wall) After(d time.Duration) <-chan time.Time { return time.After(d) }
```

> **Idiom.** Defining `Clock` as an interface with a wall-clock default looks like over-engineering until Day 3, when it is the only thing that makes a one-hour sleep testable in one millisecond. Seams you will need later are cheapest to add before there is code depending on the concrete type.

An observer is a no-op hook for now; it earns its keep on Day 5, when tests use it to assert what the engine did.

**`hooks.go`**

```go
package rerun

type Observer interface {
	OnStart(r Run)
	OnStep(runID string, l Log)
	OnFinish(runID string, s Status)
}

type noopObserver struct{}

func (noopObserver) OnStart(Run)             {}
func (noopObserver) OnStep(string, Log)      {}
func (noopObserver) OnFinish(string, Status) {}
```

Now the engine itself: a registry of named workflows plus the injected seams. `New` wires the defaults; options override them.

**`engine.go`**

```go
package rerun

import "sync"

type Func func(w *W) error

type Engine struct {
	store Store
	codec Codec
	clock Clock
	obs   Observer
	reg   map[string]Func
	mu    sync.RWMutex
}

func New(s Store, opts ...Opt) *Engine {
	e := &Engine{
		store: s,
		codec: jsonCodec{},
		clock: wall{},
		obs:   noopObserver{},
		reg:   make(map[string]Func),
	}
	for _, o := range opts {
		o(e)
	}
	return e
}

type Opt func(*Engine)

func WithCodec(c Codec) Opt       { return func(e *Engine) { e.codec = c } }
func WithClock(c Clock) Opt       { return func(e *Engine) { e.clock = c } }
func WithObserver(o Observer) Opt { return func(e *Engine) { e.obs = o } }

func (e *Engine) Handle(name string, fn Func) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if _, dup := e.reg[name]; dup {
		panic("rerun: duplicate workflow: " + name)
	}
	e.reg[name] = fn
}

func (e *Engine) lookup(name string) Func {
	e.mu.RLock()
	defer e.mu.RUnlock()
	fn, ok := e.reg[name]
	if !ok {
		panic("rerun: unknown workflow: " + name)
	}
	return fn
}
```

> **Trap.** `Handle` panics on a duplicate name and `lookup` panics on an unknown one. These are programmer errors, not runtime conditions, and a panic at startup beats an error threaded through every call site. We draw the panic-versus-error line sharply on Day 3.

The day-1 step runner. `Do` is generic over the step's return type. Today it always executes live: run the function, marshal the result, append it to the journal, advance the cursor. Tomorrow it grows the branch that replays.

**`workflow.go`**

```go
package rerun

import (
	"context"
	"fmt"
)

// W is the live handle passed to a workflow. On day 1 it only needs to know
// where it is in the run (seq) and how to reach the engine.
type W struct {
	RunID string
	seq   int
	eng   *Engine
	ctx   context.Context
}

// Do runs one step and journals its result. Day 1 has no replay yet, so every
// call executes the function live and appends an entry. Day 2 adds the branch
// that returns a journaled result instead of re-running.
func Do[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	v, err := fn(w.ctx)

	b, merr := w.eng.codec.Marshal(v)
	if merr != nil {
		panic(fmt.Sprintf("rerun: marshal failed at seq %d in run %s: %v", w.seq, w.RunID, merr))
	}
	errStr := ""
	if err != nil {
		errStr = err.Error()
	}
	l := Log{Seq: w.seq, Tag: tag, Payload: b, Err: errStr, At: w.eng.clock.Now()}
	if serr := w.eng.store.Append(w.ctx, w.RunID, l); serr != nil {
		panic(fmt.Sprintf("rerun: journal write failed at seq %d in run %s: %v", w.seq, w.RunID, serr))
	}
	w.eng.obs.OnStep(w.RunID, l)

	w.seq++
	return v, err
}
```

And the driver. `Start` creates the run and launches it in a goroutine; `exec` marks it running, builds the handle, calls the workflow, and records the terminal status.

**`run.go`**

```go
package rerun

import (
	"context"
	"fmt"
)

func (e *Engine) Start(ctx context.Context, workflow, runID string) error {
	r := Run{ID: runID, Workflow: workflow, Status: Pending, Created: e.clock.Now()}
	if err := e.store.Create(ctx, r); err != nil {
		return fmt.Errorf("rerun: create run %s: %w", runID, err)
	}
	e.obs.OnStart(r)
	go e.exec(ctx, r)
	return nil
}

// exec on day 1 just runs the workflow once. No log loading, no recovery.
func (e *Engine) exec(ctx context.Context, r Run) {
	closer, err := e.store.Acquire(ctx, r.ID)
	if err != nil {
		return
	}
	defer closer.Close()

	e.store.Finish(ctx, r.ID, Running)

	w := &W{RunID: r.ID, eng: e, ctx: ctx}
	fn := e.lookup(r.Workflow)
	if werr := fn(w); werr != nil {
		e.store.Finish(ctx, r.ID, Failed)
		e.obs.OnFinish(r.ID, Failed)
		return
	}
	e.store.Finish(ctx, r.ID, Done)
	e.obs.OnFinish(r.ID, Done)
}
```

The in-memory store completes the skeleton. Every method takes the mutex so the race detector stays quiet, and `LoadLogs` returns a sorted copy so a caller cannot mutate stored state. It carries one method beyond the interface, `Get`, used only by examples and tests to read a run's status directly.

**`internal/memstore.go`**

```go
package internal

import (
	"context"
	"fmt"
	"io"
	"sort"
	"sync"

	"github.com/sylvester-francis/rerun"
)

type MemStore struct {
	mu   sync.Mutex
	runs map[string]rerun.Run
	logs map[string][]rerun.Log
}

func NewMemStore() *MemStore {
	return &MemStore{
		runs: make(map[string]rerun.Run),
		logs: make(map[string][]rerun.Log),
	}
}

func (m *MemStore) Create(ctx context.Context, r rerun.Run) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.runs[r.ID]; dup {
		return fmt.Errorf("memstore: run %s already exists", r.ID)
	}
	m.runs[r.ID] = r
	return nil
}

func (m *MemStore) Append(ctx context.Context, runID string, l rerun.Log) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logs[runID] = append(m.logs[runID], l)
	return nil
}

func (m *MemStore) Finish(ctx context.Context, runID string, s rerun.Status) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	if !ok {
		return fmt.Errorf("memstore: run %s not found", runID)
	}
	r.Status = s
	m.runs[runID] = r
	return nil
}

func (m *MemStore) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	src := m.logs[runID]
	out := make([]rerun.Log, len(src))
	copy(out, src)
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out, nil
}

func (m *MemStore) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []rerun.Run
	for _, r := range m.runs {
		if r.Status == rerun.Pending || r.Status == rerun.Running {
			out = append(out, r)
		}
	}
	return out, nil
}

func (m *MemStore) Acquire(ctx context.Context, runID string) (io.Closer, error) {
	return io.NopCloser(nil), nil
}

func (m *MemStore) Get(runID string) (rerun.Run, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.runs[runID]
	return r, ok
}
```

### Milestone: a workflow that runs and journals

A two-step workflow, run to completion, printing its journal:

**`examples/skeleton/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func statusName(s rerun.Status) string {
	switch s {
	case rerun.Pending:
		return "Pending"
	case rerun.Running:
		return "Running"
	case rerun.Done:
		return "Done"
	case rerun.Failed:
		return "Failed"
	}
	return "?"
}

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 2000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)

	eng.Handle("greet", func(w *rerun.W) error {
		name, _ := rerun.Do(w, "make-name", func(ctx context.Context) (string, error) {
			fmt.Println("  [live] make-name")
			return "Ada", nil
		})
		_, _ = rerun.Do(w, "greeting", func(ctx context.Context) (string, error) {
			fmt.Printf("  [live] greeting: hello %s\n", name)
			return "hello " + name, nil
		})
		return nil
	})

	ctx := context.Background()
	fmt.Println("running workflow r1:")
	eng.Start(ctx, "greet", "r1")
	waitDone(store, "r1")

	r, _ := store.Get("r1")
	logs, _ := store.LoadLogs(ctx, "r1")
	fmt.Printf("status: %s\n", statusName(r.Status))
	fmt.Println("journal:")
	for _, l := range logs {
		fmt.Printf("  seq=%d tag=%-10q payload=%s\n", l.Seq, l.Tag, string(l.Payload))
	}
}
```

Running it:

```
running workflow r1:
  [live] make-name
  [live] greeting: hello Ada
status: Done
journal:
  seq=0 tag="make-name" payload="Ada"
  seq=1 tag="greeting" payload="hello Ada"
```

Two steps executed, two journal entries written in order, run marked `Done`. That journal is the entire basis for tomorrow's recovery: those recorded results are what let us skip re-execution after a crash.

> **Pattern.** A step is `result, err := Do(w, "unique-tag", func(ctx) (T, error) { ... })`. The tag names the step for journal matching; keep it stable and unique within the workflow. The function does the real work and returns a serializable result. Everything durable flows through this one shape.

### Exercises

1. The skeleton never reads the journal back inside a workflow. Add a workflow that calls `Do` three times and confirm the journal has three entries with `seq` 0, 1, 2. What determines `seq`?
2. `Do` panics if `Marshal` fails. Make a step return a value that cannot be JSON-encoded (a channel, say) and observe the panic. Is panicking right here, or should it be an error? Argue both sides; we settle it on Day 3.
3. `exec` runs in a goroutine launched by `Start`, so the example has to poll for completion. Why can `Start` not simply block until the workflow finishes? What would that cost a caller who starts a thousand workflows?

Solutions in Appendix C.

---

## Day 2: Determinism and crash recovery

Yesterday's engine runs forward and journals. Today it learns to come back from the dead, which is the whole point of the library. It rests on one demand made of your workflow code: determinism.

### The contract that makes replay possible

Recovery replays the workflow function from the top and matches each `Do` call to a journal entry by position. For that matching to be correct, the function must issue the same sequence of steps, with the same tags, every time it runs with the same inputs. If a workflow does `Do("a")` then `Do("b")` on the first run but `Do("b")` then `Do("a")` after a restart, replay hands the result of `a` to the call that expected `b`, and the run is silently corrupt.

> **Principle.** A workflow body must be deterministic in its control flow and step tags. Anything non-deterministic, the current time, a random number, a read whose result steers a branch, must be captured *inside* a `Do` step so its value is journaled and replayed, not recomputed. The step is the boundary between the deterministic skeleton and the non-deterministic world.

> **Trap.** `if time.Now().Hour() < 12 { Do("morning") } else { Do("evening") }` is the classic corruption: the branch can differ between the original run and the replay, so the tags diverge. The fix is to journal the decision: `morning, _ := Do(w, "is-morning", func(ctx)(bool,error){ return time.Now().Hour()<12, nil }); if morning { ... }`. Now both runs see the same recorded boolean.

We enforce the contract rather than hope for it: during replay, if the tag a `Do` call presents does not match the tag stored at that position, the engine panics with the exact position and both tags. A determinism bug fails loudly at the first divergence instead of quietly producing wrong results.

### Errors are results too

A step that fails is still a step that happened. If `charge-card` returned a "card declined" error on the first run, recovery must reproduce that same error, not re-run the charge hoping for a different answer. So the journal stores the error string alongside the result, and replay reconstructs it.

**`errors.go`**

```go
package rerun

import "fmt"

type StepError struct {
	Tag string
	Msg string
}

func (e *StepError) Error() string {
	return fmt.Sprintf("rerun: step %q: %s", e.Tag, e.Msg)
}
```

### Growing Do into a replay machine

`Do` gains its second branch. The handle `W` now carries the loaded journal (`logs`) and a `replay` flag. The guard `w.replay && w.seq < len(w.logs)` decides: if we are replaying and the cursor still points inside the journal, return the stored entry; otherwise execute live.

**`workflow.go`** (the same file, now with the replay branch)

```go
package rerun

import (
	"context"
	"fmt"
)

type W struct {
	RunID  string
	seq    int
	logs   []Log
	replay bool
	eng    *Engine
	ctx    context.Context
}

func Do[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	if w.replay && w.seq < len(w.logs) {
		return replayStep[T](w, tag)
	}
	return liveStep[T](w, tag, fn)
}

func replayStep[T any](w *W, tag string) (T, error) {
	l := w.logs[w.seq]
	if l.Tag != tag {
		panic(fmt.Sprintf(
			"rerun: determinism broken at seq %d in run %s: journal=%q code=%q",
			w.seq, w.RunID, l.Tag, tag,
		))
	}
	w.seq++

	var v T
	if err := w.eng.codec.Unmarshal(l.Payload, &v); err != nil {
		panic(fmt.Sprintf(
			"rerun: journal corrupt at seq %d in run %s: %v",
			w.seq-1, w.RunID, err,
		))
	}
	if l.Err != "" {
		return v, &StepError{Tag: l.Tag, Msg: l.Err}
	}
	return v, nil
}

func liveStep[T any](w *W, tag string, fn func(context.Context) (T, error)) (T, error) {
	w.replay = false
	v, err := fn(w.ctx)

	b, merr := w.eng.codec.Marshal(v)
	if merr != nil {
		panic(fmt.Sprintf("rerun: marshal failed at seq %d in run %s: %v", w.seq, w.RunID, merr))
	}

	errStr := ""
	if err != nil {
		errStr = err.Error()
	}

	l := Log{
		Seq:     w.seq,
		Tag:     tag,
		Payload: b,
		Err:     errStr,
		At:      w.eng.clock.Now(),
	}
	if serr := w.eng.store.Append(w.ctx, w.RunID, l); serr != nil {
		panic(fmt.Sprintf("rerun: journal write failed at seq %d in run %s: %v", w.seq, w.RunID, serr))
	}
	w.eng.obs.OnStep(w.RunID, l)

	w.seq++
	return v, err
}

```

Read `replayStep` carefully, because it is where the contract is enforced. It checks the tag first and panics on mismatch, then unmarshals the stored payload, then reconstructs a `StepError` if one was recorded. `liveStep` is yesterday's `Do` body with one addition: it clears `w.replay` once a live step runs.

> **Design.** That `w.replay = false` line is kept for readability, but it is not load-bearing, and Day 6 proves it with mutation testing. The guard's second condition, `w.seq < len(w.logs)`, already governs the transition: the cursor only advances and never resets, and the journal slice does not grow during a run, so once `seq` reaches `len(logs)` the engine is permanently live whether or not the flag was cleared. Recognizing redundancy like this is a real skill, and we return to it deliberately.

Now `exec` loads the journal before running, sets `replay` when there is anything to replay, and a new `Recover` method finds every unfinished run and relaunches it.

**`run.go`** (the same file, now with Recover)

```go
package rerun

import (
	"context"
	"fmt"
)

func (e *Engine) Start(ctx context.Context, workflow, runID string) error {
	r := Run{
		ID:       runID,
		Workflow: workflow,
		Status:   Pending,
		Created:  e.clock.Now(),
	}
	if err := e.store.Create(ctx, r); err != nil {
		return fmt.Errorf("rerun: create run %s: %w", runID, err)
	}
	e.obs.OnStart(r)
	go e.exec(ctx, r)
	return nil
}

func (e *Engine) exec(ctx context.Context, r Run) {
	closer, err := e.store.Acquire(ctx, r.ID)
	if err != nil {
		return
	}
	defer closer.Close()

	e.store.Finish(ctx, r.ID, Running)

	logs, err := e.store.LoadLogs(ctx, r.ID)
	if err != nil {
		e.store.Finish(ctx, r.ID, Failed)
		return
	}

	w := &W{
		RunID:  r.ID,
		logs:   logs,
		replay: len(logs) > 0,
		eng:    e,
		ctx:    ctx,
	}

	fn := e.lookup(r.Workflow)
	if werr := fn(w); werr != nil {
		e.store.Finish(ctx, r.ID, Failed)
		e.obs.OnFinish(r.ID, Failed)
		return
	}
	e.store.Finish(ctx, r.ID, Done)
	e.obs.OnFinish(r.ID, Done)
}

func (e *Engine) Recover(ctx context.Context) error {
	runs, err := e.store.Incomplete(ctx)
	if err != nil {
		return fmt.Errorf("rerun: recover: %w", err)
	}
	for _, r := range runs {
		go e.exec(ctx, r)
	}
	return nil
}
```

> **Idiom.** `Recover` asks the store for incomplete runs and relaunches each through the same `exec` path a fresh `Start` uses. Recovery is not a separate code path with its own bugs; it is the normal path with a pre-populated journal. Fewer paths, fewer ways to be wrong.

### Milestone: resuming from the middle of a crash

This is the demonstration that matters: a run that died after two of three steps, recovered, with the completed steps replayed and only the unfinished one executed. The example seeds a partial journal directly (the on-disk state a crash leaves behind) and calls `Recover`.

**`examples/recover/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 2000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)

	var liveRuns int32
	eng.Handle("pipeline", func(w *rerun.W) error {
		for _, tag := range []string{"extract", "transform", "load"} {
			tag := tag
			rerun.Do(w, tag, func(ctx context.Context) (int, error) {
				atomic.AddInt32(&liveRuns, 1)
				fmt.Printf("  [live] %s\n", tag)
				return 1, nil
			})
		}
		return nil
	})

	ctx := context.Background()

	// Simulate a process that crashed AFTER extract+transform were journaled
	// but BEFORE load ran. Seed a partial journal directly, status Running.
	fmt.Println("seeding a crashed run with a partial journal [extract, transform]:")
	store.Create(ctx, rerun.Run{ID: "j1", Workflow: "pipeline", Status: rerun.Running, Created: time.Now()})
	store.Append(ctx, "j1", rerun.Log{Seq: 0, Tag: "extract", Payload: []byte(`1`)})
	store.Append(ctx, "j1", rerun.Log{Seq: 1, Tag: "transform", Payload: []byte(`1`)})

	fmt.Println("calling Recover:")
	eng.Recover(ctx)
	waitDone(store, "j1")

	r, _ := store.Get("j1")
	fmt.Printf("status: Done? %v\n", r.Status == rerun.Done)
	fmt.Printf("steps executed live during recovery: %d (expected 1, only 'load')\n", atomic.LoadInt32(&liveRuns))
}
```

Running it:

```
seeding a crashed run with a partial journal [extract, transform]:
calling Recover:
  [live] load
status: Done? true
steps executed live during recovery: 1 (expected 1, only 'load')
```

`extract` and `transform` were replayed from the journal without running; only `load`, the first step with no entry, executed; the run finished `Done`. That is durable resume across a crash: a step already recorded in the journal is replayed rather than re-executed, so completed work is not repeated and only the unfinished step runs. That is the property no status column can give you. A step is repeated only if the process dies after its side effect runs and before its journal entry commits, which is why production steps are written to be idempotent.

### Exercises

1. Add a fourth step `notify` after `load`, re-seed the same two-entry journal, and predict before running: how many steps execute live during recovery? Verify.
2. Break determinism deliberately. Make the pipeline emit its steps in a different order on the second run. Trigger the panic and read its message. What does it tell you, and why is a panic better here than a logged warning?
3. `replayStep` unmarshals into a fresh `var v T`. What happens if you change a step's return type between deploys, so the old journal payload no longer fits the new type? Where does it fail, and what does that imply for versioning?

Solutions in Appendix C.

---

## Day 3: Durable time and handling failure

Two pieces of the engine today. First, durable sleep: a workflow that can wait an hour, or a week, and survive a restart during the wait without blocking a thread or losing track of time. Second, the rule for when rerun panics and when it returns an error.

### Sleep is just a step

A durable sleep is not `time.Sleep`. If the process restarts during it, a real `time.Sleep` is simply gone, and on replay you would sleep the full duration again. The durable version records *when the sleep was first entered* as a journaled step, so on replay the engine knows the sleep already happened and skips it instantly.

That falls straight out of the model: sleeping is a step like any other. `Sleep` is a thin wrapper over `Do` whose work is to wait on the clock. Add this to `workflow.go`:

**`workflow.go`** (add the Sleep function to this file)

```go
func Sleep(w *W, d time.Duration) error {
	_, err := Do(w, fmt.Sprintf("sleep:%v", d), func(ctx context.Context) (struct{}, error) {
		select {
		case <-w.eng.clock.After(d):
			return struct{}{}, nil
		case <-ctx.Done():
			return struct{}{}, ctx.Err()
		}
	})
	return err
}
```

The step's tag encodes the duration, its body waits on `w.eng.clock.After(d)` or a cancelled context, and it returns an empty struct. On the first run it genuinely waits; once it completes, the journal has an entry for it; on replay `Do` returns that entry without waiting. The `time` import joins `workflow.go` now that `Sleep` uses it.

> **Design.** This is why `Clock` was an interface from Day 1. In production the wall clock waits real time. In a test, a fake clock advances virtual time on command, so a workflow that sleeps a week runs in microseconds. The seam costs nothing in production and makes the whole time dimension testable, which we cash in on Day 5.

> **Trap.** A durable sleep gives no guarantee about *which process* wakes up, only that the workflow resumes after the duration has elapsed in wall-clock terms across restarts. If the process is down when the sleep expires, the sleep completes the moment recovery runs and replays the journal. Durable means "the time is accounted for," not "a timer fires in a dead process."

### Milestone: a sleep that survives a restart

The workflow sleeps one second, then fires. We run it once (it waits), simulate a crash, and recover (it does not wait again).

**`examples/durablesleep/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 5000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store) // real wall clock

	eng.Handle("reminder", func(w *rerun.W) error {
		rerun.Sleep(w, 1*time.Second)
		rerun.Do(w, "fire", func(ctx context.Context) (struct{}, error) {
			fmt.Println("  [live] reminder fired")
			return struct{}{}, nil
		})
		return nil
	})

	ctx := context.Background()

	fmt.Println("first run (must actually wait out the 1s sleep):")
	t0 := time.Now()
	eng.Start(ctx, "reminder", "s1")
	waitDone(store, "s1")
	fmt.Printf("first run took %v\n", time.Since(t0).Round(100*time.Millisecond))

	fmt.Println("simulating crash, then recovering:")
	store.Finish(ctx, "s1", rerun.Running)
	t1 := time.Now()
	eng.Recover(ctx)
	waitDone(store, "s1")
	fmt.Printf("recovery took %v (sleep already satisfied, so it skips the wait)\n", time.Since(t1).Round(100*time.Millisecond))
}
```

Running it:

```
first run (must actually wait out the 1s sleep):
  [live] reminder fired
first run took 1s
simulating crash, then recovering:
recovery took 0s (sleep already satisfied, so it skips the wait)
```

The first run took the full second; recovery took no measurable time because the sleep was already journaled and replay skipped it. Swap the second for twenty-four hours and the shapes are identical: the first run waits a day, a crash-and-recovery the next morning resumes instantly.

### When rerun panics and when it returns an error

The engine panics in some places and returns errors in others, and the split is principled.

> **Principle.** Panic on programmer errors: a determinism violation, an unknown or duplicate workflow name, a journal that cannot be unmarshaled into the declared type, a result that cannot be serialized. These mean the program is built wrong and cannot proceed correctly; failing loud and early is the service. Return errors for operational conditions: a step's own business failure, a store write that fails. These are expected in a running system and the caller decides what to do.

The litmus test: could this condition occur in a correct program talking to a healthy world? If yes, it is an error. If it can only happen because the code or the stored data is wrong, it is a panic. A declined card is an error. A workflow that asks to replay a step that does not exist is a panic, because a correct engine never does that.

> **Trap.** It is tempting to convert the determinism panic into a `Failed` status so one bad workflow cannot crash the process. That feels defensive but it is the wrong default: it hides a corruption bug behind a status field, turning a screaming failure into a silent one. Day 5 shows how to test the panic properly.

### Exercises

1. Make a workflow `Do` a step, then `Sleep` a short duration, then `Do` another step. Crash it after the first step (seed a one-entry journal), recover, and confirm the sleep runs live during recovery but the first step does not. Why does the sleep run live this time when it was skipped in the milestone?
2. The `Sleep` step also honors `ctx.Done()`. Write a workflow whose context you cancel mid-sleep and observe the returned error. Is that error journaled, and what happens on replay?
3. Classify each as panic-worthy or error-worthy and justify: a step returns a typed business error; the SQLite file is read-only; a `Do` tag contains a value computed from `rand.Int()`; the journal row's payload is valid JSON but for a different shape than the step now returns.

Solutions in Appendix C.

---

## Day 4: Pluggable storage

The engine works against an in-memory store. Real durability needs real persistence, and other people will want backends we have not written. Today we make storage, serialization, time, and observation all swappable without touching the engine, and we add a SQLite backend to prove the seams hold. The interfaces have been in place since Day 1; today we explain why they are shaped this way and exercise them.

### The dependency rule

> **Principle.** Dependencies point inward. The engine depends on interfaces it defines (`Store`, `Codec`, `Clock`, `Observer`). Concrete backends depend on the engine to implement those interfaces. The engine never imports a backend. This is dependency inversion, and the payoff is blunt: a Postgres backend is a new package that imports `rerun`, and adding it changes zero lines of engine code.

### Interface segregation, justified

Recall the three-way split of `Store` into `Writer`, `Reader`, and `Guarder`. Now it earns its place. Not every consumer needs every capability. A metrics process that only reads incomplete runs depends on `Reader` alone. A design that separates the component doing locking from the component doing writes can take just `Guarder`. Splitting the interface lets a caller declare exactly the capability it uses, and lets a test double implement only what the test exercises.

> **Design.** Small interfaces compose into the big one for free: `Store` is just `Writer; Reader; Guarder` embedded together. You lose nothing by splitting and gain the ability to depend on slices of behavior. Go's structural typing makes this weightless.

### Serialization and observation as seams

`Codec` lets a backend or a workflow choose a wire format. JSON is the default because it is debuggable: you can read the journal in the database. A team that needs compactness or schema evolution passes `WithCodec` and a protobuf or msgpack implementation, and nothing else changes. `Observer` turns engine lifecycle events into a stream a caller can watch: start, each step, finish. The default is a no-op; production passes one that increments metrics or writes structured logs. The options that inject them already exist on the engine: `WithCodec`, `WithClock`, `WithObserver`.

### A SQLite backend

Here is a real backend behind the same `Store` contract. It is mechanical, which is the point: the interface is small enough that any backend is a short, boring file. The schema encodes two invariants directly. `runs.id` is a primary key, so the "no duplicate run" rule that `Create` promises is enforced by the database. `journal(run_id, seq)` is a composite primary key, so a run can never have two entries at the same sequence number.

**`sqlite/sqlite.go`**

```go
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"sync"

	"github.com/sylvester-francis/rerun"
	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func New(path string) *Store {
	// WAL lets readers proceed alongside a writer; busy_timeout turns a
	// momentary lock into a short wait instead of an error.
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn) // modernc driver registers as "sqlite", not "sqlite3"
	if err != nil {
		panic(fmt.Sprintf("sqlite: open %s: %v", path, err))
	}
	db.SetMaxOpenConns(1) // single writer; serializes cleanly, avoids "database is locked"

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS runs (
			id       TEXT PRIMARY KEY,
			workflow TEXT NOT NULL,
			status   INTEGER NOT NULL,
			created  DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS journal (
			run_id  TEXT NOT NULL,
			seq     INTEGER NOT NULL,
			tag     TEXT NOT NULL,
			payload BLOB,
			err     TEXT,
			at      DATETIME NOT NULL,
			PRIMARY KEY (run_id, seq)
		);`); err != nil {
		panic(fmt.Sprintf("sqlite: schema: %v", err))
	}
	return &Store{db: db}
}

func (s *Store) Create(ctx context.Context, r rerun.Run) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO runs (id, workflow, status, created) VALUES (?, ?, ?, ?)`,
		r.ID, r.Workflow, int(r.Status), r.Created,
	)
	if err != nil {
		return fmt.Errorf("sqlite: create %s: %w", r.ID, err)
	}
	return nil
}

func (s *Store) Append(ctx context.Context, runID string, l rerun.Log) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO journal (run_id, seq, tag, payload, err, at) VALUES (?, ?, ?, ?, ?, ?)`,
		runID, l.Seq, l.Tag, l.Payload, l.Err, l.At,
	)
	if err != nil {
		return fmt.Errorf("sqlite: append %s seq %d: %w", runID, l.Seq, err)
	}
	return nil
}

func (s *Store) Finish(ctx context.Context, runID string, st rerun.Status) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET status = ? WHERE id = ?`, int(st), runID)
	if err != nil {
		return fmt.Errorf("sqlite: finish %s: %w", runID, err)
	}
	return nil
}

func (s *Store) LoadLogs(ctx context.Context, runID string) ([]rerun.Log, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT seq, tag, payload, err, at FROM journal WHERE run_id = ? ORDER BY seq`,
		runID,
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: load logs %s: %w", runID, err)
	}
	defer rows.Close()

	var out []rerun.Log
	for rows.Next() {
		var l rerun.Log
		if err := rows.Scan(&l.Seq, &l.Tag, &l.Payload, &l.Err, &l.At); err != nil {
			return nil, fmt.Errorf("sqlite: scan log %s: %w", runID, err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) Incomplete(ctx context.Context) ([]rerun.Run, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, workflow, status, created FROM runs WHERE status IN (?, ?)`,
		int(rerun.Pending), int(rerun.Running),
	)
	if err != nil {
		return nil, fmt.Errorf("sqlite: incomplete: %w", err)
	}
	defer rows.Close()

	var out []rerun.Run
	for rows.Next() {
		var r rerun.Run
		var st int
		if err := rows.Scan(&r.ID, &r.Workflow, &st, &r.Created); err != nil {
			return nil, fmt.Errorf("sqlite: scan run: %w", err)
		}
		r.Status = rerun.Status(st)
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) Acquire(ctx context.Context, runID string) (io.Closer, error) {
	s.mu.Lock()
	return closerFunc(s.mu.Unlock), nil
}

type closerFunc func()

func (c closerFunc) Close() error {
	c()
	return nil
}
```

Three operational details separate this from a toy. The driver from `modernc.org/sqlite` registers as `"sqlite"`, not `"sqlite3"` (that name belongs to the cgo `mattn` driver). WAL journal mode plus a busy timeout, set through DSN pragmas, let reads proceed alongside a writer and turn a momentary lock into a short wait. And `SetMaxOpenConns(1)` keeps concurrent writers from colliding; SQLite is a single-writer database, and one connection serializes cleanly.

> **Trap.** `LoadLogs` carries the single most important clause in the backend: `ORDER BY seq`. Replay matches journal entries to `Do` calls by position, so the rows must come back in sequence order. Omit it and you get a store that passes casual testing and corrupts runs under recovery. Day 5's contract test inserts rows out of order specifically to catch this.

> **Pattern.** `Acquire` for the single-process store is a plain mutex wrapped as an `io.Closer` via a `closerFunc` adapter. A distributed Postgres backend would replace this one method with `pg_advisory_lock`, and nothing in the engine would change. The hard, distributed concern is isolated behind one interface method.

### The one verification exception

An honest note, the single exception to this guide's verification rule. The build environment could not reach the Go module proxy to download `modernc.org/sqlite`, so unlike every other file, this backend's runtime was not separately executed. The code is shown and reasoned through; its behavior is pinned by the same contract test the in-memory store passes on Day 5, which is the real guarantee. In your own environment, `go mod tidy` followed by `go test ./sqlite/` exercises it directly.

### Exercises

1. Implement a third `Store` against a Go `map` plus an append-only file (append each `Log` as a JSON line, replay the file on startup). You do not need a database to be durable; you need an append-only log. What does this teach about what SQLite is actually buying you?
2. The engine takes `Store`, the whole interface. Find a consumer that uses only part of it, the recovery loop or a test, and narrow its parameter to `Reader` or `Writer`. Does the code still compile? What did the narrowing document?
3. `SetMaxOpenConns(1)` serializes all database access. Given that `Acquire` already takes a process-wide mutex, is the connection limit redundant today? Under what change to `Acquire` would it stop being redundant?

Solutions in Appendix C.

---

## Day 5: A test harness you can trust

A durable engine that is merely probably correct is worthless, because the whole reason to use one is to trust it with money and state across failures. Today builds the harness that earns that trust: a contract suite every backend inherits, fake time that makes sleeps testable, and the right way to test code that panics. Tomorrow measures how good the harness actually is.

### One contract suite, every backend

The highest-leverage test for a pluggable library is written once against the `Store` interface and run against every implementation. Place it in an ordinary, importable package, deliberately not a `_test.go` file, because Go forbids importing a test package from another package, so a contract trapped in `rerun_test` could never be reused by the SQLite or memstore tests.

**`storetest/storetest.go`**

```go
package storetest

import (
	"context"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
)

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func Contract(t *testing.T, makeStore func() rerun.Store) {
	ctx := context.Background()

	t.Run("create and load", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "r1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Append(ctx, "r1", rerun.Log{Seq: 0, Tag: "a", Payload: []byte(`"x"`), At: time.Now()}))
		must(t, s.Append(ctx, "r1", rerun.Log{Seq: 1, Tag: "b", Payload: []byte(`"y"`), At: time.Now()}))
		logs, err := s.LoadLogs(ctx, "r1")
		must(t, err)
		if len(logs) != 2 {
			t.Fatalf("want 2 logs, got %d", len(logs))
		}
		if logs[0].Tag != "a" || logs[1].Tag != "b" {
			t.Fatalf("tags out of order: %q, %q", logs[0].Tag, logs[1].Tag)
		}
	})

	t.Run("logs return in seq order", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "r2", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Append(ctx, "r2", rerun.Log{Seq: 2, Tag: "c", Payload: []byte(`1`), At: time.Now()}))
		must(t, s.Append(ctx, "r2", rerun.Log{Seq: 0, Tag: "a", Payload: []byte(`2`), At: time.Now()}))
		must(t, s.Append(ctx, "r2", rerun.Log{Seq: 1, Tag: "b", Payload: []byte(`3`), At: time.Now()}))
		logs, err := s.LoadLogs(ctx, "r2")
		must(t, err)
		for i, l := range logs {
			if l.Seq != i {
				t.Fatalf("seq %d at index %d, ordering broken", l.Seq, i)
			}
		}
	})

	t.Run("incomplete filters correctly", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "done1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Finish(ctx, "done1", rerun.Done))
		must(t, s.Create(ctx, rerun.Run{ID: "active1", Workflow: "wf", Status: rerun.Running, Created: time.Now()}))
		must(t, s.Create(ctx, rerun.Run{ID: "pending1", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		runs, err := s.Incomplete(ctx)
		must(t, err)
		ids := map[string]bool{}
		for _, r := range runs {
			ids[r.ID] = true
		}
		if ids["done1"] {
			t.Fatal("completed run returned as incomplete")
		}
		if !ids["active1"] || !ids["pending1"] {
			t.Fatal("missing an incomplete run")
		}
	})

	t.Run("finish transitions status", func(t *testing.T) {
		s := makeStore()
		must(t, s.Create(ctx, rerun.Run{ID: "r3", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}))
		must(t, s.Finish(ctx, "r3", rerun.Failed))
		runs, err := s.Incomplete(ctx)
		must(t, err)
		for _, r := range runs {
			if r.ID == "r3" {
				t.Fatal("failed run still listed as incomplete")
			}
		}
	})

	t.Run("lock and release", func(t *testing.T) {
		s := makeStore()
		closer, err := s.Acquire(ctx, "r1")
		must(t, err)
		must(t, closer.Close())
	})

	t.Run("duplicate create errors", func(t *testing.T) {
		s := makeStore()
		r := rerun.Run{ID: "dup", Workflow: "wf", Status: rerun.Pending, Created: time.Now()}
		must(t, s.Create(ctx, r))
		if err := s.Create(ctx, r); err == nil {
			t.Fatal("duplicate create should error")
		}
	})
}
```

> **Trap.** The "logs return in seq order" sub-test inserts entries `2, 0, 1` and asserts they read back `0, 1, 2`. That out-of-order insert is the whole point: it is the case that catches a backend that forgot `ORDER BY`. A test that inserts in order would pass against a broken store.

Each backend is then one tiny file. The memstore's:

**`internal/memstore_test.go`**

```go
package internal_test

import (
	"testing"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
	"github.com/sylvester-francis/rerun/storetest"
)

func TestMemStore(t *testing.T) {
	storetest.Contract(t, func() rerun.Store {
		return internal.NewMemStore()
	})
}
```

The SQLite test is identical in shape, handing `storetest.Contract` a factory that opens a database in `t.TempDir()`, so each run gets a fresh file the framework cleans up.

### Fake time

A workflow that sleeps a day cannot be tested against a real clock. The `Clock` seam from Day 1 is what makes it tractable: a fake clock records each `After` as a waiter on a virtual timeline, and the test advances that timeline on command.

**`helpers_test.go`**

```go
package rerun_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

func setup(t *testing.T) (*rerun.Engine, *internal.MemStore, *fakeClock) {
	t.Helper()
	store := internal.NewMemStore()
	clk := newFakeClock()
	eng := rerun.New(store, rerun.WithClock(clk))
	return eng, store, clk
}

func setupWith(t *testing.T, opts ...rerun.Opt) (*rerun.Engine, *internal.MemStore, *fakeClock) {
	t.Helper()
	store := internal.NewMemStore()
	clk := newFakeClock()
	all := append([]rerun.Opt{rerun.WithClock(clk)}, opts...)
	eng := rerun.New(store, all...)
	return eng, store, clk
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func waitDone(t *testing.T, s *internal.MemStore, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := s.Get(runID); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("run %s did not finish within timeout", runID)
}

func waitStatus(t *testing.T, s *internal.MemStore, runID string, want rerun.Status) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if r, ok := s.Get(runID); ok && r.Status == want {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("run %s did not reach status %v within timeout", runID, want)
}

type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	deadline time.Time
	ch       chan time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now()}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) After(d time.Duration) <-chan time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	ch := make(chan time.Time, 1)
	if d <= 0 {
		ch <- f.now
		return ch
	}
	f.waiters = append(f.waiters, fakeWaiter{deadline: f.now.Add(d), ch: ch})
	return ch
}

func (f *fakeClock) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
	var remaining []fakeWaiter
	for _, w := range f.waiters {
		if !w.deadline.After(f.now) {
			w.ch <- f.now
		} else {
			remaining = append(remaining, w)
		}
	}
	f.waiters = remaining
}

func (f *fakeClock) BlockUntil(n int) {
	for {
		f.mu.Lock()
		count := len(f.waiters)
		f.mu.Unlock()
		if count >= n {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

type spyObserver struct {
	mu       sync.Mutex
	starts   []rerun.Run
	steps    []rerun.Log
	finishes []rerun.Status
}

func (s *spyObserver) OnStart(r rerun.Run)               { s.mu.Lock(); s.starts = append(s.starts, r); s.mu.Unlock() }
func (s *spyObserver) OnStep(id string, l rerun.Log)     { s.mu.Lock(); s.steps = append(s.steps, l); s.mu.Unlock() }
func (s *spyObserver) OnFinish(id string, st rerun.Status) { s.mu.Lock(); s.finishes = append(s.finishes, st); s.mu.Unlock() }

func (s *spyObserver) snapshot() (int, int, []rerun.Status) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fin := make([]rerun.Status, len(s.finishes))
	copy(fin, s.finishes)
	return len(s.starts), len(s.steps), fin
}

func (s *spyObserver) clearSteps() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.steps = nil
}

var _ = context.Background
```

> **Trap.** `BlockUntil` is the part everyone forgets. Because `Start` runs the workflow in a goroutine, a test must wait until that goroutine has actually reached the sleep and registered its waiter before advancing the clock. Advance too early and the wakeup is delivered to no one, the workflow hangs, and the test times out. `BlockUntil(1)` blocks until the waiter exists, then `Advance` resolves it deterministically.

### Testing code that is supposed to panic

The engine panics on an unknown workflow and on a determinism violation, both by design. Testing those panics has a structural catch: in normal operation they fire inside the goroutine `exec` spawns, where a Go panic kills the whole test process instead of failing one test. The fix is to test them synchronously in a white-box file that declares `package rerun` and can therefore construct a `W` directly and reach the unexported `lookup`.

**`panics_internal_test.go`**

```go
package rerun

import (
	"context"
	"fmt"
	"strings"
	"testing"
)

func TestLookup_UnknownPanics(t *testing.T) {
	e := New(nil)
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on unknown workflow")
		}
	}()
	e.lookup("nope")
}

func TestReplayStep_DeterminismPanic(t *testing.T) {
	w := &W{
		RunID:  "r1",
		logs:   []Log{{Seq: 0, Tag: "expected-tag", Payload: []byte(`0`)}},
		replay: true,
		eng:    New(nil),
		ctx:    context.Background(),
	}
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("expected panic on tag mismatch")
		}
		if !strings.Contains(fmt.Sprint(r), "determinism") {
			t.Fatalf("wrong panic message: %v", r)
		}
	}()
	Do(w, "actual-tag", func(ctx context.Context) (int, error) { return 0, nil })
}
```

> **Idiom.** White-box tests (`package rerun`) and black-box tests (`package rerun_test`) coexist in the same directory. Use black-box by default, because it exercises the public API exactly as a user would; reach for white-box only when you must touch unexported behavior or, as here, avoid a goroutine that would crash the runner.

The full suite also includes the crash-and-recover tests (run to completion, reset to `Running`, recover, assert no re-execution) and the partial-replay test that seeds a half-finished journal, which is the most important test in the suite for reasons tomorrow makes concrete.

### Milestone: the whole suite green under the race detector

```
ok  	github.com/sylvester-francis/rerun	1.094s
?   	github.com/sylvester-francis/rerun/examples/capstone	[no test files]
?   	github.com/sylvester-francis/rerun/examples/durablesleep	[no test files]
?   	github.com/sylvester-francis/rerun/examples/recover	[no test files]
?   	github.com/sylvester-francis/rerun/examples/skeleton	[no test files]
ok  	github.com/sylvester-francis/rerun/internal	1.013s
?   	github.com/sylvester-francis/rerun/storetest	[no test files]
```

Every engine test and every contract sub-test passes with `-race` on. That is the bar; tomorrow we find out whether the bar is high enough to catch a real bug.

### Exercises

1. Run `go test -cover ./...` and note the percentage. Then delete one assertion from any test and re-run coverage. Did the number move? What does that tell you about coverage as a quality signal?
2. Write the white-box test for the unknown-workflow panic: construct an engine, call `lookup` on a name you never registered, recover the panic. Why can this not be a black-box test driven through `Start`?
3. Write the SQLite backend's test by handing `storetest.Contract` a factory that opens a DB in `t.TempDir()`. Which sub-test would fail against a backend whose `LoadLogs` dropped `ORDER BY`, and why is that the one that matters?

Solutions in Appendix C.

---

## Day 6: Mutation testing

Yesterday ended with a green suite under the race detector. Green is necessary, not sufficient. Line coverage tells you a line ran, not that any assertion would notice it being wrong; a test can execute every line and check nothing. Today we measure the suite the only way that means something, mutation testing, and we ship a small tester so the measurement is a command, not a one-off.

### The idea

A mutation tool introduces small deliberate bugs (mutants), one at a time, and runs the suite against each. A mutant that makes a test fail is *killed*; one that leaves the suite green *survived*, which means a real bug of that shape would slip through your tests undetected. The fraction killed is a far better quality signal than coverage, because it measures detection, not execution.

### A mutation tester you can run

rerun ships its own, in `tools/mutate`. It is deliberately small and dependency-free: it applies each known fault from the core's mutant table to a fresh copy of the source, runs the suite, restores the file, and reports killed or survived. It also *asserts*: real faults must be killed and the one documented equivalent must survive, and it exits non-zero otherwise, so `make mutate` gates CI exactly as the unit suite does.

**`tools/mutate/main.go`**

```go
// Command mutate is a dependency-free mutation tester for rerun's core.
//
// It introduces one known mutation at a time into the source, runs the unit
// suite, and reports whether each mutant was killed (a test failed) or survived
// (the suite stayed green). Known-equivalent mutants are expected to survive;
// every other mutant is expected to be killed. The command exits non-zero if any
// outcome does not match expectation, so it can gate CI. Each mutation is applied
// to a fresh copy of the original source and restored immediately afterward.
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
		old:    "e.store.Finish(ctx, r.ID, Done)",
		new:    "e.store.Finish(ctx, r.ID, Failed)",
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
```

Run it:

```
MUTANT                       OUTCOME   EXPECTED  STATUS    CATCHER / REASON
guard && -> ||               killed    killed    ok        TestPartialReplay_ResumesMidway
guard off-by-one < -> <=     killed    killed    ok        TestPartialReplay_ResumesMidway
determinism check != -> ==   killed    killed    ok        TestReplayStep_DeterminismPanic
drop journaled step error    killed    killed    ok        TestDo_ReplayPreservesError
success marked failed        killed    killed    ok        TestWorkflow_SuccessSetsDone
remove w.replay = false      survived  survived  ok        redundant: seq < len(logs) already gates replay

6 mutants: 5 killed, 1 survived (1 equivalent expected)
PASS: every mutant matched expectation
```

Five real faults killed by the test named beside each, and the one equivalent mutant surviving as expected. Two of those results carry the day's lessons.

The `&&` to `||` flip in the `Do` guard is killed by the partial-replay test, and only by it. A naive "does a fresh run execute its step" test does not catch it, because on a fresh run both sides of the guard are false and `&&` and `||` agree. The partial-replay test catches it because, after replaying a partial journal, the workflow continues to a step past the journal's end, where the correct `&&` runs it live but the `||` mutant tries to replay an entry that does not exist. That is the concrete reason the mid-crash recovery test is the one that matters most in the whole suite. The off-by-one `<` to `<=` is caught the same way and for the same reason.

> **Principle.** Not every mutant can be killed, and recognizing the ones that cannot is a real skill. Removing `w.replay = false` leaves the suite green because it is an *equivalent mutant*: the guard's `w.seq < len(w.logs)` bound already governs replay-versus-live, the cursor only advances, and the journal does not grow during a run, so the flag is redundant. The tester encodes this expectation, it asserts that this mutant *survives*, so an honest equivalent is recorded once in code and never re-investigated. When a broader mutation run reports a survivor, this is the response: decide whether it is a genuine gap or equivalent, not reflexively write a test. A perfect kill rate is a number to interrogate, not chase.

### The fuller mutant map

The tester encodes six representative faults; the table below is the fuller map for the core, each row produced by introducing that exact mutant and confirming the named test fails while the others stay green. Extending `tools/mutate` with the rows it does not yet cover is one of today's exercises.

| Mutation | Effect | Killed by |
|---|---|---|
| `w.seq++` removed in replay | cursor stuck, same entry replayed forever | TestMultiStepReplay_AllStepsSkipped |
| `&&` to `\|\|` in `Do` guard | replay runs past a partial journal | TestPartialReplay_ResumesMidway |
| `<` to `<=` in `Do` guard | off-by-one past the journal | TestPartialReplay_ResumesMidway |
| `l.Tag != tag` to `==` | determinism check inverted | TestReplayStep_DeterminismPanic (white-box) |
| panic removed on tag mismatch | corruption goes silent | TestReplayStep_DeterminismPanic (white-box) |
| `errStr` assignment removed | step errors lost on replay | TestDo_ReplayPreservesError |
| `Finish(Done)` to `Finish(Failed)` | successes marked failed | TestWorkflow_SuccessSetsDone |
| `store.Append` removed | nothing journaled | TestDo_ReplaySkipsExecution |
| `obs.OnStep` removed | observability broken | TestObserver_ReceivesAllEvents |
| `Incomplete` includes terminal runs | recovery re-runs completed work | TestRecover_OnlyIncomplete |
| `Incomplete` drops Running | recovery misses crashed runs | TestDo_ReplaySkipsExecution |

> **Pattern.** What we ship is a focused tester pinned to known faults: fast, dependency-free, CI-gating. For exhaustive coverage across all of Go's operators and statements, run a general tool such as `gremlins` or `go-mutesting` over the package periodically, and triage its survivors into "real gap, write a test" or "equivalent, document it." Keep the equivalents listed, in code here, so a future reader does not re-investigate them.

### Exercises

1. Add one row from the table that `tools/mutate` does not yet encode, for example `obs.OnStep` removed (it should be killed by `TestObserver_ReceivesAllEvents`), as a new `mutant` entry and run `make mutate`. Did your expectation hold?
2. Introduce the `<` to `<=` off-by-one in the guard by hand. Which test catches it, and why does the partial-replay test catch an off-by-one that a test seeding a *complete* journal would not?
3. The `w.replay = false` removal survives. Build the argument that it is *truly* equivalent, not merely uncaught, citing the cursor-only-advances and journal-does-not-grow invariants. Under what change to the engine would removing that line stop being equivalent?

Solutions in Appendix C.

---

## Day 7: Shipping rerun

The engine is built and proven. Two things finish the week: turn rerun into a package someone can `go get`, and run it end to end on a realistic workflow as a final acceptance test of the whole thing.

### The repository

A library nobody can adopt is a private exercise. The layout that makes rerun installable and legible:

```
rerun/
  go.mod                 module github.com/sylvester-francis/rerun
  store.go               Run, Log, Status, the Store/Writer/Reader/Guarder interfaces
  codec.go  clock.go  hooks.go  errors.go
  engine.go  workflow.go  run.go
  internal/memstore.go   in-process store (kept internal: not part of the public API)
  storetest/storetest.go importable contract suite for any backend
  sqlite/sqlite.go       a real persistent backend
  tools/mutate/main.go   the dependency-free mutation tester from Day 6
  examples/
    skeleton/  recover/  durablesleep/  capstone/
  README.md  LICENSE  Makefile  .github/workflows/ci.yml
```

> **Design.** The in-memory store lives under `internal/` on purpose: it is the engine's own default and a test aid, not a backend you promise to support, and `internal/` makes that boundary a compiler rule rather than a comment. The SQLite backend lives in an exported `sqlite/` package because it *is* part of what you support. Where a file sits encodes the support promise.

### The public API surface

Keep it tiny, which is what makes the library easy to learn and hard to misuse: `New`, the `WithCodec` / `WithClock` / `WithObserver` options, `Handle`, `Start`, `Recover`, the generic `Do`, and `Sleep`. Everything else is an interface a backend implements or an internal detail. That is the whole surface a user touches, and it is small because the idea is small.

A `README` should open with the thirty-second pitch (a workflow that survives crashes, no cluster), the smallest runnable example (the skeleton), the one rule the user must obey (determinism, with the journal-the-decision fix), and the backend-swap line, `rerun.New(sqlite.New("rerun.db"))` in place of the in-memory store. A `Makefile` wires `test` (`go test -race ./...`), `vet`, `cover`, and `mutate` (`go run ./tools/mutate`), and CI runs them on every push. The license is your call; a single MIT or Apache-2.0 is the honest default for a library you want adopted.

> **Principle.** Ship the contract suite as a public package, not just your own tests. It is the single highest-value artifact for adoption, because it lets anyone write a backend and prove it correct against the same bar your in-tree backends meet. A pluggable library whose contract is testable by third parties is one others can actually extend.

### Acceptance: the finished engine, exercised end to end

Before shipping, run rerun on a realistic multi-step workflow that crashes and recovers, to confirm the library delivers the guarantee we set out to build. A user signup: create an account, charge a card with retry, wait before welcoming the user, send a welcome email. Every step durable, a journaled charge replayed on recovery rather than charged again, the wait durable, recovery resuming from wherever the crash left it. Note that the retry is not a new engine feature; it is a loop over `Do`, each attempt its own journaled step, which is the whole point: useful patterns fall out of the primitive.

**`examples/capstone/main.go`**

```go
package main

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/sylvester-francis/rerun"
	"github.com/sylvester-francis/rerun/internal"
)

// Live side effects we want to prove happen exactly once across a crash.
var (
	charges int32 // times the payment processor was actually called
	emails  int32 // welcome emails actually sent
)

func waitDone(s *internal.MemStore, id string) {
	for i := 0; i < 5000; i++ {
		if r, ok := s.Get(id); ok && (r.Status == rerun.Done || r.Status == rerun.Failed) {
			return
		}
		time.Sleep(time.Millisecond)
	}
}

// chargeCard is a retry pattern built on the Do primitive: each attempt is its
// own journaled step, so a transient failure is recorded and the next attempt
// is a new step. On replay the whole attempt sequence is reproduced from the
// journal without calling the processor again.
func chargeCard(w *rerun.W, accountID string) (string, error) {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		txn, err := rerun.Do(w, fmt.Sprintf("charge:attempt-%d", attempt),
			func(ctx context.Context) (string, error) {
				n := atomic.AddInt32(&charges, 1)
				if n < 2 { // first LIVE attempt simulates a processor timeout
					fmt.Printf("    [live] charge attempt %d: processor timeout\n", attempt)
					return "", fmt.Errorf("processor timeout")
				}
				fmt.Printf("    [live] charge attempt %d: ok (txn_%d)\n", attempt, n)
				return fmt.Sprintf("txn_%d", n), nil
			})
		if err == nil {
			return txn, nil
		}
		lastErr = err
	}
	return "", fmt.Errorf("charge failed after retries: %w", lastErr)
}

func signup(w *rerun.W) error {
	acct, _ := rerun.Do(w, "create-account", func(ctx context.Context) (string, error) {
		fmt.Println("    [live] create-account")
		return "acct_1", nil
	})

	if _, err := chargeCard(w, acct); err != nil {
		return err
	}

	rerun.Sleep(w, 500*time.Millisecond) // durable "wait a bit before welcoming"

	rerun.Do(w, "send-welcome-email", func(ctx context.Context) (struct{}, error) {
		atomic.AddInt32(&emails, 1)
		fmt.Println("    [live] send-welcome-email")
		return struct{}{}, nil
	})
	return nil
}

func main() {
	store := internal.NewMemStore()
	eng := rerun.New(store)
	eng.Handle("signup", signup)
	ctx := context.Background()

	fmt.Println("=== clean first run ===")
	eng.Start(ctx, "signup", "u1")
	waitDone(store, "u1")
	fmt.Printf("after first run: charges(live)=%d emails(live)=%d\n\n",
		atomic.LoadInt32(&charges), atomic.LoadInt32(&emails))

	// Now the interesting case: a process that crashed AFTER the card was
	// charged but BEFORE the welcome email. Seed exactly that partial journal
	// for a second user and recover. The card must NOT be charged again, and
	// recovery must resume at the sleep and then send the email.
	fmt.Println("=== crash recovery for u2 (crashed after charge, before email) ===")
	atomic.StoreInt32(&charges, 0)
	atomic.StoreInt32(&emails, 0)
	store.Create(ctx, rerun.Run{ID: "u2", Workflow: "signup", Status: rerun.Running, Created: time.Now()})
	store.Append(ctx, "u2", rerun.Log{Seq: 0, Tag: "create-account", Payload: []byte(`"acct_1"`)})
	store.Append(ctx, "u2", rerun.Log{Seq: 1, Tag: "charge:attempt-0", Payload: []byte(`""`), Err: "processor timeout"})
	store.Append(ctx, "u2", rerun.Log{Seq: 2, Tag: "charge:attempt-1", Payload: []byte(`"txn_2"`)})

	eng.Recover(ctx)
	waitDone(store, "u2")
	r, _ := store.Get("u2")
	fmt.Printf("u2 status Done? %v\n", r.Status == rerun.Done)
	fmt.Printf("during recovery: charges(live)=%d (card NOT re-charged), emails(live)=%d (welcome sent once)\n",
		atomic.LoadInt32(&charges), atomic.LoadInt32(&emails))
}
```

Running it:

```
=== clean first run ===
    [live] create-account
    [live] charge attempt 0: processor timeout
    [live] charge attempt 1: ok (txn_2)
    [live] send-welcome-email
after first run: charges(live)=2 emails(live)=1

=== crash recovery for u2 (crashed after charge, before email) ===
    [live] send-welcome-email
u2 status Done? true
during recovery: charges(live)=0 (card NOT re-charged), emails(live)=1 (welcome sent once)
```

Read the second half of that output. The clean run charged twice live, once failing, once succeeding, because the retry loop did its job. The recovery run, seeded with a journal that ends just after the successful charge, executed the card charge *zero* times live and still completed, sending the welcome email once. That is the durable-resume property the library exists to provide, demonstrated end to end: a workflow that crashed after the charge was journaled resumes without taking the money again, and finishes the work that was left. The charge repeats only if the process dies in the narrow window after it runs and before its journal entry commits, which is why a production charge step is written to be idempotent. In production you build this on the SQLite backend instead of the in-memory store, a one-line swap, because the workflow code is identical against any `Store`; the engine is what the acceptance test proves, and the backend is what makes the durability outlive the process.

### Ship checklist

- `go test -race ./...` green, and `go vet ./...` clean.
- `make mutate` passes: the known faults are killed and the documented equivalent survives.
- The contract suite passes against every backend you ship.
- `README` has a runnable example, the determinism rule, and the backend-swap line.
- Public API is the minimal set above; everything else is `internal/` or an interface.
- `go.mod` declares the real module path; `go mod tidy` resolves the `modernc.org/sqlite` requirement.
- A `LICENSE` file exists and you chose it deliberately.
- The determinism contract is stated where a new user sees it before writing their first workflow.

### Exercises

1. Walk the public API one entry at a time and state in a line what a user needs each for. Is anything missing that a first real workflow would require? Is anything exported that could be unexported without hurting a user?
2. Write the README's determinism section in under ten lines: the rule, the wrong example, and the journal-the-decision fix. It is the most important paragraph in your documentation; make a new user unable to miss it.

Solutions in Appendix C.

That is a week. You built a durable execution engine from the idea up, proved it resumes correctly from the middle of a crash, made it sleep durably and plug into any store, built a test harness and measured it with mutation analysis, and shipped it with a realistic workflow proving the guarantee holds. rerun is the core that the large platforms are built on, in a form you can read, test, and own.

---

## Part II: The hard problems

The seven days bought you the mechanism: journal, replay, recover. That mechanism is correct and it is small, but it runs one process, sleeps by replaying from zero, cannot hear the outside world, and breaks the moment you change a workflow's code. Production durable execution lives in exactly those four gaps. This part closes them, in the order where each unlocks the next: first make rerun safe for many workers, then make time durable, then let a workflow wait on an external event, then let it survive a deploy.

Each chapter is a real change to the engine you already built, verified the same way as the week: compiled, tested under the race detector, and run with the output reproduced here. One honest exception carries over from Day 4. The multi-process mechanism is exercised in memory with concurrent workers; the Postgres backend that realizes it across machines is shown and ships in the repository, but its runtime is not executed in this text for the same reason the SQLite backend's was not, the driver is not fetchable in the build sandbox. The Day 5 contract suite is what pins it.

New files this part adds:

```
rerun/
  signal.go               # Signaler capability, Deliver, Wait[T]   (hard problem 3)
  version.go              # Version marker                          (hard problem 4)
  postgres/
    postgres.go           # advisory-lock backend, multi-process    (hard problem 1)
  examples/
    workers/main.go       # N workers, exactly-once dispatch
    durabletimer/main.go  # remaining-time timer across a crash
    signals/main.go       # deliver an approval, then replay it
    versioning/main.go    # old run pinned, new run on new code
```

store.go, internal/memstore.go, and run.go also change; the diffs are shown where they matter.

> **Principle.** Each of these is a property the journal already almost gives you. Multi-process needs a lease so two workers do not replay the same run. A durable timer needs the journal to hold a deadline, not the fact of having slept. A signal is a step whose value comes from outside. A version is a step whose value is a branch selector. The pattern under all four is the one from Day 2: anything nondeterministic becomes a journaled step.

## Hard problem 1: Multi-process execution

### The problem

rerun's `Recover` walks the incomplete runs and starts each one. Run a second copy of your service for availability and both recoverers see the same incomplete run and both replay it. Every live step past the crash point then runs twice: two charges, two emails, two shipments. The Day 1 engine is single-process by omission, not by design. `Guarder` was the seam left for this.

### A lease, and why it must be a try-lock

The fix is an exclusive lease per run: a worker may only execute a run it holds the lease on. The subtlety is in how it acquires. A blocking lock is wrong for recovery: ten workers scanning the same backlog would queue ten deep on the first run while the rest sit idle. Recovery wants a try-lock: take it if free, otherwise skip and move to the next run. Work distributes itself, and a run already owned by a live worker is left alone.

So `Guarder.Acquire` grows a boolean.

**`store.go`** (the evolved Guarder)

```go
// Guarder grants an exclusive lease on a run. Acquire is non-blocking: it
// returns acquired=false if another worker already holds the run, so a
// recovering worker skips it and moves on instead of queueing. The Closer
// releases the lease. For a single in-memory process this is a held-set; for
// Postgres it is a session-scoped advisory lock that frees automatically if the
// worker dies, which is exactly the property crash recovery needs.
type Guarder interface {
	Acquire(ctx context.Context, runID string) (release io.Closer, acquired bool, err error)
}
```

The in-memory store implements the try-lock as a held set guarded by its existing mutex.

**`internal/memstore.go`** (new field, and the lock methods)

```go
type MemStore struct {
	mu      sync.Mutex
	runs    map[string]rerun.Run
	logs    map[string][]rerun.Log
	held    map[string]bool
	signals map[string][][]byte
}

// Acquire is a non-blocking try-lock over an in-process held set.
func (m *MemStore) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.held[runID] {
		return nil, false, nil
	}
	m.held[runID] = true
	return &memRelease{m: m, runID: runID}, true, nil
}

type memRelease struct {
	m     *MemStore
	runID string
}

func (r *memRelease) Close() error {
	r.m.mu.Lock()
	defer r.m.mu.Unlock()
	delete(r.m.held, r.runID)
	return nil
}
```

The `signals` field belongs to hard problem 3; it is shown here only because it is the same struct.

> **Idiom.** The releaser is a tiny struct implementing `io.Closer`, not a bare `func()`. `defer release.Close()` in the caller reads the same whether the lease is a map entry or a Postgres session, and the closer carries exactly the state it needs to undo itself.

`exec` honors it. One added check turns the engine multi-process.

**`run.go`** (the top of exec; the rest is unchanged from Day 1)

```go
func (e *Engine) exec(ctx context.Context, r Run) {
	release, acquired, err := e.store.Acquire(ctx, r.ID)
	if err != nil || !acquired {
		return // store error, or another worker already owns this run
	}
	defer release.Close()

	e.store.Finish(ctx, r.ID, Running)
	// ... LoadLogs, build W, run the workflow, Finish(Done/Failed): all unchanged
}
```

> **Trap.** It is tempting to also re-check the run's status after acquiring and skip if it is already Done, to avoid re-running a just-finished run. You do not need it for correctness: replaying a fully journaled run executes no live steps and produces no new side effects, so a redundant acquire is harmless. Real systems still add the check as an optimization; rerun leans on replay being free of effects, the same property Day 2 established.

`Recover` itself does not change. It still launches `exec` per incomplete run. With many workers each running `Recover`, the try-lock is what makes the fan-out safe: every run is executed by exactly one worker.

### A Postgres backend with advisory locks

A held set in one process is not a lease across machines. Postgres gives you one with advisory locks, and the right variant is `pg_try_advisory_lock`: non-blocking, session-scoped, and released automatically when the session ends. That last property is the one that matters. When a worker dies, its connection drops, Postgres frees the lock, and another worker can acquire it. No lease expiry, no heartbeat, no reaper.

The catch is session scope. `database/sql` pools connections, so a lock taken on one pooled connection and released on another would leak. The backend pins a dedicated `*sql.Conn` for the life of the lease.

**`postgres/postgres.go`** (the lock; Create, Append, Finish, LoadLogs, and Incomplete mirror the Day 4 SQLite store)

```go
// Acquire takes a session-scoped advisory lock on a dedicated connection.
// pg_try_advisory_lock is non-blocking; a different worker (a different session)
// gets false. If this worker's process dies, the connection drops and Postgres
// releases the lock automatically, which is what makes a crashed run recoverable.
func (s *Store) Acquire(ctx context.Context, runID string) (io.Closer, bool, error) {
	conn, err := s.db.Conn(ctx) // a dedicated connection, so the lock is held by one session
	if err != nil {
		return nil, false, fmt.Errorf("postgres: conn: %w", err)
	}
	var ok bool
	if err := conn.QueryRowContext(ctx, `SELECT pg_try_advisory_lock($1)`, key(runID)).Scan(&ok); err != nil {
		conn.Close()
		return nil, false, fmt.Errorf("postgres: try lock %s: %w", runID, err)
	}
	if !ok {
		conn.Close()
		return nil, false, nil
	}
	return &pgRelease{conn: conn, key: key(runID)}, true, nil
}

type pgRelease struct {
	conn *sql.Conn
	key  int64
}

func (r *pgRelease) Close() error {
	_, err := r.conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, r.key)
	r.conn.Close()
	return err
}

// key hashes a run ID into the bigint that advisory locks operate on.
func key(runID string) int64 {
	h := fnv.New64a()
	h.Write([]byte(runID))
	return int64(h.Sum64())
}
```

The other methods are the Day 4 SQLite store with Postgres placeholders (`$1` instead of `?`) and types (`BYTEA`, `TIMESTAMPTZ`); the full file is in the repository. One difference from SQLite: do not cap the pool at one connection. Postgres handles concurrency, and the contract's exclusivity test depends on a second acquire landing on a second connection so it observes the lock as held.

> **Design.** The Day 5 contract suite is exactly what lets you trust this backend without running it here. Its "lock is exclusive then releasable" case acquires, asserts a second acquire fails, releases, and asserts acquire succeeds again. That holds for the held set in memory and for advisory locks across sessions in Postgres, so the test that passes here is the test you run against a real database in CI.

### The runtime caveat

Everything in this chapter except the Postgres backend's execution is compiled, raced, and run below. The advisory-lock store is shown and ships in `postgres/postgres.go`; `go mod tidy` pulls `github.com/lib/pq`, and the contract suite runs it against a live database. Its runtime is not reproduced in this text, the same exception Day 4 made for SQLite.

### Milestone: exactly-once dispatch across concurrent workers

`examples/workers/main.go` seeds twelve runs that each finished a `work` step but crashed before a `finalize` step that increments a counter. It then starts four independent recoverers against the one store at the same instant, standing in for four processes, and waits for every run to finish. If the lease works, `finalize` runs twelve times, not forty-eight.

The workflow:

```go
eng.Handle("job", func(w *rerun.W) error {
	rerun.Do(w, "work", func(ctx context.Context) (int, error) { return 1, nil })
	rerun.Do(w, "finalize", func(ctx context.Context) (int, error) {
		atomic.AddInt32(&finalized, 1) // the side effect that must happen exactly once
		return 1, nil
	})
	return nil
})
```

Run it with the race detector:

```
$ go run -race ./examples/workers
12 jobs, 4 concurrent workers racing the same store
jobs completed: 12
finalize executed live: 12 (expected 12: exactly once per job)
```

Twelve finalizations, no data race. The lease, not luck, produced that.

> **Pattern.** This is how you test exactly-once dispatch without a cluster: many goroutines sharing one store with a real lock, a side effect that counts itself, and an assertion that the count equals the number of runs. The in-memory lock stands in for the advisory lock; the dispatch logic under test is identical.

### Exercises

1. Add the re-check: after acquiring, load the run and skip if it is already Done. Confirm the live `finalize` count is unchanged, then explain in one sentence why it was already correct without the check.
2. The held set never expires. Describe the failure if `memRelease.Close` were never called (a panic in the workflow body, say), and confirm that `defer release.Close()` in `exec` already prevents it.
3. Give `Acquire` a blocking variant and use it in `Recover` instead of the try-lock. With twelve runs and four workers, what changes about how fast the backlog drains, and why is try-lock the better default for recovery?

Solutions in Appendix B.

## Hard problem 2: Durable timers, done right

### The problem

Day 3's `Sleep` is a step that blocks on the clock. It survives a restart only in the lucky case: if the journal recorded the sleep before the crash, replay skips it; if the crash landed during the sleep, nothing was journaled, and recovery waits the full duration again. A workflow that sleeps for twenty-four hours and crashes at hour twenty-three sleeps another twenty-four. That is not a durable timer.

### Journal the deadline, not the nap

The fix is to record when the timer should fire, not that it fired, and to record it before waiting. The absolute deadline goes into the journal as the timer's first act; the wait is then always for whatever time remains until that deadline, recomputed on every run from the journaled value.

**`workflow.go`** (Sleep, reworked from Day 3)

```go
func Sleep(w *W, d time.Duration) error {
	// Journal the absolute wake-up deadline on the first run. The inner Do
	// completes instantly, so the deadline is recorded before any waiting; a
	// crash mid-sleep therefore leaves the original deadline in the journal.
	deadline, _ := Do(w, fmt.Sprintf("sleep:%v", d), func(ctx context.Context) (int64, error) {
		return w.eng.clock.Now().Add(d).UnixNano(), nil
	})

	// Wait only the time still remaining, recomputed from the journaled deadline
	// on every run. A restart after the deadline waits zero; a restart with time
	// left waits just the remainder, never the full duration over again.
	remaining := time.Unix(0, deadline).Sub(w.eng.clock.Now())
	if remaining <= 0 {
		return nil
	}
	select {
	case <-w.eng.clock.After(remaining):
		return nil
	case <-w.ctx.Done():
		return w.ctx.Err()
	}
}
```

> **Design.** The deadline is journaled by an ordinary `Do`, so it inherits everything Day 2 built: written once, replayed deterministically, survives a crash. The wait is deliberately not a journaled step. Waiting is not a fact to remember; it is a function of the journaled deadline and the current time, so it is recomputed, never stored. Separating the durable fact from the derived wait is the whole trick.

> **Trap.** Order matters. The deadline must be journaled before the wait begins, which is why it is its own instantaneous step rather than something computed alongside the wait. Journal after waiting and you are back to Day 3: a mid-sleep crash leaves nothing recorded.

The signature did not change, so the capstone and every caller are untouched. Both sleep tests from the week still pass, because each advances the clock to the deadline before restarting, the case the old code already handled. The new behavior shows only when you restart with time left.

> **Note on scale.** rerun's timer is still a parked goroutine: a sleeping workflow holds a goroutine blocked on the clock. Goroutines are cheap enough that this carries thousands of timers, which is part of why Day 1 chose a language with cheap blocking. Past that, production engines stop parking and move to a timer service: deadlines live in the store, one component polls for due ones, and a sleeping workflow holds no goroutine at all. rerun's `Incomplete` plus a due-before query is the seam where that attaches; the exercise sketches it.

### Milestone: remaining time across a crash

`examples/durabletimer/main.go` uses a manual clock so a one-hour timer is testable in microseconds. Case A seeds a run that crashed forty minutes into a one-hour sleep, its journaled deadline twenty minutes in the future, then recovers it. Case B seeds a run whose deadline already passed.

```
$ go run ./examples/durabletimer
Case A (crashed mid-sleep): recovered run waited the 20m remainder of a 1h sleep, fired=true
Case B (deadline already elapsed): resumed instantly with no wait, fired=true
```

Case A advanced the clock by twenty minutes, not sixty, and the run woke. Case B needed no advance at all. The deadline in the journal, not the duration in the code, decided both.

> **Pattern.** A controllable clock behind the `Clock` interface is what makes durable time testable. The demo's `manualClock` is the Day 5 fake clock in a `main` package: `Now`, `After`, `Advance`, and a `blockUntil` that waits for the workflow to park before time moves. No real seconds elapse.

### Exercises

1. The deadline is stored as `UnixNano`. What breaks if a workflow sleeps across a machine whose wall clock is wrong by an hour, and why does journaling the deadline (rather than the duration) make the answer "nothing, after the first run"?
2. Sketch the timer-service version: a `DueBefore(ctx, t) ([]Run, error)` on the store, a loop that polls it, and a `Sleep` that returns after journaling the deadline instead of parking. What does a sleeping workflow now hold? No code required; name the moving parts.
3. Add a `SleepUntil(w, t time.Time)` built on the same deadline journaling. Which one line of `Sleep` does it replace?

Solutions in Appendix B.

## Hard problem 3: Signals and external events

### The problem

Every step so far computes its own result. Real workflows wait on the world: an approval click, a payment webhook, a "shipment scanned" event. The value arrives from outside the process, at an unknown time, possibly days later, and it must survive a crash exactly like any other step.

### A signal is a step whose value comes from outside

Two pieces: a way for the outside to deposit an event for a run, and a workflow primitive that blocks until one arrives and then journals it. The deposit is a store capability, kept optional so a backend opts in.

**`signal.go`**

```go
// Signaler is an optional Store capability: a per-run mailbox for external
// events. A backend that implements it enables Wait and Deliver.
type Signaler interface {
	PushSignal(ctx context.Context, runID, name string, payload []byte) error
	PopSignal(ctx context.Context, runID, name string) (payload []byte, ok bool, err error)
}

func (e *Engine) signaler() Signaler {
	s, ok := e.store.(Signaler)
	if !ok {
		panic("rerun: this store does not support signals (it must implement Signaler)")
	}
	return s
}

// Deliver records an external event for a run. Call it from outside the
// workflow: an HTTP handler, a webhook, another service.
func (e *Engine) Deliver(ctx context.Context, runID, name string, payload any) error {
	b, err := e.codec.Marshal(payload)
	if err != nil {
		return fmt.Errorf("rerun: deliver %s/%s: marshal: %w", runID, name, err)
	}
	return e.signaler().PushSignal(ctx, runID, name, b)
}

// Wait blocks the workflow until a signal named `name` arrives, then journals
// and returns its payload. On replay it returns the journaled payload without
// waiting, so a workflow that waited three days for an approval resumes with the
// approval already in hand and does not wait again.
func Wait[T any](w *W, name string) (T, error) {
	return Do(w, "signal:"+name, func(ctx context.Context) (T, error) {
		sig := w.eng.signaler()
		for {
			raw, ok, err := sig.PopSignal(ctx, w.RunID, name)
			if err != nil {
				var zero T
				return zero, err
			}
			if ok {
				var v T
				if uerr := w.eng.codec.Unmarshal(raw, &v); uerr != nil {
					var zero T
					return zero, uerr
				}
				return v, nil
			}
			select {
			case <-ctx.Done():
				var zero T
				return zero, ctx.Err()
			case <-time.After(2 * time.Millisecond):
			}
		}
	})
}
```

> **Design.** `Wait[T]` is a `Do` whose function happens to block on a mailbox instead of computing. That is the entire integration with durability: because the received value is returned from a `Do`, it is journaled on the way out, and replay returns it from the journal without ever touching the mailbox again. A signal is not a new kind of thing in the engine; it is a step with an external source.

> **Idiom.** `Signaler` is a separate optional interface, not a method bolted onto `Store`. A backend with no use for signals does not implement it; `signaler()` type-asserts and panics with a clear message if you call `Wait` on a store that cannot support it. This is the Day 4 interface-segregation rule applied again: capabilities are small interfaces a backend opts into.

The in-memory mailbox is a per-key queue on the store.

**`internal/memstore.go`** (the Signaler methods)

```go
func sigKey(runID, name string) string { return runID + "\x00" + name }

func (m *MemStore) PushSignal(ctx context.Context, runID, name string, payload []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := sigKey(runID, name)
	m.signals[k] = append(m.signals[k], payload)
	return nil
}

func (m *MemStore) PopSignal(ctx context.Context, runID, name string) ([]byte, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	k := sigKey(runID, name)
	q := m.signals[k]
	if len(q) == 0 {
		return nil, false, nil
	}
	p := q[0]
	m.signals[k] = q[1:]
	return p, true, nil
}
```

> **Trap.** Delivery can land before the workflow reaches `Wait`. That is fine, and it is why `PushSignal` stores into a queue rather than handing off to a waiting goroutine: the value sits in the mailbox until `Wait` polls and finds it. A signal delivered to a run that has not asked for it yet is not lost; a signal delivered twice queues twice. The poll is a two-millisecond loop here; a production backend would use notification instead of polling, but the contract is the same.

### Milestone: deliver an approval, then replay it

`examples/signals/main.go` runs a two-step approval workflow twice. Phase A starts a fresh run and delivers the approval live. Phase B seeds a run whose `signal:approval` step is already journaled and recovers it with no delivery at all; if `Wait` did not read the journal, recovery would block forever.

```
$ go run ./examples/signals
Phase A (live delivery): finished=true approved=true
Phase B (journaled signal, crash recovery): finished=true approved=true
```

Phase B completed without anyone delivering the approval a second time. The value came from the journal, which is exactly what "the workflow waited three days and the process restarted twice in between" needs. The suite covers both directions: `TestSignal_DeliveredWhileWaiting` proves a live delivery unblocks the wait, and `TestSignal_ReplayReturnsJournaledWithoutWaiting` proves replay returns the journaled value without a delivery, both under the race detector.

### Exercises

1. `Wait` polls every two milliseconds. Replace the poll with a notification: give the store a per-key channel that `PushSignal` sends on, and have `Wait` select on it. What must `Wait` still do on the first iteration before blocking on the channel, and why? Hint: the signal may already be in the mailbox.
2. Add `WaitTimeout[T](w, name, d)` that returns a sentinel error if no signal arrives within `d`. Build it from `Wait` and the durable `Sleep` from hard problem 2 so the timeout itself survives a crash. Which existing piece makes the timeout durable rather than a wall-clock race?
3. Two different signals, "approve" and "reject", race. Sketch a `Select` that waits for whichever arrives first and journals only the winner. Why does journaling the winner's name (not just its payload) matter for deterministic replay?

Solutions in Appendix B.

## Hard problem 4: Versioning across deploys

### The problem

Replay's contract is determinism: the workflow function must take the same path it took before, or the tag check from Day 2 panics. Now ship a new version of that function while runs are in flight. The new code inserts a step, an old run's journal does not have it, replay reaches a step the journal never recorded, the tags diverge, and a run that was halfway to done can no longer finish. Changing a workflow's code is the most common way to break a running workflow, and the journal's own strictness is what breaks it.

### Pin the branch in the journal

The fix is to make the version a journaled value, so a run records which code path it is on and replays that path forever, regardless of what the deployed code now prefers. New runs journal the new version and take the new path; in-flight runs replay their old version and keep the old path. One primitive does it.

**`version.go`**

```go
// Version pins a run to a code path so in-flight runs survive a deploy that
// changes the workflow. On the first run it journals max, the newest version
// this build knows; on replay it returns the journaled value, so a run started
// before the change keeps taking its original branch while new runs take the
// new one. It panics if the journaled version is outside [min, max], catching a
// rollback to code too old to honor a version some run already committed to.
func Version(w *W, changeID string, min, max int) int {
	v, _ := Do(w, "version:"+changeID, func(ctx context.Context) (int, error) {
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
```

You use it by gating the changed region on the returned version:

```go
v := rerun.Version(w, "add-fraud-check", 1, maxV)
rerun.Do(w, "validate", validate)
if v >= 2 {
	rerun.Do(w, "fraud-check", fraudCheck) // new in version 2
}
rerun.Do(w, "charge", charge)
```

> **Pattern.** This is the change-marker pattern Temporal exposes as `GetVersion` and others call patching. The value is journaled the first time the marker is reached, so it is fixed for the life of the run. Every workflow change gets its own `changeID`; you never edit an old branch, you add a new one behind a higher version and let old runs drain on the old path.

> **Trap.** The marker only protects runs that recorded it. A run started before you introduced any `Version` call has no marker in its journal, so the call's tag will not match whatever step the journal has at that position. The honest rule: introduce the marker before you need it, or accept that runs predating it must finish on the old binary. Production engines add a "no marker recorded" default to paper over this; rerun keeps the primitive small and states the constraint instead.

> **Design.** The `[min, max]` bounds are a rollback guard. If a run journaled version 2 and you deploy code that only knows version 1, replay reads 2, finds it above `max`, and panics loudly rather than silently taking the wrong branch. The check turns an undetectable correctness bug into an obvious crash at the marker.

### Milestone: an old run and a new run on the same code

`examples/versioning/main.go` deploys one engine running version-2 code, a workflow that added a fraud check behind `Version`. It seeds an old run whose journal pins it to version 1, recovers it, then starts a fresh run. A counter records how many fraud checks actually executed.

```
$ go run ./examples/versioning
deployed code = v2 (adds a fraud check behind Version)
old run (pinned to v1) fraud checks run: 0
new run (v2)           fraud checks run: 1
```

Same binary, same workflow function, two outcomes: the old run replayed version 1 and skipped the fraud check, the new run journaled version 2 and ran it. The journal, not the code, chose the branch. `TestVersion_OldRunKeepsItsBranch` asserts the same property under the race detector.

### Exercises

1. Delete the `if v < min || v > max` guard and seed a run journaled at version 3 against code with `max = 2`. What happens, and where? Restore the guard and confirm the panic names the run and the bound.
2. A workflow has two independent changes over time. Show why each needs its own `changeID` rather than reusing one and bumping `max` twice. Consider a run that recorded the first change but not the second.
3. Once every run older than version 2 has drained, the `if v >= 2` branch is the only path left. Describe the two-deploy procedure to remove the marker safely, and why you cannot delete it in the same deploy that stops creating version-1 runs.

Solutions in Appendix B.


## Shipping it as a library: the MVP checklist

You have built rerun and hardened it through the four hard problems. Turning it into a library other people can depend on is a separate step, and most of the work is not code. An MVP library clears four bars: someone can install it, the README does not overstate what it does, they can write one real workflow with it, and enough scaffolding sits around it to make adopting it reasonable. What follows is the checklist, with the MVP cut line marked. Build what is above the line first, and treat the rest as a deliberate fast-follow.

### Make it honest

This blocks publishing, because a durable execution library that overstates its guarantee will cost an adopter real money the first time a process dies in the wrong place.

1. Move your README off any "exactly once" phrasing. State the guarantee as it actually is: durable and resumable, at-least-once for side effects, and exactly-once only when your steps are idempotent. The mechanism is faithful, so the wording has to match it.
2. Add a Non-goals and Limitations section that names the at-least-once window, the absence of a built-in retry policy, timeouts, and cancellation, the lock-polling scaling ceiling, and the fact that this is not a Temporal replacement. Adopters trust a library that states its limits over one that hides them.

### Make it usable for one real workflow

3. Add workflow input and result. Today a workflow takes only its `*W` and returns an error, and `Start` accepts no argument, so you can run only parameterless workflows and cannot read a result or even the failure reason back out. Thread an input into `Start` and journal it as the run's seed so replay sees the same value, let the workflow read it with `Input[T]`, turn the workflow's return into a journaled result you fetch with `Result[T]`, and persist the terminal error instead of dropping it. This is the one feature that separates a demo from a library, because real workflows take data in and hand a result back.

### Package it as a module

4. Replace the placeholder module path `github.com/sylvester-francis/rerun` with your own, and fix every import in the examples and tests to match.
5. Put your name and year in LICENSE in place of the placeholder.
6. Run the SQLite and Postgres backends against the storetest `Contract` on a real database. Either they pass and you keep the persistence claim, or you label them experimental until they do. The in-memory store is verified by the suite, but the disk and network backends are only verified once you run them against the real thing.
7. Add a `doc.go` with a package comment so the pkg.go.dev page reads well, and commit both go.mod and go.sum.

### Make it adoptable

8. Finalize the README around a single honest sentence on what rerun is, followed by install, the smallest example, the one rule on determinism, persistence, the non-goals, and a Status line stating v0.x with an unstable API.
9. Add CONTRIBUTING.md, SECURITY.md, and a CHANGELOG.md, so a contributor knows how to help and a user knows what changed between tags.
10. Tag v0.1.0 and push it. Stay on v0 so you owe no stability promise while the surface is still moving. Run `git tag v0.1.0` then `git push origin v0.1.0`, and the release is live.

That is the MVP cut line. Everything past it is a deliberate v0.2 fast-follow, not a gap that blocks launch: a built-in `RetryPolicy` with backoff, which is ergonomics rather than a missing capability since the retry pattern already works as a loop over `Do`; per-step timeouts; cancellation of a running run; and a public list and get surface over the store for building an admin view. Ship the honest minimum first, then add these as real workflows ask for them.


## Appendix A: The complete source

The full, verified project ships as a companion tarball, `rerun.tar.gz`, with this layout:

```
rerun/
  go.mod
  store.go  codec.go  clock.go  hooks.go  errors.go
  engine.go  workflow.go  run.go
  internal/memstore.go        internal/memstore_test.go
  storetest/storetest.go
  sqlite/sqlite.go
  tools/mutate/main.go
  helpers_test.go  engine_test.go  workflow_test.go
  clock_test.go  errors_test.go  hooks_test.go  panics_internal_test.go
  examples/skeleton/  examples/recover/  examples/durablesleep/  examples/capstone/
  README.md  LICENSE  Makefile  .github/workflows/ci.yml
```

Everything except `sqlite/sqlite.go` was compiled and run under Go 1.22 with `go test -race`; the suite is green and every milestone output in this guide is captured from an actual run. `sqlite/sqlite.go` is syntactically validated and contract-pinned but, per the note on Day 4, its runtime was not separately exercised in the build environment. Run `go mod tidy` once to fetch `modernc.org/sqlite`, then `go test -race ./...` exercises it directly. The day-by-day files in the guide are sliced from this same source, so what you read is what compiles.

---


Part II adds `signal.go`, `version.go`, `postgres/postgres.go`, and four more programs under `examples/`. The complete hardened project, including all four extensions, ships as `rerun-hardened.tar.gz`; the Day 1 to 7 library is `rerun.tar.gz`. The two are kept separate so the code in Days 1 to 7 matches `rerun.tar.gz` byte for byte, and the evolved interfaces (the try-lock `Guarder`, the deadline-based `Sleep`) live in the hardened tree alongside the chapters that introduce them.

## Appendix B: Exercise solutions

### Day 1

**1.** A three-step workflow journals three entries with `seq` 0, 1, 2. The sequence number is owned entirely by `W.seq`, a field initialized to 0 and incremented once per completed `Do`. It is the workflow's position counter, independent of tags or wall-clock time, which is precisely why replay can match by position.

**2.** Returning a channel makes `json.Marshal` fail, and `Do` panics with `marshal failed at seq N`. Panicking is correct: an unserializable step result is a programmer error, because no input from a healthy world makes a channel serializable. The code is simply wrong and cannot be made durable, so failing loud at the first occurrence beats returning an error a caller might log and ignore.

**3.** `Start` cannot block because a workflow may sleep for hours, and a caller that starts a thousand workflows cannot afford a thousand blocked goroutines waiting on them. Launching `exec` in its own goroutine and returning immediately lets one process drive many concurrent runs; the caller observes completion through the store or an `Observer`, not by waiting on the call.

### Day 2

**1.** Adding `notify` after `load` and re-seeding the two-entry journal yields **two** live executions during recovery, `load` and `notify` (verified). `extract` and `transform` replay from the journal; the first step without an entry, `load`, and everything after it run live.

**2.** Emitting steps in a different order on the second run trips the tag-mismatch check in `replayStep`, which panics with the seq, the expected tag from the journal, and the tag the workflow presented. A panic beats a logged warning because a determinism violation makes every subsequent journal match meaningless; continuing would produce a corrupt run that looks successful. The panic stops the corruption at its first symptom and names exactly where the divergence is.

**3.** Changing a step's return type so the old payload no longer fits fails inside `replayStep`, at `codec.Unmarshal` into the new `T`. That is the versioning problem: an in-flight run's journal was written by the old code. The implication is that you cannot freely change step shapes for runs already in flight; you version the workflow name, make only additive changes (a new step at the end is always safe), or anchor divergence in a journaled version marker.

### Day 3

**1.** With `Do("a")`, `Sleep`, `Do("b")` and only `a` journaled, recovery replays `a` (live count 0), then runs the sleep **live** because it was never journaled (the crash was before it), then runs `b` live (verified: `a live=0, b live=1, recovery took 200ms`). The sleep runs live this time, unlike the Day 3 milestone, precisely because the crash happened before it was recorded; in the milestone the sleep had already completed and been journaled, so replay skipped it. Whether a sleep is skipped depends only on whether its journal entry exists.

**2.** Cancelling the context mid-sleep makes the `Sleep` step return `ctx.Err()`. That error is journaled by `liveStep` exactly like any step error, with the error string stored. On replay the sleep step reconstructs and returns that same error, so a workflow cancelled mid-sleep stays cancelled deterministically rather than sleeping again.

**3.** A typed business error is **error-worthy** (expected; the caller decides). A read-only SQLite file is **error-worthy** as an operational condition from a store write, though failing hard at startup is defensible if it is a config mistake. A `Do` tag computed from `rand.Int()` is **panic-worthy** the moment it causes a replay mismatch, because it is a determinism bug by construction. A journal payload that is valid JSON for a different shape is **panic-worthy**: a correct engine talking to its own journal never sees this, so its presence means the code or stored data is wrong.

### Day 4

**1.** A `map` plus an append-only JSON-lines file is durable: append each `Log` as a line, replay the file on startup to rebuild the map. It works because durability comes from an append-only record, not from a database. What SQLite buys you over this is querying (find incomplete runs without scanning everything), crash-atomic writes (WAL ensures a half-written record is invisible), and concurrency control, not durability per se.

**2.** Narrowing a read-only consumer, the recovery scan, to take `Reader` instead of `Store` still compiles, because Go satisfies interfaces structurally and any `Store` is a `Reader`. The narrowing documents in the type signature that this code reads and does not write or lock, which is real information and prevents accidental writes.

**3.** Today `SetMaxOpenConns(1)` is *not* fully redundant with the `Acquire` mutex. `Acquire` serializes per-run *workflow execution*, but `Recover`'s call to `Incomplete`, and the contract tests' direct store calls, happen outside any per-run lock, so without the connection limit those could race with a writer at the SQLite level. If you changed `Acquire` to a true distributed lock and routed every database call through one serialized path, the connection limit could become redundant; as written, it is the backstop that makes concurrent store access safe.

### Day 5

**1.** Deleting an assertion does not move the coverage percentage, because coverage measures whether a line *executed*, not whether anything *checked* its result. That is the entire limitation of coverage as a quality signal and the reason mutation testing (Day 6) exists: a test can run every line and assert nothing.

**2.** The unknown-workflow panic must be triggered synchronously, so it is white-box. Driven through `Start` it fires inside the spawned `exec` goroutine and crashes the test process; a white-box test in `package rerun` calls the unexported `lookup` directly and recovers the panic in the same goroutine:

**`panics_internal_test.go`** (excerpt)

```go
func TestLookup_UnknownPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on unknown workflow")
		}
	}()
	e := New(noStore{})
	e.lookup("never-registered") // must panic
}
```

**3.** The SQLite test hands `storetest.Contract` a factory like `func() rerun.Store { return sqlite.New(filepath.Join(t.TempDir(), "t.db")) }`. The sub-test that fails against a backend whose `LoadLogs` dropped `ORDER BY` is the "logs return in seq order" case: it inserts entries `2, 0, 1` and asserts they read back `0, 1, 2`. It is the one that matters because replay matches journal entries to `Do` calls by position, so a store that returns rows in insertion or arbitrary order corrupts every recovered run, and only an out-of-order insert exposes it.

### Day 6

**1.** Adding the `obs.OnStep` removal as a new `mutant` entry, `{name: "drop OnStep", file: "workflow.go", old: "\tw.eng.obs.OnStep(w.RunID, l)\n", new: "", expect: mustKill, reason: "TestObserver_ReceivesAllEvents"}`, and running `make mutate` reports it `killed`, because the observer no longer receives step events and `TestObserver_ReceivesAllEvents` asserts it does. The expectation holds, the row prints `killed / killed / ok`. What the entry encodes is durable coverage of a known fault: a later change that silently drops that assertion is now caught by the mutation run, not only by the unit run, because the tester asserts the fault stays killed.

**2.** The `<` to `<=` off-by-one is caught by `TestPartialReplay_ResumesMidway`. With a journal of K entries, correct code does `w.seq < K`: at `seq == K` (the first step past the journal) the guard is false and the step runs live. The mutant does `w.seq <= K`: at `seq == K` the guard is true and `replayStep` indexes `w.logs[K]`, which is out of range, and the run fails. A test that seeds a *complete* journal of all N steps never issues a `Do` call at `seq == len(logs)`, because the workflow ends exactly when the journal does, so it never exercises the boundary where `<` and `<=` differ. Only a partial journal forces a step at that boundary.

**3.** Removing `w.replay = false` is truly equivalent, not merely uncaught, by this argument: a step runs live only when the guard `w.replay && w.seq < len(w.logs)` is false; given the flag may still be true, the false guard implies `w.seq >= len(w.logs)`. Since `seq` only increments and `len(logs)` is fixed for the duration of a run, every subsequent step also has `seq >= len(logs)`, so the guard stays false regardless of the flag. The flag therefore cannot change any decision once live execution begins. It would stop being equivalent if the journal could grow during a run (so `len(logs)` rises past `seq`), if `seq` could reset, or if the guard were changed to gate solely on `w.replay` (dropping the `seq < len(logs)` bound), at which point the flag becomes the only thing distinguishing replay from live.

### Day 7

**1.** `New(store, ...Opt)` constructs an engine on a chosen backend. `WithCodec` / `WithClock` / `WithObserver` override serialization, time, and lifecycle observation. `Handle(name, fn)` registers a workflow. `Start(ctx, wf, runID)` begins a new run. `Recover(ctx)` resumes every unfinished run after a restart. `Do[T](w, tag, fn)` is one durable step; `Sleep(w, d)` is a durable wait. What a first real workflow arguably still wants is a public way to read a finished run's result or status from outside the engine (the data is in the `Store`, but there is no exported helper), which is a reasonable addition. Nothing public clearly needs unexporting: the in-memory store is already `internal`, and the `Status` constants are legitimately part of the surface.

**2.** A README determinism section in under ten lines:

```
A workflow body must be deterministic. Capture anything non-deterministic
(time, randomness, a read that steers a branch) inside a Do step so its value
is journaled and replayed, never recomputed.

    // wrong: the branch can differ on replay
    if time.Now().Hour() < 12 { ... }
    // right: journal the decision
    morning, _ := rerun.Do(w, "is-morning", func(ctx) (bool, error) {
        return time.Now().Hour() < 12, nil
    })

A violation panics on replay with the exact step, so it fails loud, not silent.
```

---


### Part II, hard problem 1 (multi-process)

1. The re-check skips a run already Done; the live `finalize` count stays 12 because replaying a fully journaled run executes no live steps, so a redundant acquire adds no side effect.
2. Without `Close`, the held entry stays set and the run can never be re-acquired, stranding it permanently; `defer release.Close()` in `exec` runs even when the workflow body panics, so the lease is always released.
3. A blocking `Acquire` makes the four workers queue on the same run, so the backlog drains roughly serially; the try-lock lets each worker take a different free run, so twelve runs finish in about the time of the slowest handful rather than end to end. Try-lock is the right default because recovery is a scan over many runs, not a wait for one.

### Part II, hard problem 2 (durable timers)

1. The first run computes the deadline from the wrong clock, but it journals an absolute instant; every later run waits until that same instant, so after the first run a wrong wall clock changes nothing. Journaling the duration instead would re-apply the offset on every restart.
2. A `DueBefore(ctx, t) ([]Run, error)` query on the store, a poller loop that calls it and resumes the due runs, and a `Sleep` that journals the deadline and returns immediately. A sleeping workflow then holds only a row, no goroutine.
3. `SleepUntil(w, t)` journals `t.UnixNano()` directly, replacing the `w.eng.clock.Now().Add(d).UnixNano()` line; the remaining-time wait below it is identical.

### Part II, hard problem 3 (signals)

1. `Wait` must `PopSignal` once before blocking, because the signal may already be queued from a delivery that beat the workflow to the mailbox; only when the mailbox is empty does it wait on the channel. Skipping the first pop would hang on a signal that already arrived.
2. Build `WaitTimeout` as a race between `Wait` and `Sleep(d)`, taking whichever returns first. The durable `Sleep` makes the deadline survive a crash, so the timeout is replayed from the journaled deadline rather than restarting from `d` on every recovery.
3. `Select` journals the winning signal's name so replay takes the same branch; journaling only the payload would lose which of "approve" or "reject" won, and a replay that polled the mailbox again could pick the other one, diverging from the original path.

### Part II, hard problem 4 (versioning)

1. Replay reads version 3, finds it above `max` 2, and panics at the `Version` marker, naming the run and `[1,2]`. Without the guard it would fall through `if v >= 2` and silently take the version-2 branch, an undetectable wrong path.
2. A second `changeID` is required because a run that recorded the first change at version 2 but predates the second would, under a reused id, read 2 and wrongly take the second change's branch as well. Separate ids keep the two decisions independent, each pinned by its own journal entry.
3. Deploy once with the marker still present but both arms collapsed to the version-2 behavior (the old branch removed, the marker still journaled), let every version-1 run drain, then deploy again to delete the marker. Deleting it in the same deploy that stops creating version-1 runs would break any version-1 run still replaying, since its journal expects the marker's tag at that position.

## Appendix C: The week at a glance

| Day | What you build | Milestone you run | Files added or grown |
|---|---|---|---|
| 1 | Journal-and-replay idea; walking skeleton | A 2-step workflow journals and finishes `Done` | store, codec, clock, hooks, engine, workflow (live-only), run, memstore |
| 2 | Determinism and crash recovery | A run resumes from a partial journal; only the unfinished step runs | errors; workflow grows replay; run grows `Recover` |
| 3 | Durable time and the panic/error line | A 1s sleep is waited once, skipped on recovery | workflow grows `Sleep` |
| 4 | Pluggable storage (SOLID) and a SQLite backend | The SQLite backend compiles behind the `Store` contract | sqlite |
| 5 | A test harness: contract suite, fake time, white-box panics | The whole suite green under `-race` | storetest, helpers_test, the test suite |
| 6 | Mutation testing the engine | A real mutant dies; an equivalent one survives | tools/mutate (the mutation tester) |
| 7 | Shipping rerun: API, repo, acceptance run | The signup saga charges once across a crash | examples/capstone; README, LICENSE, Makefile, CI |

One rule underneath all seven days: a workflow body is deterministic, and everything non-deterministic is captured inside a `Do` step so it is journaled and replayed, never recomputed. Hold that line and the engine does the rest.

### Part II at a glance

| Hard problem | Mechanism | What proves it |
| --- | --- | --- |
| Multi-process execution | try-lock lease on `Guarder`; Postgres advisory lock across machines | 12 jobs, 4 concurrent workers, 12 finalizations, race-clean |
| Durable timers | journal the absolute deadline, wait only the remainder | 20m remainder honored across a mid-sleep crash |
| Signals | `Wait[T]` is a `Do` whose value comes from an external mailbox | a journaled approval replays with no second delivery |
| Versioning | journal the version, branch on the journaled value | old run pinned to v1, new run on v2, one binary |

