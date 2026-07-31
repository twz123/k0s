// SPDX-FileCopyrightText: 2022 k0s authors
// SPDX-License-Identifier: Apache-2.0

package watch

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"maps"
	"reflect"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/utils/ptr"
)

type Object interface {
	runtime.Object
	GetResourceVersion() string
	GetNamespace() string
	GetName() string
}

// A pointer to T that implements [Object].
type ObjectPtr[T any] interface {
	*T
	Object
}

// Watcher offers a convenient way of watching Kubernetes resources. An
// ephemeral alternative to Reflectors and Indexers, useful for one-shot tasks
// when no caching is required. It performs an initial list of all the resources
// and then starts watching them. In case the watch needs to be restarted
// (a.k.a. resource version too old), Watcher will re-list all the resources.
// The Watcher will restart the watch API call from time to time at the last
// seen resource version, so that stale HTTP connections won't make the watch go
// stale, too.
type Watcher[T any, PT ObjectPtr[T]] struct {
	List  func(ctx context.Context, opts metav1.ListOptions) (resourceVersion string, items []T, err error)
	Watch func(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)

	includeDeletions bool
	fieldSelector    string
	labelSelector    string
	errorCallback    ErrorCallback
}

// Condition is a func that gets called by [Watcher.Until] for each updated
// item. The watch will terminate successfully if it returns true, continue if
// it returns false or terminate with the returned error.
type Condition[T any] func(item *T) (done bool, err error)

// AllCondition is a func that gets called by [Watcher.UntilAll] with the
// complete sequence of currently matching items, in no particular order. The
// items are only valid for the duration of the call and must not be modified.
// The watch will terminate successfully if it returns true, continue if it
// returns false or terminate with the returned error.
type AllCondition[T any] func(items iter.Seq[*T]) (done bool, err error)

// ErrorCallback is a func that, if specified, will be called by the [Watcher]
// whenever it encounters some error. Whenever the returned error is nil, the
// Watcher will wait for the specified duration and retry the last call.
// Otherwise the Watcher will return the returned error.
type ErrorCallback = func(error) (retryDelay time.Duration, err error)

// Provider represents the backend for [Watcher].
// It is compatible with client-go's typed interfaces.
type Provider[L any] interface {
	List(ctx context.Context, opts metav1.ListOptions) (L, error)
	Watch(ctx context.Context, opts metav1.ListOptions) (watch.Interface, error)
}

// FromClient creates a [Watcher] from the given client-go client. Note that the
// types L and I need to be connected in a way that L is a pointer type to a
// struct that has an `Items` field of type []I. This function will panic if
// this is not the case. Refer to [FromProvider] in order to provide a custom
// way of obtaining items from the list type.
func FromClient[L metav1.ListInterface, I any, PI ObjectPtr[I]](client Provider[L]) *Watcher[I, PI] {
	itemsFromList, err := itemsFromList[L, I]()
	if err != nil {
		panic(err)
	}

	return FromProvider[L, PI](client, itemsFromList)
}

// FromProvider creates a [Watcher] from the given [Provider] and the
// corresponding itemsFromList function.
func FromProvider[L metav1.ListInterface, PI ObjectPtr[I], I any](provider Provider[L], itemsFromList func(L) []I) *Watcher[I, PI] {
	return &Watcher[I, PI]{
		List: func(ctx context.Context, opts metav1.ListOptions) (string, []I, error) {
			list, err := provider.List(ctx, opts)
			if err != nil {
				return "", nil, err
			}
			return list.GetResourceVersion(), itemsFromList(list), nil
		},
		Watch: provider.Watch,

		fieldSelector: fields.Everything().String(),
		labelSelector: labels.Everything().String(),
	}
}

// IsRetryable checks if the given error might make sense to be retried in the
// context of watching Kubernetes resources. Returns the retry delay and no
// error if it's retryable, or the passed in error otherwise.
func IsRetryable(err error) (time.Duration, error) {
	// Only consider errors that suggest a client delay ...
	if delaySecs, ok := apierrors.SuggestsClientDelay(err); ok {
		// ... and whose reason indicates that retries might make sense.
		switch apierrors.ReasonForError(err) {
		case metav1.StatusReasonTooManyRequests,
			metav1.StatusReasonServerTimeout,
			metav1.StatusReasonTimeout,
			metav1.StatusReasonServiceUnavailable:
			return time.Duration(delaySecs) * time.Second, nil
		}
	}

	return 0, err
}

// Ensure that [IsRetryable] is a valid error callback.
var _ ErrorCallback = IsRetryable

// IncludingDeletions will include deleted items in watches.
func (w *Watcher[T, PT]) IncludingDeletions() *Watcher[T, PT] {
	w.includeDeletions = true
	return w
}

// ExcludingDeletions will suppress deleted items from watches.
// This is the default, but has not effect for [Watcher.UntilAll].
func (w *Watcher[T, PT]) ExcludingDeletions() *Watcher[T, PT] {
	w.includeDeletions = false
	return w
}

// WithObjectName sets this Watcher's field selector in a way to only match
// objects with the given name.
func (w *Watcher[T, PT]) WithObjectName(name string) *Watcher[T, PT] {
	return w.WithFieldSelector(fields.OneTermEqualSelector(metav1.ObjectNameField, name))
}

// WithFieldSelector sets the given field selector for this Watcher. The default
// is to match everything:
//
//	watcher.FromClient(...).WithFieldSelector(fields.Everything())
//
// Refer to the [concept] for a general introduction to field selectors. To gain
// an overview of the supported values, have a look at the usages of
// [k8s.io/apimachinery/pkg/runtime.Scheme.AddFieldLabelConversionFunc] in the
// [Kubernetes codebase].
//
// [concept]: https://kubernetes.io/docs/concepts/overview/working-with-objects/field-selectors/
// [Kubernetes codebase]: https://sourcegraph.com/search?q=lang:go+AddFieldLabelConversionFunc%28...%29+repo:%5Egithub%5C.com/kubernetes/kubernetes%24+-file:_test%5C.go%24+select:content&patternType=structural
func (w *Watcher[T, PT]) WithFieldSelector(selector fields.Selector) *Watcher[T, PT] {
	w.fieldSelector = selector.String()
	return w
}

// WithLabelSelector sets the given label selector for this Watcher. The default
// is to match everything:
//
//	watcher.FromClient(...).WithLabelSelector(labels.Everything())
func (w *Watcher[T, PT]) WithLabelSelector(selector labels.Selector) *Watcher[T, PT] {
	w.labelSelector = selector.String()
	return w
}

// WithLabels sets this Watcher's label selector to match exactly the given Set.
// A nil and empty Sets are considered equivalent to labels.Everything(). It
// does not perform any validation, which means the server will reject the
// request if the Set contains invalid values.
func (w *Watcher[T, PT]) WithLabels(l labels.Set) *Watcher[T, PT] {
	return w.WithLabelSelector(labels.SelectorFromSet(l))
}

// WithErrorCallback sets this Watcher's error callback. It's invoked every time
// an error occurs and determines if the watch should continue or terminate:
//   - The returned error is nil: continue watching
//   - The returned error is not nil: terminate watch with that error
//
// If the error callback is not set or nil, the default behavior is to terminate.
func (w *Watcher[T, PT]) WithErrorCallback(callback ErrorCallback) *Watcher[T, PT] {
	w.errorCallback = callback
	return w
}

// Until runs a watch until condition returns true. It will error out in case
// the context gets canceled or the condition returns an error.
func (w *Watcher[T, PT]) Until(ctx context.Context, condition Condition[T]) error {
	s := sink[T, PT]{reset: func(items []T) (bool, error) {
		for i := range items {
			if done, err := condition(&items[i]); err != nil || done {
				return done, err
			}
		}
		return false, nil
	}}

	if w.includeDeletions {
		s.update = func(item PT, _ bool) (bool, error) { return condition(item) }
	} else {
		s.update = func(item PT, deleted bool) (bool, error) {
			if !deleted {
				return condition(item)
			}
			return false, nil
		}
	}

	return w.runWithSink(ctx, s)
}

// UntilAll runs a watch until condition returns true. It will error out in case
// the context gets canceled or the condition returns an error. In contrast to
// [Watcher.Until], the condition gets called with the complete sequence of
// matching items rather than with individual items. Every time any of the
// watched items change, condition is invoked with a sequence over all the
// current items. It may get called repeatedly with an unchanged sequence.
//
// Deleted items are reflected by their absence from the sequence:
// [Watcher.ExcludingDeletions] doesn't apply to this method.
func (w *Watcher[T, PT]) UntilAll(ctx context.Context, condition AllCondition[T]) error {
	type itemMap = map[types.NamespacedName]*T
	var current itemMap

	update := func(item PT, deleted bool) (done bool, err error) {
		k := types.NamespacedName{Namespace: item.GetNamespace(), Name: item.GetName()}
		if deleted {
			delete(current, k)
		} else {
			current[k] = item
		}
		return condition(maps.Values(current))
	}

	return w.runWithSink(ctx, sink[T, PT]{update: update, reset: func(items []T) (bool, error) {
		if len := len(items); len < 1 {
			current = nil
			return condition(func(yield func(*T) bool) {})
		} else {
			current = make(itemMap, len)
		}
		for i := range items {
			if done, err := update(&items[i], false); err != nil || done {
				return done, err
			}
		}
		return false, nil
	}})
}

func itemsFromList[L metav1.ListInterface, I any]() (func(L) []I, error) {
	// List types from client-go don't provide any methods to get their items.
	// Hence provide a way to get the items via reflection.

	index, err := func() ([]int, error) {
		var list L
		listType := reflect.TypeOf(list)
		if listType.Kind() != reflect.Pointer {
			return nil, fmt.Errorf("not a pointer type: %s", listType)
		}
		itemsType := reflect.TypeFor[[]I]()
		itemsField, ok := listType.Elem().FieldByName("Items")
		if !ok || itemsField.Type != itemsType {
			return nil, fmt.Errorf(`expected an "Items" field of type %s in %s`, itemsType, listType)
		}
		return itemsField.Index, nil
	}()
	if err != nil {
		return nil, err
	}

	return func(l L) []I {
		// The compatibility of the types has been checked above.
		// This will not panic at runtime.
		return reflect.ValueOf(l).Elem().FieldByIndex(index).Interface().([]I)
	}, nil
}

// conditionError indicates that an error originated from a [Condition]. Those
// errors will never be presented to the error callback, but terminate the watch
// immediately.
type conditionError struct{ error }

type startWatch struct {
	resourceVersion string
}

type sink[T any, PT ObjectPtr[T]] struct {
	reset  func(items []T) (done bool, err error)
	update func(item PT, deleted bool) (done bool, err error)
}

func (w *Watcher[T, PT]) runWithSink(ctx context.Context, sink sink[T, PT]) error {
	return retry(ctx, w.errorCallback, func(ctx context.Context) error {
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		return w.run(ctx, sink)
	})
}

func (w *Watcher[T, PT]) run(ctx context.Context, sink sink[T, PT]) error {
	startWatch, err := w.list(ctx, sink)
	if err != nil {
		return err
	}

	for startWatch != nil {
		startWatch, err = w.watch(ctx, startWatch.resourceVersion, sink)
		if err != nil {
			return err
		}
	}

	return nil
}

func (w *Watcher[T, PT]) list(ctx context.Context, sink sink[T, PT]) (*startWatch, error) {
	const maxListDurationSecs = 30
	ctx, cancel := context.WithTimeout(ctx, (maxListDurationSecs+10)*time.Second)
	defer cancel()
	resourceVersion, items, err := w.List(ctx, metav1.ListOptions{
		FieldSelector:  w.fieldSelector,
		LabelSelector:  w.labelSelector,
		TimeoutSeconds: ptr.To(int64(maxListDurationSecs)),
	})
	if err != nil {
		return nil, err
	}

	if done, err := sink.reset(items); err != nil {
		return nil, conditionError{err}
	} else if done {
		return nil, nil // terminate successfully
	}

	if !isResourceVersionValid(resourceVersion) {
		return nil, fmt.Errorf("list returned invalid resource version: %q", resourceVersion)
	}

	return &startWatch{resourceVersion}, nil
}

func (w *Watcher[T, PT]) watch(ctx context.Context, resourceVersion string, sink sink[T, PT]) (*startWatch, error) {
	const maxWatchDurationSecs = 120
	watcher, err := w.Watch(ctx, metav1.ListOptions{
		ResourceVersion:     resourceVersion,
		AllowWatchBookmarks: true,
		FieldSelector:       w.fieldSelector,
		LabelSelector:       w.labelSelector,
		TimeoutSeconds:      ptr.To(int64(maxWatchDurationSecs)),
	})
	if err != nil {
		return nil, err
	}
	defer watcher.Stop()

	watchTimeout := time.NewTimer((maxWatchDurationSecs + 10) * time.Second)
	defer watchTimeout.Stop()

	startWatch := &startWatch{resourceVersion}
	for startWatch != nil {
		select {
		case <-ctx.Done():
			return nil, context.Cause(ctx)

		case <-watchTimeout.C:
			return nil, apierrors.NewTimeoutError("server unexpectedly didn't close the watch", 1)

		case event, ok := <-watcher.ResultChan():
			if !ok {
				// The server closed the watch remotely.
				// This usually happens after maxWatchDurationSecs have passed.
				return startWatch, nil
			}

			startWatch, err = w.processWatchEvent(&event, sink.update)
			if err != nil {
				return nil, err
			}
		}
	}

	return nil, nil // terminate successfully
}

func (w *Watcher[T, PT]) processWatchEvent(event *watch.Event, update func(PT, bool) (bool, error)) (*startWatch, error) {
	switch event.Type {
	case watch.Added, watch.Modified, watch.Deleted, watch.Bookmark:
		item, ok := event.Object.(PT)
		if !ok {
			var example PT
			var err error = &apierrors.UnexpectedObjectError{Object: event.Object}
			return nil, fmt.Errorf("got an event of type %q, expecting %T: (%T) %w", event.Type, example, event.Object, err)
		}

		if event.Type != watch.Bookmark {
			if done, err := update(item, event.Type == watch.Deleted); err != nil {
				return nil, conditionError{err}
			} else if done {
				return nil, nil // terminate successfully
			}
		}

		if nextResourceVersion := item.GetResourceVersion(); isResourceVersionValid(nextResourceVersion) {
			return &startWatch{nextResourceVersion}, nil
		}

		return nil, fmt.Errorf("invalid resource version: %w", &apierrors.UnexpectedObjectError{Object: event.Object})

	case watch.Error:
		return nil, fmt.Errorf("watch error: %w", apierrors.FromObject(event.Object))

	default:
		return nil, fmt.Errorf("unexpected watch event (%s): %w", event.Type, apierrors.FromObject(event.Object))
	}
}

func isResourceVersionValid(resourceVersion string) bool {
	// https://github.com/kubernetes/kubernetes/issues/74022
	switch resourceVersion {
	case "", "0":
		return false
	default:
		return true
	}
}

func retry(ctx context.Context, errorCallback ErrorCallback, runWatch func(context.Context) error) error {
	for {
		err := runWatch(ctx)
		if err == nil {
			// No error means the user-specified condition returned success.
			// The watch is done.
			return nil
		}

		if condErr, ok := errors.AsType[conditionError](err); ok {
			// The user-specified condition returned an error.
			return condErr.error
		}

		if ctx.Err() != nil {
			// The context has been closed. Good bye.
			return context.Cause(ctx)
		}

		if apierrors.IsResourceExpired(err) {
			// Start over without delay (resource version too old)
			continue
		}

		// Ask the error callback about any other errors.
		if errorCallback != nil {
			retryDelay, err := errorCallback(err)
			if err != nil {
				return err
			}

			// Retry after some timeout.
			timer := time.NewTimer(retryDelay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return context.Cause(ctx)
			case <-timer.C:
				continue
			}
		}

		return err
	}
}
