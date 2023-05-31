package internal

import (
	"strings"

	"github.com/pulumi/pulumi-random/sdk/v4/go/random"
	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

type NamePrefix interface {
	PrefixedName(name string) pulumi.StringOutput
}

type randomPrefix struct {
	*random.RandomPet
}

func RandomNamePrefix(ctx *pulumi.Context) (NamePrefix, error) {
	resourcePrefix, err := random.NewRandomPet(ctx, "resource-prefix", nil)
	if err != nil {
		return nil, err
	}

	return &randomPrefix{resourcePrefix}, nil
}

func (p *randomPrefix) PrefixedName(name string) pulumi.StringOutput {
	return pulumi.All(p.ID(), p.Separator).ApplyT(func(v []any) string {
		prefix, sep := string(v[0].(pulumi.ID)), v[1].(string)
		return strings.Join([]string{prefix, name}, sep)
	}).(pulumi.StringOutput)
}
