package cgroups_test

import (
	"context"
	"os"
	"testing"

	internallog "github.com/k0sproject/k0s/internal/pkg/log"
	"github.com/k0sproject/k0s/pkg/component/cgroups"
)

func TestXxx(t *testing.T) {

	setup := cgroups.Setup{}

	err := setup.Init(context.TODO())
	t.Error(err)
}

func TestMain(m *testing.M) {
	internallog.SetDebugLevel()
	os.Exit(m.Run())
}
