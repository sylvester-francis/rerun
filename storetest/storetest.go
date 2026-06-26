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

// Package storetest is the importable contract suite. Any rerun.Store
// implementation can prove itself correct by passing RunStoreContract.
package storetest

import (
	"testing"

	"github.com/sylvester-francis/rerun"
)

// RunStoreContract exercises a Store against the behavior every backend must
// honor: create-and-load, seq ordering, incomplete filtering, status
// transitions, locking, and duplicate-create rejection.
func RunStoreContract(t *testing.T, makeStore func() rerun.Store) {
	panic("rerun: not implemented")
}
