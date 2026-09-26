// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package caller

import "example.com/fixture/seams"

func viaOtherPackage(b *configschema.Block) string { return seams.SurfaceOf(b) }

func notASeam() string { return "nothing here" }

func mixed(b *configschema.Block) bool { return seams.SurfaceOf(b) != "" && seams.Deep(b) }
