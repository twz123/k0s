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
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/lifecycle"
	"github.com/k0sproject/k0s/internal/testutil"
	"github.com/stretchr/testify/assert"
)

func TestGroup_SelfDependency(t *testing.T) {

	var underTest lifecycle.Group
	provide := make(chan struct{})

	var self *lifecycle.Ref[any]
	self = lifecycle.Go(&underTest, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
		<-provide
		if _, err := lifecycle.Get(ctx, slot, self); err != nil {
			return nil, nil, nil, fmt.Errorf("self: %w", err)
		}
		return nil, nil, nil, nil
	})

	close(provide)

	underTest.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
		_, err := lifecycle.Get(ctx, slot, self)
		assert.ErrorContains(t, err, "self: circular dependency")
		assert.ErrorIs(t, err, lifecycle.ErrCircular)
	})
}

func TestGroup_LoopDetection(t *testing.T) {
	order := [...]int{0, 1, 2, 3, 4, 5}
	testutil.Permute(order[:], func() bool {
		t.Logf("Testing order: %v", order)

		provide := make(chan struct{})
		seq := make([]chan struct{}, len(order))
		for i := range seq {
			seq[i] = make(chan struct{})
		}

		var underTest lifecycle.Group

		circle := make([]*lifecycle.Ref[any], len(order)-1)
		for i := range circle {
			order, i, j := order[i], i, (i+1)%len(circle)
			circle[i] = lifecycle.Go(&underTest, func(ctx context.Context, slot *lifecycle.Slot) (any, chan<- struct{}, <-chan struct{}, error) {
				<-provide
				for i := 0; i < order; i++ {
					<-seq[i]
				}
				time.AfterFunc(10*time.Microsecond, func() { close(seq[order]) })
				if _, err := lifecycle.Get(ctx, slot, circle[j]); err != nil {
					return nil, nil, nil, fmt.Errorf("(order %d) %d awaits %d: %w", order, i, j, err)
				}
				return nil, nil, nil, nil
			})
		}

		close(provide)

		underTest.Do(context.TODO(), func(ctx context.Context, slot *lifecycle.Slot) {
			for i := 0; i < order[len(order)-1]; i++ {
				<-seq[i]
			}
			close(seq[order[len(order)-1]])

			for i := range circle {
				order, i, j := order[i], i, (i+1)%len(circle)
				_, err := lifecycle.Get(ctx, slot, circle[i])
				assert.ErrorContains(t, err, fmt.Sprintf("(order %d) %d awaits %d: ", order, i, j))
				assert.ErrorIs(t, err, lifecycle.ErrCircular)
			}
		})

		return !t.Failed()
	})
}
