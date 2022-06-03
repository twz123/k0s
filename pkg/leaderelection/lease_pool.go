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
package leaderelection

import (
	"context"
	"time"

	"github.com/cloudflare/cfssl/log"
	"github.com/k0sproject/k0s/internal/pkg/sysinfo/machineid"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

// The LeasePool represents a single lease accessed by multiple clients (considered part of the "pool")
type LeasePool struct {
	name   string
	client kubernetes.Interface
	config LeaseConfiguration
}

// LeaderCallback is the function that will be called whenever the lease pool
// gets the lease. The given context will be cancelled when the lease is lost.
type LeaderCallback = func(context.Context)

// The LeaseConfiguration allows passing through various options to customize the lease.
type LeaseConfiguration struct {
	log           logrus.FieldLogger
	identity      string
	namespace     string
	duration      time.Duration
	renewDeadline time.Duration
	retryPeriod   time.Duration
}

// A LeaseOpt is a function that modifies a LeaseConfiguration
type LeaseOpt = func(config LeaseConfiguration) LeaseConfiguration

// WithLogger allows the consumer to pass a different logrus entry with additional context
func WithLogger(logger logrus.FieldLogger) LeaseOpt {
	return func(config LeaseConfiguration) LeaseConfiguration {
		config.log = logger
		return config
	}
}

// WithIdentity sets the identity of the lease holder
func WithIdentity(identity string) LeaseOpt {
	return func(config LeaseConfiguration) LeaseConfiguration {
		config.identity = identity
		return config
	}
}

// WithNamespace specifies which namespace the lease should be created in, defaults to kube-node-lease
func WithNamespace(namespace string) LeaseOpt {
	return func(config LeaseConfiguration) LeaseConfiguration {
		config.namespace = namespace
		return config
	}
}

// WithDuration sets the duration of the lease (for new leases)
func WithDuration(duration time.Duration) LeaseOpt {
	return func(config LeaseConfiguration) LeaseConfiguration {
		config.duration = duration
		return config
	}
}

// WithRenewDeadline sets the renew deadline of the lease
func WithRenewDeadline(deadline time.Duration) LeaseOpt {
	return func(config LeaseConfiguration) LeaseConfiguration {
		config.renewDeadline = deadline
		return config
	}
}

// WithRetryPeriod specifies the retry period of the lease
func WithRetryPeriod(retryPeriod time.Duration) LeaseOpt {
	return func(config LeaseConfiguration) LeaseConfiguration {
		config.retryPeriod = retryPeriod
		return config
	}
}

// NewLeasePool creates a new LeasePool struct to interact with a lease
func NewLeasePool(client kubernetes.Interface, name string, opts ...LeaseOpt) (*LeasePool, error) {
	leaseConfig := LeaseConfiguration{
		duration:      60 * time.Second,
		renewDeadline: 15 * time.Second,
		retryPeriod:   5 * time.Second,
		namespace:     "kube-node-lease",
	}

	for _, opt := range opts {
		leaseConfig = opt(leaseConfig)
	}

	if leaseConfig.log == nil {
		leaseConfig.log = logrus.StandardLogger().
			WithField("component", "lease_pool").
			WithField("namespace", leaseConfig.namespace).
			WithField("name", name)
	}

	// we default to the machine ID unless the user explicitly sets an identity
	if leaseConfig.identity == "" {
		machineID, err := machineid.Generate()

		if err != nil {
			return nil, err
		}

		leaseConfig.identity = machineID.ID()
	}

	return &LeasePool{
		name:   name,
		client: client,
		config: leaseConfig,
	}, nil
}

// Run is the primary function of LeasePool, running the leader election process.
func (p *LeasePool) Run(ctx context.Context, callback LeaderCallback) error {
	lock := &resourcelock.LeaseLock{
		LeaseMeta: metav1.ObjectMeta{
			Name:      p.name,
			Namespace: p.config.namespace,
		},
		Client: p.client.CoordinationV1(),
		LockConfig: resourcelock.ResourceLockConfig{
			Identity: p.config.identity,
		},
	}

	lec := leaderelection.LeaderElectionConfig{
		Lock:            lock,
		ReleaseOnCancel: true,
		LeaseDuration:   p.config.duration,
		RenewDeadline:   p.config.renewDeadline,
		RetryPeriod:     p.config.retryPeriod,
		Callbacks: leaderelection.LeaderCallbacks{
			OnStartedLeading: func(ctx context.Context) {
				log.Info("Acquired leader lease")
				go callback(ctx)
			},
			OnStoppedLeading: func() {
				log.Info("Lost leader lease")
			},
			OnNewLeader: nil,
		},
	}
	le, err := leaderelection.NewLeaderElector(lec)
	if err != nil {
		return err
	}
	if lec.WatchDog != nil {
		lec.WatchDog.SetLeaderElection(le)
	}

	for ctx.Err() == nil {
		logrus.Info("XOXO")
		le.Run(ctx)
	}

	return nil
}
