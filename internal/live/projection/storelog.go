// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"log"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

// recordStoreRequestLog logs one line per request the record store's own AWS
// clients send, in the wording internal/live/cloudcontrol's client already
// uses, so a TF_LOG capture counts them with everything else.
//
// GitHub issue #1335 is why it exists. The claim that one estate never asks
// the bucket for a neighbour's keys is a claim about requests, and before
// this the record store's S3 and SSM traffic left no trace at all in a debug
// capture: the emulator does not log requests either, so there was no wire to
// read and the claim could only have been inferred from a plan that happened
// to look right. It is the same gap #682 closed for the tag sweep.
//
// It sits at the end of the Finalize step, inside the retry loop and after
// signing, so every attempt is a line and the URL is the one that was sent.
// The URL is logged and the body is not: an S3 URL here carries a bucket, a
// key and a prefix, and a record's payload can carry secret material.
func recordStoreRequestLog(stack *middleware.Stack) error {
	return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc("choudoufuRecordStoreRequestLog",
		func(ctx context.Context, in middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
			if req, ok := in.Request.(*smithyhttp.Request); ok {
				log.Printf("[DEBUG] stateless/recordstore: HTTP Request Sent: rpc.service=%s rpc.method=%s http.method=%s http.url=%s",
					awsmiddleware.GetServiceID(ctx), awsmiddleware.GetOperationName(ctx), req.Method, req.URL.String())
			}
			return next.HandleFinalize(ctx, in)
		}), middleware.After)
}
