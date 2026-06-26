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

import "time"

// Clock abstracts time so workflows can be driven by a fake clock in tests.
type Clock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// wall is the default Clock, delegating to the standard library.
type wall struct{}

func (wall) Now() time.Time { panic("rerun: not implemented") }

func (wall) After(d time.Duration) <-chan time.Time { panic("rerun: not implemented") }
