package clusterconfig

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"

	corev1 "k8s.io/api/core/v1"
	eventsv1 "k8s.io/api/events/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/tools/events"

	"github.com/sirupsen/logrus"
)

type ReconciliationGroup struct {
	Log       logrus.FieldLogger
	NodeName  string
	EventSink events.EventSink
	Peek      func(*v1beta1.ClusterConfig)

	mu      sync.Mutex
	current *currentConfig
	expired chan struct{}
	slots   []string
}

type Current struct {
	Spec       k0sv1beta1.ClusterSpec
	Reconciled func(error)
}

type currentConfig struct {
	config k0sv1beta1.ClusterConfig

	mu          sync.Mutex
	remaining   uint
	slots       []*error
	cancelEvent context.CancelFunc
}

func (g *ReconciliationGroup) Update(ctx context.Context, config *k0sv1beta1.ClusterConfig) {
	// Validate incoming configuration.
	if err := errors.Join(config.Validate()...); err != nil {
		logrus.WithFields(logrus.Fields{
			"component":     "configsource.ReconciliationGroup",
			logrus.ErrorKey: err,
		}).Errorf(
			"Failed to validate cluster configuration (resource version %q)",
			config.ResourceVersion,
		)

		g.reportEvent(ctx, config, "Validation", err.Error(), corev1.EventTypeWarning)
		return
	}

	// Create new current configuration.
	var current currentConfig
	config.DeepCopyInto(&current.config)

	// Replace the current configuration and prepare its slots.
	g.mu.Lock()
	len := len(g.slots)
	current.remaining = uint(len)
	current.slots = make([]*error, len)
	expired := g.expired
	g.current, g.expired = &current, nil
	g.mu.Unlock()

	// Notify all the callers that the previous configuration expired.
	if expired != nil {
		close(expired)
	}
}

func (g *ReconciliationGroup) Slot(name string) *Slot {
	var current *currentConfig

	// Add the slot if it doesn't exist already.
	g.mu.Lock()
	idx := slices.Index(g.slots, name)
	if idx < 0 {
		idx = len(g.slots)
		g.slots = append(g.slots, name)
		current = g.current
	}
	g.mu.Unlock()

	// If this is a new slot, increment the number of expected slots.
	if current != nil {
		current.mu.Lock()
		if current.cancelEvent != nil {
			current.cancelEvent()
			current.cancelEvent = nil
		}
		current.remaining++
		current.slots = append(current.slots, nil)
		current.mu.Unlock()
	}

	return &Slot{g, idx}
}

type Slot struct {
	group *ReconciliationGroup
	idx   int
}

func (s *Slot) CurrentConfig() (*Current, <-chan struct{}) {
	group, idx := s.group, s.idx

	group.mu.Lock()
	current, expired := group.current, group.expired
	if expired == nil {
		expired = make(chan struct{})
		group.expired = expired
	}
	group.mu.Unlock()

	if current == nil {
		return nil, expired
	}

	var config Current
	current.config.Spec.DeepCopyInto(&config.Spec)
	config.Reconciled = func(err error) { group.reconciled(current, idx, err) }
	return &config, expired
}

func (g *ReconciliationGroup) reconciled(current *currentConfig, idx int, err error) {
	current.mu.Lock()
	defer current.mu.Unlock()

	if current.slots[idx] != nil {
		return
	}
	current.slots[idx] = &err
	current.remaining--

	// TODO: Maybe use event series and report every change.
	if current.remaining == 0 {
		// Reslice to prevent data races when adding new slots concurrently.
		slots := current.slots[:]

		// Cancel any pending update.
		if current.cancelEvent != nil {
			current.cancelEvent()
		}

		// Setup a context for the event submission.
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		current.cancelEvent = cancel

		go func() {
			defer cancel()
			g.reportRecoResult(ctx, &current.config, slots)
		}()
	}
}

func (g *ReconciliationGroup) reportRecoResult(ctx context.Context, config *k0sv1beta1.ClusterConfig, slots []*error) {
	var msg strings.Builder

	// Collect the error messages, if any.
	for _, slot := range slots {
		if *slot != nil {
			if msg.Len() > 0 {
				msg.WriteString("; ")
			}
			msg.WriteString((*slot).Error())
		}
	}

	// Determine the event type.
	var note, eventType string
	if msg.Len() > 0 {
		note = msg.String()
		eventType = corev1.EventTypeWarning
	} else {
		note = "Reconciliation done"
		eventType = corev1.EventTypeNormal
	}

	g.reportEvent(ctx, config, "Reconciliation", note, eventType)
}

func (g *ReconciliationGroup) reportEvent(ctx context.Context, config *v1beta1.ClusterConfig, action, note, eventType string) {
	now := time.Now()

	if _, err := g.EventSink.Create(ctx, &eventsv1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fmt.Sprintf("%v.%x", config.Name, now.UnixNano()),
			Namespace: config.Namespace,
		},
		EventTime:           metav1.MicroTime{Time: now},
		ReportingController: v1beta1.GroupName + "/controller",
		ReportingInstance:   g.NodeName,
		Action:              action,
		Reason:              "Cluster configuration reconciliation",
		Regarding: corev1.ObjectReference{
			APIVersion:      config.APIVersion,
			Kind:            config.Kind,
			Name:            config.Name,
			Namespace:       config.Namespace,
			UID:             config.UID,
			ResourceVersion: config.ResourceVersion,
		},
		Note: note,
		Type: eventType,
	}); err != nil {
		g.Log.WithError(err).Warnf(
			"Failed to create event for ClusterConfig validation error (resource version %q)",
			config.ResourceVersion,
		)
	}
}
