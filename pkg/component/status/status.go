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

package status

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/k0sproject/k0s/internal/pkg/dir"
	"github.com/k0sproject/k0s/pkg/component/manager"
	"github.com/k0sproject/k0s/pkg/component/prober"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

type Stater interface {
	State(maxCount int) prober.State
}
type Status struct {
	StatusInformation K0sStatus
	Prober            Stater
	Socket            string
	L                 *logrus.Entry
	GetWorkerClient   func() (kubernetes.Interface, error)

	httpserver http.Server
}

var _ manager.Component = (*Status)(nil)

const defaultMaxEvents = 5

// Init initializes component
func (s *Status) Init(ctx context.Context) error {
	s.L = logrus.WithFields(logrus.Fields{"component": "status"})

	if err := dir.Init(s.StatusInformation.K0sVars.RunDir, 0755); err != nil {
		return fmt.Errorf("failed to create %s: %w", s.Socket, err)
	}

	mux := http.NewServeMux()
	mux.Handle("/status", http.HandlerFunc(s.serveStatus))
	mux.HandleFunc("/components", http.HandlerFunc(s.serveComponents))
	s.httpserver = http.Server{Handler: mux}

	return s.removeLeftovers(ctx)
}

// removeLeftovers tries to remove leftover sockets that nothing is listening on
func (s *Status) removeLeftovers(ctx context.Context) error {
	// FIXME test

	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", s.Socket)
	if err != nil {
		if errors.Is(err, ctx.Err()) {
			return err
		}

		if err := os.Remove(s.Socket); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.L.WithError(err).Warn("Failed to remove socket")
		}

		return nil
	}

	defer conn.Close()
	s.L.WithError(err).Warn("Something is listening on the socket already")

	return nil
}

// Start runs the component
func (s *Status) Start(context.Context) error {
	listener, err := net.Listen("unix", s.Socket)
	if err != nil {
		return fmt.Errorf("failed to create listener: %w", err)
	}

	s.L.Info("Listening on ", s.Socket)

	go func() {
		if err := s.httpserver.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.L.WithError(err).Error("Failed to serve")
		}
	}()
	return nil
}

// Stop stops status component and removes the unix socket
func (s *Status) Stop() error {
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()
	// Unix socket doesn't need to be explicitly removed because it's handled
	// by httpserver.Shutdown
	return s.httpserver.Shutdown(ctx)
}

func (s *Status) serveStatus(w http.ResponseWriter, r *http.Request) {
	status := s.StatusInformation
	status.WorkerToAPIConnectionStatus = s.getWorkerStatus(r.Context())
	s.sendJSON(w, &status)
}

func (s *Status) getWorkerStatus(ctx context.Context) *ProbeStatus {
	if !s.StatusInformation.Workloads {
		return nil
	}

	client, err := s.GetWorkerClient()
	if err != nil {
		return &ProbeStatus{Message: "failed to obtain worker client: " + err.Error()}
	}

	if _, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{}); err != nil {
		return &ProbeStatus{Message: err.Error()}
	}

	return &ProbeStatus{Success: true}
}

func (s *Status) serveComponents(w http.ResponseWriter, r *http.Request) {
	maxCount := defaultMaxEvents
	if r.URL.Query().Has("maxCount") {
		parsed, err := strconv.ParseUint(r.URL.Query().Get("maxCount"), 10, 8)
		if err != nil {
			s.sendError(w, err, &problem{Title: "Query parameter invalid: maxCount", Status: http.StatusBadRequest})
			return
		} else {
			maxCount = int(parsed)
		}
	}

	s.sendJSON(w, s.Prober.State(maxCount))
}

func (s *Status) sendJSON(w http.ResponseWriter, data any) {
	body, err := json.Marshal(&data)
	if err != nil {
		s.sendError(w, err, &problem{Title: "Failed to marshal data", Status: http.StatusInternalServerError})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := w.Write(body); err != nil {
		s.L.WithError(err).Debug("Failed to send body")
	}
}

func (s *Status) sendError(w http.ResponseWriter, err error, p *problem) {
	log := s.L

	if p.Status >= http.StatusInternalServerError {
		if uuid, uuidErr := uuid.NewRandom(); uuidErr != nil {
			log.WithError(errors.Join(err, uuidErr)).Error(p.Title)
		} else {
			p.Instance = uuid.String()
			log = log.WithField("instance", p.Instance)
			log.Error(p.Title)
		}
	} else {
		p.Detail = err.Error()
	}

	body, err := json.Marshal(*p)
	if err != nil {
		log.WithError(err).Error("Failed to marshal problem response")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(p.Status)
	if _, err := w.Write(body); err != nil {
		log.WithError(err).Info("Failed to send body")
	}
}

type problem struct {
	Title    string `json:"title,omitempty"`
	Status   int    `json:"status,omitempty"`
	Detail   string `json:"detail,omitempty"`
	Instance string `json:"instance,omitempty"`
}
