// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// cloudcontrol-probe is a manual smoke test for internal/live/cloudcontrol,
// not shipped in any binary. Point it at floci or real AWS and it either
// lists a type or, given -identifier, fetches one resource — the two calls
// the package's automated tests only ever exercise against a fake server.
//
// Usage, from anywhere in the checkout:
//
//	# against floci, once `floci start` (or the emulator's run.sh) is up
//	go run ./tools/cloudcontrol-probe -type AWS::EC2::Instance -endpoint http://localhost:4566
//
//	# against real AWS, using whatever the environment's credential chain resolves
//	go run ./tools/cloudcontrol-probe -type AWS::EC2::Instance -region us-east-1
//
//	# one resource instead of a list
//	go run ./tools/cloudcontrol-probe -type AWS::EC2::Instance -identifier i-0123456789abcdef0
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "cloudcontrol-probe:", err)
		os.Exit(1)
	}
}

func run() error {
	typeName := flag.String("type", "", "Cloud Control type name, e.g. AWS::EC2::Instance (required)")
	identifier := flag.String("identifier", "", "identifier to GetResource instead of ListResources; multi-part identifiers join with |")
	endpoint := flag.String("endpoint", "", "endpoint override, e.g. http://localhost:4566 for floci; empty means real AWS")
	region := flag.String("region", "us-east-1", "region, for both the real-AWS host and the SigV4 credential scope")
	signOverride := flag.Bool("sign-endpoint-override", false, "sign even against -endpoint, for an override that is itself real AWS")
	timeout := flag.Duration("timeout", 30*time.Second, "request timeout")
	flag.Parse()

	if *typeName == "" {
		flag.Usage()
		return errors.New("-type is required")
	}

	client := cloudcontrol.New(cloudcontrol.Config{
		Endpoint:             *endpoint,
		Region:               *region,
		SignEndpointOverride: *signOverride,
		RoundTripper:         http.DefaultTransport,
	})

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	if *identifier != "" {
		desc, err := client.GetResource(ctx, *typeName, *identifier)
		if err != nil {
			return reportAPIError(err)
		}
		return enc.Encode(desc)
	}

	descs, err := client.ListResources(ctx, *typeName)
	if err != nil {
		return reportAPIError(err)
	}
	fmt.Fprintf(os.Stderr, "%d resource(s) of type %s\n", len(descs), *typeName)
	return enc.Encode(descs)
}

// reportAPIError adds the HTTP status and API error code to the returned
// error's text when err is a *cloudcontrol.APIError, so a probe run against
// floci shows UnsupportedOperation (its answer for GetResource on some
// types) as what it is rather than as an opaque failure.
func reportAPIError(err error) error {
	var apiErr *cloudcontrol.APIError
	if errors.As(err, &apiErr) {
		return fmt.Errorf("%s: HTTP %d, code %q: %s", apiErr.Op, apiErr.StatusCode, apiErr.Code, apiErr.Message)
	}
	return err
}
