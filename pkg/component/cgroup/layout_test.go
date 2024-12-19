package cgroup_test

import (
	"context"
	"os"
	"testing"

	internallog "github.com/k0sproject/k0s/internal/pkg/log"
	"github.com/k0sproject/k0s/pkg/component/cgroup"

	"github.com/stretchr/testify/assert"
)

func TestXxx(t *testing.T) {

	layout := cgroup.Layout{}

	err := layout.Init(context.TODO())
	assert.NoError(t, err)
}

func TestMain(m *testing.M) {
	internallog.SetDebugLevel()
	os.Exit(m.Run())
}
