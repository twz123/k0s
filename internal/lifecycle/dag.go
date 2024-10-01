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

package lifecycle

import (
	"errors"
	"fmt"
	"slices"
)

var ErrSelfReferential = errors.New("self-referential")

type node struct {
	dependencies []*node
	dependents   []*node
}

func (n *node) addDependency(other *node) (err error) {
	if exists, depth := other.hasDependency(n, nil); exists {
		return selfRefErr{depth}
	}

	n.dependencies = append(n.dependencies, other)
	other.dependents = append(other.dependents, n)
	return nil
}

func (n *node) disposeLeaf(consumeNewLeaf func(*node)) {
	for _, d := range n.dependencies {
		if len(d.dependents) > 1 {
			i := slices.Index(d.dependents, n)
			d.dependents = slices.Delete(d.dependents, i, i+1)
		} else { // only n can be left in the dependents list
			d.dependents[0] = nil
			d.dependents = nil
			consumeNewLeaf(d)
		}
	}

	clear(n.dependencies)
	n.dependencies = nil
}

func (n *node) hasDependency(other *node, visited map[*node]struct{}) (exists bool, depth uint) {
	if n == other {
		return true, 0
	}

	for _, dependency := range n.dependencies {
		if dependency == other {
			return true, 1
		}
		if exists, depth := dependency.hasDependency(other, visited); exists {
			return true, depth + 1
		}
	}

	return false, 0
}

type selfRefErr struct {
	depth uint
}

func (e selfRefErr) Error() string {
	switch e.depth {
	case 0:
		return fmt.Sprintf("%s self-dependency", ErrSelfReferential)
	case 1:
		return fmt.Sprintf("%s direct dependency", ErrSelfReferential)
	default:
		return fmt.Sprintf("%s circular dependency at depth %d", ErrSelfReferential, e.depth)
	}
}

func (e selfRefErr) Is(target error) bool {
	if target, ok := target.(selfRefErr); ok {
		return e == target
	}
	return target == ErrSelfReferential
}

func mapSet[K comparable, V any](m *map[K]V, k K, v V) {
	if *m == nil {
		*m = map[K]V{k: v}
	} else {
		(*m)[k] = v
	}
}
