package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/gocty"
)

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
			log.Fatalf("not found schema: %s", strings.Join(path, "/"))
		}
	}
	return block
}

func main() {
	hclfile := os.Args[1]

	execPathBuf, err := exec.Command("which", "terraform").Output()
	execPath := strings.SplitN(string(execPathBuf), "\n", 2)[0]
	if err != nil {
		panic(err)
	}
	log.Printf("executable path: %s\n", execPath)

	workingDirBuf, err := exec.Command("pwd").Output()
	workingDir := strings.SplitN(string(workingDirBuf), "\n", 2)[0]
	if err != nil {
		panic(err)
	}
	log.Printf("working directory: %s\n", workingDir)

	tf, err := tfexec.NewTerraform(workingDir, execPath)
	if err != nil {
		panic(err)
	}

	schema, err := tf.ProvidersSchema(context.TODO())
	if err != nil {
		panic(err)
	}

	tfstate := make(map[string]*tfjson.StateResource)
	{
		state, err := tf.Show(context.TODO())
		if err != nil {
			panic(err)
		}

		for _, resource := range state.Values.RootModule.Resources {
			tfstate[resource.Address] = resource
		}
	}

	src, err := ioutil.ReadFile(hclfile)
	if err != nil {
		panic(err)
	}

	file, diagnostics := hclwrite.ParseConfig(src, hclfile, hcl.InitialPos)
	if diagnostics.HasErrors() {
		panic(diagnostics)
	}

	body := file.Body()
	var resources []*hclwrite.Block
	for _, block := range body.Blocks() {
		switch block.Type() {
		case "resource":
			resources = append(resources, block)
		}
	}

	output := hclwrite.NewEmptyFile()
	for _, resource := range resources {
		resourceKind := resource.Labels()[0]
		resourceName := resource.Labels()[1]

		log.Printf("resource address: %s", resourceKind+"."+resourceName)

		state := tfstate[resourceKind+"."+resourceName]

		var resourceSchema *tfjson.Schema
		for _, providerSchema := range schema.Schemas {
			if schema, ok := providerSchema.ResourceSchemas[resourceKind]; ok {
				resourceSchema = schema
				break
			}
		}

		attribute := state.AttributeValues
		newResource := generateNestedBlock([]string{}, "resource", resourceSchema.Block, attribute)
		newResource.SetLabels(resource.Labels())
		output.Body().AppendBlock(newResource)
	}

	output.WriteTo(os.Stdout)
}
