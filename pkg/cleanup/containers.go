/*
Copyright 2021 k0s authors

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

package cleanup

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/k0sproject/k0s/internal/pkg/file"

	"github.com/avast/retry-go"
	"github.com/sirupsen/logrus"
	"k8s.io/mount-utils"
)

type containers struct {
	Config *Config
}

// Name returns the name of the step
func (c *containers) Name() string {
	return "containers steps"
}

// Run removes all the pods and mounts and stops containers afterwards
// Run starts containerd if custom CRI is not configured
func (c *containers) Run() (err error) {
	ctx := context.TODO()

	if !c.isCustomCriUsed() {
		containerdCtx, cancel, done, startErr := c.startContainerd()
		if startErr != nil {
			if errors.Is(startErr, fs.ErrNotExist) || errors.Is(startErr, exec.ErrNotFound) {
				logrus.Debugf("containerd binary not found. Skipping container cleanup")
				return nil
			}
			return fmt.Errorf("failed to start containerd: %w", startErr)
		}

		defer func() {
			cancel(nil)
			err = errors.Join(err, <-done)
		}()

		ctx = containerdCtx
	}

	if err := c.stopAllContainers(ctx); err != nil {
		logrus.Debugf("error stopping containers: %v", err)
	}

	// if !c.isCustomCriUsed() {
	// 	c.stopContainerd()
	// }
	return nil
}

func removeMount(path string) error {
	var errs []error

	mounter := mount.New("")
	procMounts, err := mounter.List()
	if err != nil {
		return err
	}
	for _, v := range procMounts {
		if strings.Contains(v.Path, path) {
			logrus.Debugf("Unmounting: %s", v.Path)
			if err = mounter.Unmount(v.Path); err != nil {
				errs = append(errs, err)
			}

			logrus.Debugf("Removing: %s", v.Path)
			if err := os.RemoveAll(v.Path); err != nil {
				errs = append(errs, err)
			}
		}
	}

	return errors.Join(errs...)
}

func (c *containers) isCustomCriUsed() bool {
	return c.Config.containerd == nil
}

func (c *containers) startContainerd() (context.Context, context.CancelCauseFunc, <-chan error, error) {
	logrus.Debugf("starting containerd")
	args := []string{
		fmt.Sprintf("--root=%s", filepath.Join(c.Config.dataDir, "containerd")),
		fmt.Sprintf("--state=%s", filepath.Join(c.Config.runDir, "containerd")),
		fmt.Sprintf("--address=%s", c.Config.containerd.socketPath),
	}

	confPath := c.Config.containerd.configPath
	if confPath == "" {
		confPath = "/etc/k0s/containerd.toml"
	}
	if file.Exists(confPath) {
		args = append(args, "--config", confPath)
	}

	ctx, cancel := context.WithCancelCause(context.TODO())
	cmd := exec.Command(c.Config.containerd.binPath, args...)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	if err := cmd.Start(); err != nil {
		return nil, nil, nil, err
	}

	logrus.Debugf("started containerd successfully")

	terminated := make(chan struct{})
	go func() {
		defer close(terminated)
		cancel(fmt.Errorf("process terminated: %w", cmd.Wait()))
	}()

	done := make(chan error, 1)
	go func() {
		defer close(done)
		<-ctx.Done()
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			done <- fmt.Errorf("failed to kill containerd with PID %d: %w", cmd.Process.Pid, err)
			return
		}
		<-terminated
	}()

	return ctx, cancel, done, nil
}

func (c *containers) stopAllContainers(ctx context.Context) error {
	var errs []error

	var (
		pods    []string
		lastErr error
	)
	retryErr := retry.Do(
		func() error {
			logrus.Debugf("trying to list all pods")
			pods, lastErr = c.Config.containerRuntime.ListContainers(ctx)
			return lastErr
		},
		retry.Attempts(6),
		retry.Context(ctx),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(n uint, err error) {
			logrus.WithError(err).Debugf("Failed to list containers in attempt %d, retrying after backoff", n)
		}),
	)
	if retryErr != nil {
		if lastErr == nil {
			lastErr = retryErr
		} else if errors.Is(retryErr, ctx.Err()) {
			lastErr = fmt.Errorf("%w (%w)", lastErr, context.Cause(ctx))
		}

		return fmt.Errorf("failed at listing pods: %w", lastErr)
	}
	if len(pods) > 0 {
		if err := removeMount("kubelet/pods"); err != nil {
			errs = append(errs, err)
		}
		if err := removeMount("run/netns"); err != nil {
			errs = append(errs, err)
		}
	}

	for _, pod := range pods {
		logrus.Debugf("stopping container: %v", pod)
		err := c.Config.containerRuntime.StopContainer(ctx, pod)
		if err != nil {
			if strings.Contains(err.Error(), "443: connect: connection refused") {
				// on a single node instance, we will see "connection refused" error. this is to be expected
				// since we're deleting the API pod itself. so we're ignoring this error
				logrus.Debugf("ignoring container stop err: %v", err.Error())
			} else {
				errs = append(errs, fmt.Errorf("failed to stop running pod %s: %w", pod, err))
			}
		}
		err = c.Config.containerRuntime.RemoveContainer(ctx, pod)
		if err != nil {
			errs = append(errs, fmt.Errorf("failed to remove pod %s: %w", pod, err))
		}
	}

	pods, retryErr = c.Config.containerRuntime.ListContainers(ctx)
	if retryErr == nil && len(pods) == 0 {
		logrus.Info("successfully removed k0s containers!")
	}

	if len(errs) > 0 {
		return fmt.Errorf("errors occurred while removing pods: %w", errors.Join(errs...))
	}
	return nil
}
