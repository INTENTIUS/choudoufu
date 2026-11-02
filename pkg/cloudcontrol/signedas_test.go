// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package cloudcontrol

import (
	"bytes"
	"context"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/credentials"
)

// TestRequestLogNamesTheSigningPrincipal pins the signed_as field on the
// "HTTP Request Sent" line (GitHub issue #957): the access key id from the
// request's own credential scope when the client signed, "unsigned" when it
// sent the region-scope placeholder. A client with credentials but no
// SignEndpointOverride against an endpoint override is the unsigned case,
// which is the shape every emulator run had before #957.
func TestRequestLogNamesTheSigningPrincipal(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-amz-json-1.1")
		_, _ = w.Write([]byte(`{"ResourceTagMappingList":[]}`))
	}))
	t.Cleanup(server.Close)

	for _, tc := range []struct {
		name string
		cfg  Config
		want string
	}{
		{
			name: "signed as the block's principal against an override",
			cfg: Config{
				Endpoint:             server.URL,
				Credentials:          credentials.NewStaticCredentialsProvider("111111111111", "secret", ""),
				SignEndpointOverride: true,
			},
			want: "signed_as=111111111111",
		},
		{
			name: "credentials present but not signing against an override",
			cfg: Config{
				Endpoint:    server.URL,
				Credentials: credentials.NewStaticCredentialsProvider("111111111111", "secret", ""),
			},
			want: "signed_as=unsigned",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			prev := log.Writer()
			log.SetOutput(&buf)
			t.Cleanup(func() { log.SetOutput(prev) })

			c := NewTagging(tc.cfg)
			if _, err := c.GetResources(context.Background(), nil, nil); err != nil {
				t.Fatalf("GetResources: %v", err)
			}
			out := buf.String()
			line := ""
			for _, l := range strings.Split(out, "\n") {
				if strings.Contains(l, "HTTP Request Sent") {
					line = l
				}
			}
			if line == "" {
				t.Fatalf("no HTTP Request Sent line:\n%s", out)
			}
			if !strings.Contains(line, tc.want) {
				t.Errorf("request line = %q, want it to carry %q", line, tc.want)
			}
		})
	}
}
