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

// Coordinates startup and shutdown for interdependent components.
//
// A [Component] is a unit of runtime behavior with a managed "lifecycle", i.e.,
// startup and shutdown: Something that can be started, may expose a useful
// runtime result, and may need to be stopped in coordination with other running
// parts of the process. Components are governed by a lifecycle [Group].
//
// A Component is started by [Go] or [GoFunc], which registers it with the
// Group. The returned [Ref] is a reference to the Component's startup outcome.
// A Ref can be dereferenced during the startup of dependent components within
// the same Group. The Group blocks the dependent Component's startup until the
// referenced Component has started. The Group automatically records that
// dependency relation and detects and rejects circular references.
//
// Guarding the Component's startup result behind a Ref allows for building an
// implicit component dependency graph in a natural way by consuming those
// references during other components' startup. It enables the concurrent
// startup of many components while ensuring that the required dependencies are
// operational before moving on to the dependents.
//
// Once all components have been registered, the caller may enter the Group's
// completion phase via [Group.Complete]. The function passed to Complete
// receives the completion context. It is part of the same lifecycle as the
// component startup contexts, so it may be used to require refs from the same
// Group and observe startup failures through context cancellation. Unlike a
// component's startup context, the completion context does not represent a
// component itself, so requiring references from it does not create new
// dependency relations. Complete is meant for top-level orchestration logic
// that needs to wait for startup outcomes, perform one-off coordination work,
// and then keep the process alive until the Group shuts down.
//
// Shutting down a Group can be initiated via [Group.Shutdown]. During shutdown,
// components are stopped in reverse dependency order, that is, dependents are
// stopped before the components they rely on.
//
// If any component fails to start, the Group enters shutdown and completion is
// cancelled with the startup error.
package lifecycle
