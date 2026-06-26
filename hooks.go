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

// Observer receives lifecycle events for logging and metrics. Events are
// passed by value so observers cannot mutate engine state.
type Observer interface {
	OnStart(r Run)
	OnStep(runID string, l Log)
	OnFinish(runID string, s Status)
}

// noopObserver is the default Observer: it does nothing.
type noopObserver struct{}

func (noopObserver) OnStart(r Run) {}

func (noopObserver) OnStep(runID string, l Log) {}

func (noopObserver) OnFinish(runID string, s Status) {}
