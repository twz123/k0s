package k0sctl

import (
	"reflect"

	"github.com/pulumi/pulumi/sdk/v3/go/pulumi"
)

// The provider type for the k0sctl package.
type Provider struct {
	pulumi.ProviderResourceState
}

// The set of arguments for constructing a Provider resource.
type ProviderArgs struct{}
type providerArgs struct{}

func (ProviderArgs) ElementType() reflect.Type {
	return reflect.TypeOf((*providerArgs)(nil)).Elem()
}

// NewProvider registers a new resource with the given unique name, arguments, and options.
func NewProvider(ctx *pulumi.Context,
	name string, args *ProviderArgs, opts ...pulumi.ResourceOption) (*Provider, error) {
	if args == nil {
		args = &ProviderArgs{}
	}

	var resource Provider
	err := ctx.RegisterResource("pulumi:providers:k0sctl", name, args, &resource, opts...)
	if err != nil {
		return nil, err
	}
	return &resource, nil
}
