package main

import (
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"
)

type set[T comparable] map[T]struct{}

func toCtyVal(val interface{}) (cty.Value, error) {
	switch v := val.(type) {
	case []interface{}:
		if len(v) == 0 {
			return cty.ListValEmpty(cty.NilType), nil
		} else {
			var values []cty.Value
			for _, v := range v {
				ctyVal, err := toCtyVal(v)
				if err != nil {
					return cty.NilVal, err
				}

				values = append(values, ctyVal)
			}

			if !cty.CanListVal(values) {
				return cty.NilVal, fmt.Errorf("cannnot convert to list: %#v", values)
			}

			return cty.ListVal(values), nil
		}
	case map[string]interface{}:
		values := make(map[string]cty.Value)
		for k, v := range v {
			ctyVal, err := toCtyVal(v)
			if err != nil {
				return cty.NilVal, err
			}

			values[k] = ctyVal
		}

		if !cty.CanMapVal(values) {
			return cty.NilVal, fmt.Errorf("cannnot convert to map: %#v", values)
		}

		return cty.MapVal(values), nil
	default: // expect primitive value
		ctyType, err := gocty.ImpliedType(v)
		if err != nil {
			return cty.NilVal, err
		}

		return gocty.ToCtyValue(v, ctyType)
	}
}

func generateNestedBlock(path []string, name string, source *tfjson.SchemaBlock, state interface{}) *hclwrite.Block {
	attributes, ok := state.(map[string]interface{})
	if !ok {
		log.Fatalf("unexpected type: %#v", state)
	}

	path = append(path, name)

	block := hclwrite.NewBlock(name, []string{})
	for name, attribute := range attributes {
		if source.Attributes[name] != nil {
			schemaAttribute := source.Attributes[name]
			if schemaAttribute.Computed {
				continue
			}

			ctyVal, err := toCtyVal(attribute)
			if err != nil {
				log.Fatalf("failed to convert Golang value to cty value: %s", err.Error())
			}

			block.Body().SetAttributeValue(name, ctyVal)
		} else if source.NestedBlocks[name] != nil {
			if attribute == nil {
				continue
			}

			blocks, ok := attribute.([]interface{})
			if !ok {
				log.Fatalf("unexpected type: %#v", state)
			}

			for _, attribute := range blocks {
				nestedBlock := generateNestedBlock(path, name, source.NestedBlocks[name].Block, attribute)
				block.Body().AppendBlock(nestedBlock)
			}
		} else {
			log.Fatalf("not found block schema: %s", strings.Join(path, "/"))
		}
	}
	return block
}

func printResources(tf *tfexec.Terraform, resources []*tfjson.StateResource) (*hclwrite.File, error) {
	schema, err := tf.ProvidersSchema(context.TODO())
	if err != nil {
		return nil, err
	}

	output := hclwrite.NewEmptyFile()
	for _, state := range resources {
		if state.Mode != tfjson.ManagedResourceMode {
			continue
		}

		var resourceSchema *tfjson.Schema
		for _, providerSchema := range schema.Schemas {
			if schema, ok := providerSchema.ResourceSchemas[state.Type]; ok {
				resourceSchema = schema
				break
			}
		}
		if resourceSchema == nil {
			log.Fatalf("not found resource schema: %s", state.Type)
		}

		attribute := state.AttributeValues
		newResource := generateNestedBlock([]string{}, "resource", resourceSchema.Block, attribute)
		newResource.SetLabels([]string{state.Type, state.Name})
		output.Body().AppendBlock(newResource)
	}

	return output, nil
}

func main() {
	var (
		include = flag.Bool("include", false, "Mode that salvages only the resources listed on standard input.")
		exclude = flag.Bool("exclude", false, "Mode that excludes and salvages the resources listed in stdin.")
	)
	flag.Parse()
	if *include && *exclude {
		log.Fatalln("cannnot use include flag and exclude flag at the same time.")
	}

	execPathBuf, err := exec.Command("which", "terraform").Output()
	execPath := strings.SplitN(string(execPathBuf), "\n", 2)[0]
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("executable path: %s\n", execPath)

	workingDirBuf, err := exec.Command("pwd").Output()
	workingDir := strings.SplitN(string(workingDirBuf), "\n", 2)[0]
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("working directory: %s\n", workingDir)

	tf, err := tfexec.NewTerraform(workingDir, execPath)
	if err != nil {
		log.Fatal(err)
	}

	var resources []*tfjson.StateResource
	{
		state, err := tf.Show(context.TODO())
		if err != nil {
			log.Fatal(err)
		}
		if state.Values == nil {
			log.Fatalf("here is not Terraform directory: %s", workingDir)
		}
		if *include {
			stdin, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				log.Fatal(err)
			}

			includeList := strings.Fields(string(stdin))
			includeSet := make(set[string])
			for _, item := range includeList {
				includeSet[item] = struct{}{}
			}

			for _, resource := range state.Values.RootModule.Resources {
				if _, ok := includeSet[resource.Address]; ok {
					resources = append(resources, resource)
				}
			}
		} else if *exclude {
			stdin, err := ioutil.ReadAll(os.Stdin)
			if err != nil {
				log.Fatal(err)
			}

			excludeList := strings.Fields(string(stdin))
			excludeSet := make(set[string])
			for _, item := range excludeList {
				excludeSet[item] = struct{}{}
			}

			for _, resource := range state.Values.RootModule.Resources {
				if _, ok := excludeSet[resource.Address]; !ok {
					resources = append(resources, resource)
				}
			}
		} else {
			resources = state.Values.RootModule.Resources
		}
	}

	output, err := printResources(tf, resources)
	if err != nil {
		log.Fatal(err)
	}

	output.WriteTo(os.Stdout)
}
