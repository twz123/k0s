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

package nllb

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/k0sproject/k0s/internal/pkg/file"
	"github.com/k0sproject/k0s/pkg/constant"
	"github.com/k0sproject/k0s/pkg/debounce"
	"github.com/sirupsen/logrus"

	"k8s.io/client-go/tools/clientcmd"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
)

type fileStat struct {
	size    int64
	modTime time.Time
}

type fileStatInterface interface {
	Size() int64
	ModTime() time.Time
}

var _ fileStatInterface = (*fileStat)(nil)

func (s *fileStat) Size() int64        { return s.size }
func (s *fileStat) ModTime() time.Time { return s.modTime }
func (s *fileStat) Equal(other fileStatInterface) bool {
	return s != nil && other != nil && s.size == other.Size() && s.modTime.Equal(other.ModTime())
}

type kubeconfig[T any] struct{ regular, loadBalanced T }

type kubeconfigPath kubeconfig[string]
type kubeconfigStat kubeconfig[*fileStat]

type kubeconfigs[T any] struct{ regular, bootstrap T }
type kubeconfigPaths kubeconfigs[kubeconfigPath]
type kubeconfigStats kubeconfigs[*kubeconfigStat]

func (s *fileStat) String() string {
	if s == nil {
		return "(absent)"
	}
	return fmt.Sprintf("%d bytes, modified %s", s.size, s.modTime)
}

func (s *kubeconfigStat) String() string {
	if s == nil {
		return "(absent)"
	}
	return fmt.Sprintf("(regular: %s; load balanced: %s)", s.regular, s.loadBalanced)
}

func (s *kubeconfigStats) String() string {
	if s == nil {
		return "(absent)"
	}
	return fmt.Sprintf("(regular: %s; bootstrap: %s)", s.regular, s.bootstrap)
}

func (p *kubeconfigPaths) loadBalancedPaths() []string {
	return []string{p.regular.loadBalanced, p.bootstrap.loadBalanced}
}

func (p *kubeconfigPaths) reconcile(ctx context.Context, log logrus.FieldLogger, lbAddr string) error {
	if ctx.Err() != nil {
		return nil // The context is already done.
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %w", err)
	}
	defer watcher.Close()

	debounceCtx, cancelDebouncer := context.WithCancel(ctx)
	defer cancelDebouncer()

	watchedPaths := []string{
		p.regular.regular, p.regular.loadBalanced,
		p.bootstrap.regular, p.bootstrap.loadBalanced,
	}
	for i := range watchedPaths {
		watchedPaths[i] = filepath.Clean(watchedPaths[i])
	}

	debouncer := debounce.Debouncer[fsnotify.Event]{
		Input:   watcher.Events,
		Timeout: 5 * time.Second,
		Filter: func(item fsnotify.Event) bool {
			for _, path := range watchedPaths {
				if filepath.Clean(item.Name) == path {
					log.Debug("Accepting file system event: ", item)
					return true
				}
			}

			log.Debug("Ignoring file system event: ", item)
			return false
		},
		Callback: func() func(fsnotify.Event) {
			var desiredStats kubeconfigStats
			var mu sync.Mutex
			return func(fsnotify.Event) {
				mu.Lock()
				defer mu.Unlock()
				desiredStats = p.synchronizeFiles(log, lbAddr, desiredStats)
			}
		}(),
	}

	// Consume and forward any errors.
	go func() {
		for {
			err, ok := <-watcher.Errors
			if !ok {
				return
			}
			log.WithError(err).Error("Error while watching kubeconfig files")
		}
	}()

	{
		dirs := make(map[string]struct{})
		for _, path := range watchedPaths {
			dirs[filepath.Dir(path)] = struct{}{}
		}

		for dir := range dirs {
			err = watcher.Add(dir)
			if err != nil {
				return fmt.Errorf("failed to watch %q: %w", dir, err)
			}
		}
	}

	// Reconcile once, no matter what happens on the fs
	debouncer.Callback(fsnotify.Event{})

	_ = debouncer.Run(debounceCtx)
	return nil
}

func (p *kubeconfigPaths) synchronizeFiles(log logrus.FieldLogger, host string, stats kubeconfigStats) kubeconfigStats {
	if !p.regular.statMatches(stats.regular) {
		stat := p.regular.synchronizeFiles(log, host)
		stats.regular = &stat
	} else {
		log.Debug("Skipped synchronization of regular kubeconfig: ", stats.regular)
	}
	if !p.bootstrap.statMatches(stats.bootstrap) {
		stat := p.bootstrap.synchronizeFiles(log, host)
		stats.bootstrap = &stat
	} else {
		log.Debug("Skipped synchronization of bootstrap kubeconfig: ", stats.regular)
	}
	return stats
}

func (p *kubeconfigPath) statMatches(desired *kubeconfigStat) bool {
	if desired == nil {
		return false
	}
	return statMatches(p.regular, desired.regular) && statMatches(p.loadBalanced, desired.loadBalanced)
}

func statMatches(path string, desired *fileStat) bool {
	actual, err := os.Stat(path)
	if err != nil {
		return false
	}

	return desired != nil && !desired.Equal(actual)
}

const lbContext = "k0s-node-local-load-balanced"

func (p *kubeconfigPath) synchronizeFiles(log logrus.FieldLogger, host string) (s kubeconfigStat) {
	var regularStat, lbStat fileStatInterface
	var kubeconfig, lbKubeconfig *clientcmdapi.Config
	var regularErr, lbErr error

	regularStat, kubeconfig, regularErr = loadKubeconfigFile(p.regular)
	lbStat, lbKubeconfig, lbErr = loadKubeconfigFile(p.loadBalanced)

	if regularErr != nil && lbErr != nil {
		for _, err := range []error{regularErr, lbErr} {
			if !os.IsNotExist(err) {
				log.WithError(err).Error("Failed to read kubeconfig file")
			}
		}

		return
	}

	if lbErr != nil || (regularErr == nil && !lbStat.ModTime().After(regularStat.ModTime())) {
		lbKubeconfig, lbErr = regularToLoadBalanced(kubeconfig, host)
		if lbErr != nil {
			lbErr = fmt.Errorf("failed to generate load balanced kubeconfig from regular one: %w", lbErr)
		}
	} else {
		kubeconfig, regularErr = loadBalancedToRegular(lbKubeconfig, host)
		if regularErr != nil {
			regularErr = fmt.Errorf("failed to generate regular kubeconfig from load balanced one: %w", regularErr)
		}

		lbErr = createLoadBalancedContext(lbKubeconfig, host)
		if lbErr != nil {
			lbErr = fmt.Errorf("failed to update load balanced kubeconfig: %w", lbErr)
		}
	}

	writeFile := func(kubeconfig *clientcmdapi.Config, path string) *fileStat {
		bytes, err := clientcmd.Write(*kubeconfig)
		if err != nil {
			log.WithError(regularErr).Errorf("Failed to update %q", path)
			return nil
		}

		stat := new(fileStat)
		stat.size, stat.modTime, err = file.WriteContentAtomically(path, bytes, constant.CertSecureMode)
		if err != nil {
			log.WithError(regularErr).Errorf("Failed to update %q", path)
			return nil
		}

		log.Infof("Updated %q: %s", path, stat)
		return stat
	}

	if regularErr == nil {
		writeFile(kubeconfig, p.regular)
	} else {
		log.WithError(regularErr).Error("Failed to synchronize regular kubeconfig")
	}
	if lbErr == nil {
		writeFile(lbKubeconfig, p.loadBalanced)
	} else {
		log.WithError(lbErr).Error("Failed to synchronize load balanced kubeconfig")
	}

	return
}

func regularToLoadBalanced(kubeconfig *clientcmdapi.Config, host string) (*clientcmdapi.Config, error) {
	lbKubeconfig := kubeconfig.DeepCopy()
	if err := clientcmdapi.MinifyConfig(lbKubeconfig); err != nil {
		return nil, err
	}
	if err := createLoadBalancedContext(lbKubeconfig, host); err != nil {
		return nil, err
	}

	return lbKubeconfig, nil
}

func loadBalancedToRegular(lbKubeconfig *clientcmdapi.Config, lbName string) (*clientcmdapi.Config, error) {
	var err error
	kubeconfig := lbKubeconfig.DeepCopy()
	kubeconfig.CurrentContext = ""
	for candidate := range kubeconfig.Contexts {
		if candidate == lbContext {
			continue
		}
		if kubeconfig.CurrentContext != "" {
			err = fmt.Errorf("too many contexts in load balanced kubeconfig: %s, %s", kubeconfig.CurrentContext, candidate)
			break
		}
		kubeconfig.CurrentContext = candidate
	}
	if kubeconfig.CurrentContext == "" {
		err = errors.New("no usable contexts in load balanced kubeconfig")
	}
	if err == nil {
		err = clientcmdapi.MinifyConfig(kubeconfig)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to generate regular kubeconfig from load balanced kubeconfig: %w", err)
	}

	return kubeconfig, nil
}

func createLoadBalancedContext(kubeconfig *clientcmdapi.Config, host string) error {
	if len(kubeconfig.CurrentContext) < 1 {
		return errors.New("current-context unspecified")
	}
	ctx, ok := kubeconfig.Contexts[kubeconfig.CurrentContext]
	if !ok {
		return fmt.Errorf("current-context not found: %q", kubeconfig.CurrentContext)
	}
	cluster, ok := kubeconfig.Clusters[ctx.Cluster]
	if !ok {
		return fmt.Errorf("cluster not found: %q", ctx.Cluster)
	}

	loadBalancedCtx := ctx.DeepCopy()
	loadBalancedCluster := cluster.DeepCopy()
	server, err := url.Parse(loadBalancedCluster.Server)
	if err != nil {
		return fmt.Errorf("invalid server: %w", err)
	}
	server.Host = host
	loadBalancedCluster.Server = server.String()
	loadBalancedCtx.Cluster = lbContext
	kubeconfig.Clusters[lbContext] = loadBalancedCluster
	kubeconfig.Contexts[lbContext] = loadBalancedCtx
	kubeconfig.CurrentContext = lbContext
	return nil
}

func loadKubeconfigFile(path string) (fs.FileInfo, *clientcmdapi.Config, error) {
	stat, err := os.Stat(path)
	if err != nil {
		return nil, nil, err
	}
	kubeconfig, err := (&clientcmd.ClientConfigLoadingRules{ExplicitPath: path}).Load()
	if err != nil {
		return nil, nil, err
	}
	return stat, kubeconfig, err
}
