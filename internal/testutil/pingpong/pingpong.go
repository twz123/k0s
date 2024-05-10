/*
Copyright 2024 k0s authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package pingpong

import "testing"

type PingPong pingPong

func New(t *testing.T) *PingPong {
	return (*PingPong)(makePingPong(t))
}

func (pp *PingPong) BinPath() string   { return ((*pingPong)(pp)).binPath() }
func (pp *PingPong) BinArgs() []string { return ((*pingPong)(pp)).binArgs() }
func (pp *PingPong) AwaitPing() error  { return ((*pingPong)(pp)).awaitPing() }
func (pp *PingPong) SendPong() error   { return ((*pingPong)(pp)).sendPong() }
