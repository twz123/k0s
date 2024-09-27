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

import "errors"

var ErrCircular = errors.New("circular dependency")

type direction uint8

const (
	unrelated direction = iota
	dependency
	dependent
)

func (d direction) inverse() direction {
	switch d {
	case dependency:
		return dependent
	case dependent:
		return dependency
	default:
		return d
	}
}

type node struct {
	edges map[*node]direction
}

func (n *node) hasRelations(dir direction) bool {
	for _, edgeDir := range n.edges {
		if edgeDir == dir {
			return true
		}
	}

	return false
}

func (n *node) add(d direction, other *node) error {
	if other.relatesTo(n, nil) {
		return ErrCircular
	}

	mapSet(&n.edges, other, d)
	mapSet(&other.edges, n, d.inverse())
	return nil
}

func (n *node) relatesTo(other *node, visited map[*node]struct{}) bool {
	if n == other {
		return true
	}

	mapSet(&visited, n, struct{}{})
	for neighbor := range n.edges {
		if _, visited := visited[neighbor]; visited {
			continue
		}

		if neighbor.relatesTo(other, visited) {
			return true
		}
	}

	return false
}

func mapSet[K comparable, V any](m *map[K]V, k K, v V) {
	if *m == nil {
		*m = map[K]V{k: v}
	} else {
		(*m)[k] = v
	}
}
