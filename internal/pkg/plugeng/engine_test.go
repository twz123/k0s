package plugeng

import (
	"context"
	"os"
	"testing"

	logtest "github.com/sirupsen/logrus/hooks/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestYolo(t *testing.T) {
	// code, err := os.ReadFile("../../../hack/yolo-plugin/target/wasm32-wasi/debug/yolo-plugin.wasm")
	code, err := os.ReadFile("../../../hack/demo-plugins/rust/target/wasm32-wasi/release/http-get.wasm")

	require.NoError(t, err)

	log, logs := logtest.NewNullLogger()

	underTest := Executor{
		Code: code,
		FS:   nil,
		Log:  log,
	}

	if exitCode, err := underTest.DoThatStuff(context.TODO()); assert.NoError(t, err) {
		assert.Zero(t, exitCode)
	}

	for _, e := range logs.AllEntries() {
		t.Log(e.Message)
	}

	// if entries := logs.AllEntries(); assert.Len(t, entries, 1, "Expected exactly one log line") {
	// 	entry := entries[0]
	// 	assert.Equal(t, entry.Data["stream"], "stdout")
	// 	assert.Equal(t, entry.Message, "YOLO! (I mean it!)")
	// }
}
