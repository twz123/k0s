/*
Copyright 2024 k0s authors

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

package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strings"

	"github.com/gobuffalo/flect"
	"github.com/k0sproject/k0s/pkg/apis/k0s"
	"github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	k0sv1beta1 "github.com/k0sproject/k0s/pkg/apis/k0s/v1beta1"
	"github.com/k0sproject/k0s/pkg/applier"
	"sigs.k8s.io/yaml"

	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/kube-openapi/pkg/common"
	openapicommon "k8s.io/kube-openapi/pkg/common"
	validationspec "k8s.io/kube-openapi/pkg/validation/spec"
	"k8s.io/kubernetes/pkg/generated/openapi"

	"github.com/spf13/cobra"
)

func dumpCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dump",
		Short: "Dump",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			// data, err := yaml.Marshal(swaggerSpec())
			// if err != nil {
			// 	return err
			// }

			// _, err = cmd.OutOrStdout().Write(data)
			// return err

			return nil
		},
	}
	return cmd
}

func ToYAML(def *apiextensionsv1.CustomResourceDefinition) ([]byte, error) {
	u, err := applier.ToUnstructured(nil, def)
	if err != nil {
		return nil, err
	}

	unstructured.RemoveNestedField(u.Object, "metadata", "creationTimestamp")
	delete(u.Object, "status")

	return yaml.Marshal(u.Object)
}

func xoxo() (*apiextensionsv1.CustomResourceDefinition, error) {
	refFunc := func(name string) validationspec.Ref {
		return validationspec.MustCreateRef((&url.URL{Fragment: name}).String())
	}
	defs := v1beta1.GetOpenAPIDefinitions(refFunc)
	maps.Copy(defs, openapi.GetOpenAPIDefinitions(refFunc))

	s := runtime.NewScheme()
	if err := k0s.AddToScheme(s); err != nil {
		return nil, err
	}

	for k, v := range s.KnownTypes(k0sv1beta1.SchemeGroupVersion) {
		defName := v.PkgPath() + "." + v.Name()
		def, ok := defs[defName]
		if !ok {
			return nil, fmt.Errorf("no OpenAPI definition for %s", defName)
		}

		gvk := k0sv1beta1.SchemeGroupVersion.WithKind(k)

		reducedDefs := maps.Clone(defs)
		delete(reducedDefs, defName)

		return toCRD(gvk, &def, reducedDefs)
	}

	return nil, errors.New("w00t?")
}

func toCRD(gvk schema.GroupVersionKind, def *openapicommon.OpenAPIDefinition, allDefs map[string]common.OpenAPIDefinition) (*apiextensionsv1.CustomResourceDefinition, error) {
	openAPISchema, err := toSchemaProps(&def.Schema, allDefs)
	if err != nil {
		return nil, err
	}

	singular, plural := strings.ToLower(gvk.Kind), strings.ToLower(flect.Pluralize(gvk.Kind))
	return &apiextensionsv1.CustomResourceDefinition{
		ObjectMeta: metav1.ObjectMeta{
			Name: plural + "." + gvk.Group,
		},
		Spec: apiextensionsv1.CustomResourceDefinitionSpec{
			Group: gvk.Group,
			Names: apiextensionsv1.CustomResourceDefinitionNames{
				Kind:     gvk.Kind,
				ListKind: gvk.Kind + "List",
				Singular: singular,
				Plural:   plural,
			},
			Scope: "Namespaced",
			Versions: []apiextensionsv1.CustomResourceDefinitionVersion{{
				Name:    gvk.Version,
				Served:  true,
				Storage: true,
				Schema: &apiextensionsv1.CustomResourceValidation{
					OpenAPIV3Schema: openAPISchema,
				},
			}},
		},
	}, nil
}

func toSchemaProps(in *validationspec.Schema, allDefs map[string]common.OpenAPIDefinition) (*apiextensionsv1.JSONSchemaProps, error) {
	if in == nil {
		return nil, nil
	}

	if u := in.Ref.GetURL(); u != nil {
		defName := u.Fragment
		def, ok := allDefs[defName]
		if !ok {
			return nil, fmt.Errorf("no def: %s", defName)
		}

		reducedDefs := maps.Clone(allDefs)
		delete(reducedDefs, defName)
		return toSchemaProps(&def.Schema, reducedDefs)
	}

	c := schemaConverter{allDefs: allDefs}
	return &apiextensionsv1.JSONSchemaProps{
		ID:                   in.ID,
		Schema:               "",
		Ref:                  c.ref(in.Ref),
		Description:          in.Description,
		Type:                 c.type_(in.Type),
		Format:               in.Format,
		Title:                in.Title,
		Default:              c.default_(in.Default),
		Maximum:              in.Maximum,
		ExclusiveMaximum:     in.ExclusiveMaximum,
		Minimum:              in.Minimum,
		ExclusiveMinimum:     in.ExclusiveMinimum,
		MaxLength:            in.MaxLength,
		MinLength:            in.MinLength,
		Pattern:              in.Pattern,
		MaxItems:             in.MaxItems,
		MinItems:             in.MinItems,
		UniqueItems:          in.UniqueItems,
		MultipleOf:           in.MultipleOf,
		Enum:                 c.enum(in.Enum),
		MaxProperties:        in.MaxProperties,
		MinProperties:        in.MinProperties,
		Required:             in.Required,
		Items:                c.items(in.Items),
		AllOf:                c.allOf(in.AllOf),
		OneOf:                c.oneOf(in.OneOf),
		AnyOf:                c.anyOf(in.AnyOf),
		Not:                  c.not(in.Not),
		Properties:           c.properties(in.Properties),
		AdditionalProperties: c.additionalProperties(in.AdditionalProperties),
		PatternProperties:    c.patternProperties(in.PatternProperties),
		Dependencies:         c.dependencies(in.Dependencies),
		AdditionalItems:      c.additionalItems(in.AdditionalItems),
		Definitions:          c.definitions(in.Definitions),
		ExternalDocs:         c.externalDocs(in.ExternalDocs),
		Example:              c.example(in.Example),
		Nullable:             in.Nullable,
		// XPreserveUnknownFields: in.XPreserveUnknownFields,
		// XEmbeddedResource:      in.XEmbeddedResource,
		// XIntOrString:           in.XIntOrString,
		// XListMapKeys:           in.XListMapKeys,
		// XListType:              in.XListType,
		// XMapType:               in.XMapType,
		// XValidations:           in.XValidations,
	}, errors.Join(c.errs...)
}

type schemaConverter struct {
	allDefs map[string]common.OpenAPIDefinition
	errs    []error
}

func (c *schemaConverter) ref(in validationspec.Ref) *string {
	if ref := in.String(); ref != "" {
		return &ref
	}

	return nil
}

func (c *schemaConverter) type_(in validationspec.StringOrArray) string {
	switch len(in) {
	case 1:
		return in[0]
	case 0:
		return ""
	}

	c.errs = append(c.errs, fmt.Errorf("fixme: type: %v", in))
	return ""
}

func (c *schemaConverter) default_(in any) *apiextensionsv1.JSON {
	return c.toJSON("default", in)
}

func (c *schemaConverter) enum(in []any) []apiextensionsv1.JSON {
	if in == nil {
		return nil
	}
	if len(in) < 1 {
		return []apiextensionsv1.JSON{}
	}

	c.errs = append(c.errs, fmt.Errorf("fixme: enum: %v", in))
	return nil
}

func (c *schemaConverter) items(in *validationspec.SchemaOrArray) *apiextensionsv1.JSONSchemaPropsOrArray {
	if in == nil {
		return nil
	}

	if l := len(in.Schemas); l > 0 {
		schemas := make([]apiextensionsv1.JSONSchemaProps, l)
		for i := range in.Schemas {
			schema, err := toSchemaProps(&in.Schemas[i], c.allDefs)
			if err != nil {
				c.errs = append(c.errs, fmt.Errorf("items[%d]: %w", i, err))
				return nil
			}
			schemas[i] = *schema
		}

		return &apiextensionsv1.JSONSchemaPropsOrArray{JSONSchemas: schemas}
	}

	schema, err := toSchemaProps(in.Schema, c.allDefs)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("items: %w", err))
		return nil
	}

	return &apiextensionsv1.JSONSchemaPropsOrArray{Schema: schema}
}

func (c *schemaConverter) allOf(in []validationspec.Schema) []apiextensionsv1.JSONSchemaProps {
	return c.toPropsSlice("AllOf", in)
}

func (c *schemaConverter) oneOf(in []validationspec.Schema) []apiextensionsv1.JSONSchemaProps {
	return c.toPropsSlice("OneOf", in)
}

func (c *schemaConverter) anyOf(in []validationspec.Schema) []apiextensionsv1.JSONSchemaProps {
	return c.toPropsSlice("AnyOf", in)
}

func (c *schemaConverter) not(in *validationspec.Schema) *apiextensionsv1.JSONSchemaProps {
	out, err := toSchemaProps(in, c.allDefs)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("not: %w", err))
	}

	return out
}

func (c *schemaConverter) properties(in map[string]validationspec.Schema) map[string]apiextensionsv1.JSONSchemaProps {
	return c.toPropsMap("properties", in)
}

func (c *schemaConverter) additionalProperties(in *validationspec.SchemaOrBool) *apiextensionsv1.JSONSchemaPropsOrBool {
	return c.toPropsOrBool("additionalProperties", in)
}

func (c *schemaConverter) patternProperties(in map[string]validationspec.Schema) map[string]apiextensionsv1.JSONSchemaProps {
	return c.toPropsMap("patternProperties", in)
}

func (c *schemaConverter) dependencies(in validationspec.Dependencies) apiextensionsv1.JSONSchemaDependencies {
	if in == nil {
		return nil
	}

	out := make(apiextensionsv1.JSONSchemaDependencies, len(in))
	for k, v := range in {
		var outValue apiextensionsv1.JSONSchemaPropsOrStringArray

		if len(v.Property) > 0 {
			outValue.Property = slices.Clone(v.Property)
		} else if v.Schema != nil {
			var err error
			outValue.Schema, err = toSchemaProps(v.Schema, c.allDefs)
			if err != nil {
				c.errs = append(c.errs, fmt.Errorf("dependencies: %s: %w", k, err))
				return nil
			}
		}

		out[k] = outValue
	}

	return out
}

func (c *schemaConverter) additionalItems(in *validationspec.SchemaOrBool) *apiextensionsv1.JSONSchemaPropsOrBool {
	return c.toPropsOrBool("additionalItems", in)
}

func (c *schemaConverter) definitions(in validationspec.Definitions) apiextensionsv1.JSONSchemaDefinitions {
	return c.toPropsMap("definitions", in)
}

func (c *schemaConverter) externalDocs(in *validationspec.ExternalDocumentation) *apiextensionsv1.ExternalDocumentation {
	if in == nil {
		return nil
	}

	return &apiextensionsv1.ExternalDocumentation{
		Description: in.Description,
		URL:         in.URL,
	}
}

func (c *schemaConverter) example(in any) *apiextensionsv1.JSON {
	return c.toJSON("example", in)
}

func (c *schemaConverter) toPropsSlice(prop string, in []validationspec.Schema) []apiextensionsv1.JSONSchemaProps {
	if in == nil {
		return nil
	}

	out := make([]apiextensionsv1.JSONSchemaProps, len(in))

	for k, v := range in {
		props, err := toSchemaProps(&v, c.allDefs)
		if err != nil {
			c.errs = append(c.errs, fmt.Errorf("%s: %w", prop, err))
			return nil
		}

		out[k] = *props
	}

	return out
}

func (c *schemaConverter) toPropsMap(prop string, in map[string]validationspec.Schema) map[string]apiextensionsv1.JSONSchemaProps {
	if in == nil {
		return nil
	}

	out := make(map[string]apiextensionsv1.JSONSchemaProps, len(in))
	for k, v := range in {
		outValue, err := toSchemaProps(&v, c.allDefs)
		if err != nil {
			c.errs = append(c.errs, fmt.Errorf("%s: %s: %w", prop, k, err))
			return nil
		}

		out[k] = *outValue
	}

	return out
}

func (c *schemaConverter) toPropsOrBool(prop string, in *validationspec.SchemaOrBool) *apiextensionsv1.JSONSchemaPropsOrBool {
	if in == nil {
		return nil
	}

	if in.Schema == nil {
		return &apiextensionsv1.JSONSchemaPropsOrBool{Allows: in.Allows}
	}

	out, err := toSchemaProps(in.Schema, c.allDefs)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s: %w", prop, err))
		return nil
	}

	return &apiextensionsv1.JSONSchemaPropsOrBool{Schema: out}
}

func (c *schemaConverter) toJSON(prop string, in any) *apiextensionsv1.JSON {
	if in == nil {
		return nil
	}

	data, err := json.Marshal(in)
	if err != nil {
		c.errs = append(c.errs, fmt.Errorf("%s: %w", prop, err))
		return nil
	}

	return &apiextensionsv1.JSON{Raw: data}
}
