package main

import (
	"k0s-ostests/internal"
	"k0s-ostests/internal/backends/aws"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

func main() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		prefix, err := internal.RandomNamePrefix(ctx)
		if err != nil {
			return err
		}

		backend, err := aws.New(ctx, prefix)
		if err != nil {
			return err
		}

		ctx.Export("ssh-private-key", backend.SSHKey.PrivateKeyOpenssh)
		return nil
	})
}

func mainReal() {
	pulumi.Run(func(ctx *pulumi.Context) error {
		prefix, err := internal.RandomNamePrefix(ctx)
		if err != nil {
			return err
		}

		backend, err := aws.New(ctx, prefix)
		if err != nil {
			return err
		}

		ctx.Export("ssh-private-key", backend.SSHKey.PrivateKeyOpenssh)
		return nil
	})
}
