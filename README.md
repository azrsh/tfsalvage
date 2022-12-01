# tfsalvage
This is a wrapper command for Terraform CLI to salvage HCL files from Terraform's tfstate.

## Usage

This command, when run with no arguments, restores all resources present in tfstate to HCL and prints to stdout.

```bash
go install github.com/azarashi2931/tfsalvage@latest
tfsalvage > salvaged.tf
```

The resources to restore can also be controlled by flags and a list of resource addresses passed from stdin.

```bash
cat <<EOF
resource.white_listed1
resource.white_listed2
resource.white_listed3
EOF | tfsalvage -include > salvaged.tf
```

```bash
cat <<EOF
resource.black_listed1
resource.black_listed2
resource.black_listed3
EOF | tfsalvage -exclude > salvaged.tf
```

## Use-case: Import resources

You can salvage an HCL file from imported tfstate.

```bash
terraform import hoge.fuga
echo 'hoge.fuga' | tfsalvage -include > salvaged.tf
```

## Limitations

- This command knows nothing beyond what is provided by the Terraform provider schema.
  - For example, it is not possible to automatically remove attributes with the same value as the default value set by the cloud vendor.
- Cannot control the order of resources, their attributes, or blocks. Because this command don't have such an flag.
