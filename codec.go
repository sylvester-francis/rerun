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

// Codec serializes step results into and out of the journal.
type Codec interface {
	Marshal(v any) ([]byte, error)
	Unmarshal(data []byte, v any) error
}

// jsonCodec is the default Codec, delegating to encoding/json.
type jsonCodec struct{}

func (jsonCodec) Marshal(v any) ([]byte, error) { panic("rerun: not implemented") }

func (jsonCodec) Unmarshal(data []byte, v any) error { panic("rerun: not implemented") }
