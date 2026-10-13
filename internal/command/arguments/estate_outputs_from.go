// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package arguments

import (
	"flag"
	"fmt"
	"strings"

	"github.com/urfave/cli/v3"
)

// EstateOutputsFrom is one -estate-outputs-from value (GitHub issue #2051):
// read estate Estate's outputs from the "show -json" document at Path, its
// plan, instead of from what it recorded at its last apply.
type EstateOutputsFrom struct {
	Estate string
	Path   string
}

// String renders the value as it is written on the command line.
func (e EstateOutputsFrom) String() string { return e.Estate + "=" + e.Path }

type estateOutputsFromFlag struct {
	dst *[]EstateOutputsFrom
}

func (v estateOutputsFromFlag) String() string {
	if v.dst == nil {
		return ""
	}
	parts := make([]string, len(*v.dst))
	for i, e := range *v.dst {
		parts[i] = e.String()
	}
	return strings.Join(parts, ",")
}

func (v estateOutputsFromFlag) Set(s string) error {
	estate, path, ok := strings.Cut(s, "=")
	if !ok || estate == "" || path == "" {
		return fmt.Errorf("%q is not ESTATE=PATH", s)
	}
	for _, e := range *v.dst {
		if e.Estate == estate {
			return fmt.Errorf("estate %q is named twice; one estate has one plan", estate)
		}
	}
	*v.dst = append(*v.dst, EstateOutputsFrom{Estate: estate, Path: path})
	return nil
}

func (v estateOutputsFromFlag) Get() any { return *v.dst }

func bindEstateOutputsFrom(c *CommandLine, dst *[]EstateOutputsFrom) *Flag {
	value := estateOutputsFromFlag{dst: dst}
	usage := `Under a live block, answer data "terraform_estate_outputs" reads of ESTATE from PATH, the "show -json" document of ESTATE's saved plan, rather than from what ESTATE recorded at its last apply. An output only known after apply, or sensitive, is refused. Repeatable, once per estate.`
	f := c.Flag(&Flag{
		Name:   "estate-outputs-from",
		Usage:  usage,
		Stdlib: func(fs *flag.FlagSet) { fs.Var(value, "estate-outputs-from", usage) },
	})
	f.SetDisplay("=ESTATE=PATH")
	f.Cli = func() cli.Flag {
		dest := cli.Value(value)
		return &cli.GenericFlag{Name: f.Name, Category: f.GroupID, DefaultText: f.Display, Local: true, Usage: f.Usage, Hidden: f.Hidden, Destination: &dest, Value: dest}
	}
	return f
}
