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

package lifecycle_test

import (
	"context"
	"fmt"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/lifecycle"
	"github.com/stretchr/testify/assert"
)

func TestGroup_LoopDetection(t *testing.T) {

	var g lifecycle.Group

	var delays []time.Duration
	provide := make(chan struct{})

	var self *lifecycle.Ref[any]
	self = lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
		<-provide
		time.Sleep(delays[0])
		if _, err := lifecycle.Get(ctx, slot, self); err != nil {
			return nil, nil, nil, fmt.Errorf("self: %w", err)
		}
		return nil, nil, nil, nil
	})

	circle := make([]*lifecycle.Ref[any], 10)
	for i := range circle {
		i := i
		j := i - 1
		if i == 0 {
			j = len(circle) - 1
		}
		circle[i] = lifecycle.Go(&g, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
			<-provide
			time.Sleep(delays[i+1])
			if _, err := lifecycle.Get(ctx, slot, circle[j]); err != nil {
				return nil, nil, nil, fmt.Errorf("%d awaits %d: %w", i, j, err)
			}
			return nil, nil, nil, nil
		})
	}

	// todo nested deadlock....?

	delays = make([]time.Duration, 12)
	for i := range delays {
		delays[i] = 0
		delays[i] = time.Duration(i) * time.Millisecond
	}
	rand.Shuffle(len(delays), func(i, j int) {
		delays[i], delays[j] = delays[j], delays[i]
	})
	close(provide)

	g.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
		time.Sleep(delays[11])

		var err error

		for i := range circle {
			_, err = lifecycle.Get(ctx, slot, circle[i])
			assert.ErrorContains(t, err, "i awaits j")
			assert.ErrorIs(t, err, lifecycle.ErrCircular)
		}
	})
}
