package k0sctl

import (
	"fmt"
	"io"
	"os/exec"

	"github.com/pulumi/pulumi-command/sdk/go/command/local"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func Apply(ctx *pulumi.Context, k0sctlConfig pulumi.StringInput) error {
	x := pulumi.NewStringAsset("foo")
	x.Path()

	k0sctl, err := exec.LookPath("k0sctl")
	if err != nil {
		return fmt.Errorf("k0sctl not in PATH: %w", err)
	}

	cmd := exec.Command(k0sctl, "version")
	cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to execute k0sctl: %w", err)
	}

	apply, err := local.NewCommand(ctx, "k0sctl-apply", &local.CommandArgs{
		Environment: pulumi.ToStringMap(map[string]string{
			"SSH_AUTH_SOCK":   "",
			"SSH_KNOWN_HOSTS": "",
		}),
		Interpreter: pulumi.ToStringArray([]string{
			k0sctl, "apply", "--disable-telemetry", "--disable-upgrade-check", "-c", "-",
		}),
		Create: k0sctlConfig,
	})
	if err != nil {
		return err
	}

	ctx.Export("output", apply.Stdout)
	return nil
}
