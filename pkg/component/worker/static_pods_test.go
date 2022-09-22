/*
Copyright 2022 k0s authors

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

package worker

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	"github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"
	// "fmt"
	// v1 "k8s.io/api/core/v1"
	// "k8s.io/apimachinery/pkg/runtime"
	// api "k8s.io/kubernetes/pkg/apis/core"
	// // TODO: remove this import if
	// // api.Registry.GroupOrDie(v1.GroupName).GroupVersion.String() is changed
	// // to "v1"?
	// "k8s.io/kubernetes/pkg/api/legacyscheme"
	// // Ensure that core apis are installed
	// _ "k8s.io/kubernetes/pkg/apis/core/install"
	// k8s_api_v1 "k8s.io/kubernetes/pkg/apis/core/v1"
	// "k8s.io/kubernetes/pkg/apis/core/validation"
)

const dummyPod = `
apiVersion: v1
kind: Pod
metadata:
  name: dummy-test
  namespace: default
spec:
  containers:
  - image: nginx
    name: web
    ports:
    - containerPort: 80
      name: web
      protocol: TCP
`

func TestStaticPods_Provisioning(t *testing.T) {

	underTest := NewStaticPods()

	t.Run("content_is_initlially_empty", func(t *testing.T) {
		assert.Equal(t, newList(t), getContent(t, underTest))
	})

	podUnderTest, err := underTest.ClaimStaticPod("default", "dummy-test")
	require.NoError(t, err)

	t.Run("rejects_claims", func(t *testing.T) {
		for _, test := range []struct{ test, ns, name, err string }{
			{
				"pods_without_a_name",
				"default", "",
				`invalid name: "": `,
			},
			{
				"pods_without_a_namespace",
				"", "dummy-test",
				`invalid namespace: "": `,
			},
		} {
			t.Run(test.test, func(t *testing.T) {
				_, err := underTest.ClaimStaticPod(test.ns, test.name)
				if assert.Error(t, err) {
					assert.Contains(t, err.Error(), test.err)
				}
			})
		}
	})

	t.Run("rejects", func(t *testing.T) {
		_, err = underTest.ClaimStaticPod("default", "dummy-test")
		if assert.Error(t, err) {
			assert.Equal(t, "default/dummy-test is already claimed", err.Error())
		}

		for _, test := range []struct {
			name string
			pod  interface{}
			err  string
		}{
			{
				"non_pods",
				&corev1.Pod{
					TypeMeta: metav1.TypeMeta{APIVersion: "v1", Kind: "Secret"},
				},
				"not a Pod: v1/Secret",
			},
			{
				"pods_not_matching_the_claim",
				&corev1.Pod{
					TypeMeta:   metav1.TypeMeta{APIVersion: "v1", Kind: "Pod"},
					ObjectMeta: metav1.ObjectMeta{Namespace: "foo", Name: "bar"},
				},
				`attempt to set the manifest to "foo/bar", whereas "default/dummy-test" was claimed`,
			},
			{
				"unknown_fields",
				`{"apiVersion": "v1", "kind": "Pod", "spec":{"foo": "bar"}}`,
				`error unmarshaling JSON: while decoding JSON: json: unknown field "foo"`,
			},
		} {
			t.Run(test.name, func(t *testing.T) {
				err := podUnderTest.SetManifest(test.pod)
				if assert.Error(t, err) {
					assert.Equal(t, test.err, err.Error())
				}
				assert.Equal(t, newList(t), getContent(t, underTest))
			})
		}
	})

	t.Run("accepts", func(t *testing.T) {
		expected := newList(t, []byte(dummyPod))

		for _, test := range []struct {
			name string
			pod  interface{}
		}{
			{"bytes", []byte(dummyPod)},
			{"strings", dummyPod},
		} {
			t.Run(test.name, func(t *testing.T) {
				assert.NoError(t, podUnderTest.SetManifest(test.pod))
				assert.Equal(t, expected, getContent(t, underTest))
			})
		}
	})

	t.Run("sets_pod_manifests", func(t *testing.T) {
		replaced := `{"apiVersion":"v1","kind":"Pod","metadata":{"name":"dummy-test","namespace":"default"},"spec":{"containers":[{"name":"alpine","image":"alpine"}]}}`
		expected := newList(t, []byte(replaced))

		assert.NoError(t, podUnderTest.SetManifest(dummyPod))
		assert.NoError(t, podUnderTest.SetManifest(replaced))

		assert.Equal(t, expected, getContent(t, underTest))

		podUnderTest.Clear()

		assert.Equal(t, newList(t), getContent(t, underTest))

		assert.NoError(t, podUnderTest.SetManifest(replaced))

		assert.Equal(t, expected, getContent(t, underTest))
	})

	t.Run("drops_pods", func(t *testing.T) {
		podUnderTest.Drop()
		assert.Equal(t, newList(t), getContent(t, underTest))
		err := podUnderTest.SetManifest(dummyPod)
		if assert.Error(t, err) {
			assert.Equal(t, "already dropped", err.Error())
		}
		assert.Equal(t, newList(t), getContent(t, underTest))
	})
}

func TestStaticPods_Lifecycle(t *testing.T) {
	log, logs := test.NewNullLogger()
	log.SetLevel(logrus.DebugLevel)

	underTest := NewStaticPods().(*staticPods)
	underTest.log = log
	podUnderTest, err := underTest.ClaimStaticPod("default", "dummy-test")
	require.NoError(t, err)
	assert.NoError(t, podUnderTest.SetManifest(dummyPod))

	t.Run("url_is_unavailable_before_init", func(t *testing.T) {
		_, err := underTest.ManifestURL()
		require.Error(t, err)
		assert.Equal(t, "static_pods component is not yet running", err.Error())
	})

	t.Run("fails_to_run_without_init", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		require.Equal(t, "static_pods component is not yet initialized", err.Error())
	})

	t.Run("health_check_fails_without_init", func(t *testing.T) {
		err := underTest.Healthy()
		require.Error(t, err)
		require.Equal(t, "static_pods component is not yet running", err.Error())
	})

	t.Run("fails_to_stop_without_init", func(t *testing.T) {
		err := underTest.Stop()
		require.Error(t, err)
		require.Equal(t, "static_pods component is not yet running", err.Error())
	})

	t.Run("init", func(t *testing.T) {
		require.NoError(t, underTest.Init(context.TODO()))
	})

	t.Run("another_init_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		if assert.Error(t, err) {
			assert.Equal(t, "static_pods component is already initialized", err.Error())
		}
	})

	t.Run("url_is_unavailable_after_init", func(t *testing.T) {
		_, err := underTest.ManifestURL()
		require.Error(t, err)
		assert.Equal(t, "static_pods component is not yet running", err.Error())
	})

	t.Run("health_check_fails_before_run", func(t *testing.T) {
		err := underTest.Healthy()
		require.Error(t, err)
		require.Equal(t, "static_pods component is not yet running", err.Error())
	})

	t.Run("stop_before_run_fails", func(t *testing.T) {
		err := underTest.Stop()
		require.Error(t, err)
		assert.Equal(t, "static_pods component is not yet running", err.Error())
	})

	ctx, cancel := context.WithCancel(context.TODO())
	t.Cleanup(cancel)

	t.Run("runs", func(runT *testing.T) {
		if assert.NoError(runT, underTest.Start(ctx)) {
			t.Cleanup(func() { assert.NoError(t, underTest.Stop()) })

			var lastLog *logrus.Entry
			require.NoError(t, retry.Do(func() error {
				lastLog = logs.LastEntry()
				if lastLog == nil {
					return errors.New("not yet logged")
				}
				return nil
			}, retry.Attempts(5)))

			assert.Equal(t, "Serving HTTP requests", lastLog.Message)
			assert.Contains(t, lastLog.Data["local_addr"], "127.0.0.1")
		}
	})

	t.Run("another_run_fails", func(t *testing.T) {
		err := underTest.Start(ctx)
		require.Error(t, err)
		assert.Equal(t, "static_pods component is already running", err.Error())
	})

	t.Run("health_check_works", func(t *testing.T) {
		err := underTest.Healthy()
		assert.NoError(t, err)
		lastLog := logs.LastEntry()
		require.Equal(t, "Answering health check", lastLog.Message)
		assert.Contains(t, lastLog.Data["local_addr"], "127.0.0.1")
		assert.Contains(t, lastLog.Data["remote_addr"], "127.0.0.1")
	})

	t.Run("serves_content", func(t *testing.T) {
		dummyPod, err := yaml.YAMLToJSON([]byte(dummyPod))
		require.NoError(t, err)
		expectedContent := `{"apiVersion":"v1","kind":"PodList","items":[` + string(dummyPod) + "]}"

		url, err := underTest.ManifestURL()
		require.NoError(t, err)

		req, err := http.NewRequest(http.MethodGet, url, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		t.Cleanup(cancel)

		req = req.WithContext(ctx)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		t.Cleanup(func() { assert.NoError(t, resp.Body.Close()) })

		assert.Equal(t, resp.StatusCode, http.StatusOK)

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.JSONEq(t, expectedContent, string(body))

		lastLog := logs.LastEntry()
		require.NotNil(t, lastLog)
		assert.Contains(t, lastLog.Message, "Writing content: ")
		assert.Contains(t, lastLog.Data["local_addr"], "127.0.0.1")
		assert.Contains(t, lastLog.Data["remote_addr"], "127.0.0.1")
	})

	t.Run("stops", func(t *testing.T) {
		require.NoError(t, underTest.Stop())
	})

	t.Run("health_check_fails_after_stopped", func(t *testing.T) {
		err := underTest.Healthy()
		require.Error(t, err)
		assert.Contains(t, err.Error(), "connection refused")
	})

	t.Run("does_not_serve_content_anymore", func(t *testing.T) {
		url, err := underTest.ManifestURL()
		require.NoError(t, err)

		req, err := http.NewRequest("GET", url, nil)
		require.NoError(t, err)

		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		t.Cleanup(cancel)

		resp, err := http.DefaultClient.Do(req.WithContext(ctx))
		if assert.Error(t, err) {
			assert.Contains(t, err.Error(), "connection refused")
		} else {
			assert.NoError(t, resp.Body.Close())
		}
	})

	t.Run("stop_may_be_called_again", func(t *testing.T) {
		require.NoError(t, underTest.Stop())
	})

	t.Run("claimed_pod_may_be_dropped_after_stop", func(t *testing.T) {
		podUnderTest.Drop()
	})

	t.Run("reinit_fails", func(t *testing.T) {
		err := underTest.Init(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "static_pods component is already stopped", err.Error())
	})

	t.Run("rerun_fails", func(t *testing.T) {
		err := underTest.Start(context.TODO())
		require.Error(t, err)
		assert.Equal(t, "static_pods component is already stopped", err.Error())
	})
}

func getContent(t *testing.T, underTest StaticPods) (content map[string]interface{}) {
	// parsed, _, err := tryDecodePodList(underTest.(*staticPods).content(), func(pod *api.Pod) error { return nil })
	// assert.NoError(t, err)
	// assert.True(t, parsed)

	require.NoError(t, yaml.Unmarshal(underTest.(*staticPods).content(), &content))
	return
}

func newList(t *testing.T, items ...[]byte) map[string]interface{} {
	parsedItems := []interface{}{}
	for _, item := range items {
		var parsedItem map[string]interface{}
		require.NoError(t, yaml.Unmarshal(item, &parsedItem))
		parsedItems = append(parsedItems, parsedItem)
	}

	return map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "PodList",
		"items":      parsedItems,
	}
}

// type defaultFunc func(pod *api.Pod) error

// func tryDecodePodList(data []byte, defaultFn defaultFunc) (parsed bool, pods v1.PodList, err error) {
// 	obj, err := runtime.Decode(legacyscheme.Codecs.UniversalDecoder(), data)
// 	if err != nil {
// 		return false, pods, err
// 	}

// 	newPods, ok := obj.(*api.PodList)
// 	// Check whether the object could be converted to list of pods.
// 	if !ok {
// 		err = fmt.Errorf("invalid pods list: %#v", obj)
// 		return false, pods, err
// 	}

// 	// Apply default values and validate pods.
// 	for i := range newPods.Items {
// 		newPod := &newPods.Items[i]
// 		if err = defaultFn(newPod); err != nil {
// 			return true, pods, err
// 		}
// 		if errs := validation.ValidatePodCreate(newPod, validation.PodValidationOptions{}); len(errs) > 0 {
// 			err = fmt.Errorf("invalid pod: %v", errs)
// 			return true, pods, err
// 		}
// 	}
// 	v1Pods := &v1.PodList{}
// 	if err := k8s_api_v1.Convert_core_PodList_To_v1_PodList(newPods, v1Pods, nil); err != nil {
// 		return true, pods, err
// 	}
// 	return true, *v1Pods, err
// }
