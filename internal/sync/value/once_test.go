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

package value_test

import (
	"sync"
	"testing"
	"time"

	"github.com/k0sproject/k0s/internal/sync/value"

	"github.com/stretchr/testify/assert"
)

func TestOnce_Panics(t *testing.T) {
	var underTest value.Once[int]
	assert.Panics(t, func() { underTest.Get() })
	assert.True(t, underTest.SetOnce(42))
	assert.Equal(t, 42, underTest.Get())
	assert.False(t, underTest.SetOnce(84))
}

func TestOnce_Ready(t *testing.T) {
	var underTest value.Once[int]

	var got int
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-underTest.Ready()
		got = underTest.Get()
	}()

	time.Sleep(10 * time.Millisecond) // Simulate some delay
	assert.True(t, underTest.SetOnce(42))
	wg.Wait()

	assert.Equal(t, 42, got)
}

func TestOnce_Await(t *testing.T) {
	test := func(t *testing.T, await func(*value.Once[int]) (int, bool)) {
		var underTest value.Once[int]

		var got int
		var ok bool
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			got, ok = await(&underTest)
		}()

		time.Sleep(10 * time.Millisecond) // Simulate some delay
		assert.True(t, underTest.SetOnce(42))
		wg.Wait()

		if assert.True(t, ok) {
			assert.Equal(t, 42, got)
		}
	}

	t.Run("using_method", func(t *testing.T) {
		test(t, func(underTest *value.Once[int]) (int, bool) { return underTest.Await(nil) })
	})

	t.Run("using_package_func", func(t *testing.T) {
		test(t, func(underTest *value.Once[int]) (int, bool) { return value.Await(underTest, nil) })
	})

	t.Run("done", func(t *testing.T) {
		var underTest value.Once[int]
		done := make(chan struct{})
		go func() {
			defer close(done)
			time.Sleep(10 * time.Millisecond)
		}()
		value, ok := underTest.Await(done)
		if assert.False(t, ok) {
			assert.Zero(t, value)
		}
	})
}
