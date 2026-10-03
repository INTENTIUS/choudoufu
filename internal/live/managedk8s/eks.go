// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package managedk8s

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"

	"github.com/intentius/choudoufu/internal/live/staterecord"
)

// eksDescribeAction is the IAM action DescribeCluster needs.
const eksDescribeAction = "eks:DescribeCluster"

// eksDefaultEncryptionMinor is the first Kubernetes minor version (1.x) at
// which EKS envelope-encrypts all Kubernetes API data, Secrets included, with
// an AWS owned KMS key when the cluster names no key of its own. AWS
// announced it in 2025 for every cluster on 1.28 or later; DescribeCluster's
// encryptionConfig lists only a customer key, so a cluster on that default
// has an empty one.
const eksDefaultEncryptionMinor = 28

// eksAuth is what signing a DescribeCluster needs: credentials, and the base
// endpoint the SDK's own configuration names, if any.
type eksAuth struct {
	creds        aws.Credentials
	baseEndpoint string
	region       string
}

// eksDescribeResponse is the part of DescribeCluster's response this reads.
type eksDescribeResponse struct {
	Cluster struct {
		Name             string `json:"name"`
		Arn              string `json:"arn"`
		Version          string `json:"version"`
		Endpoint         string `json:"endpoint"`
		Status           string `json:"status"`
		EncryptionConfig []struct {
			Resources []string `json:"resources"`
			Provider  struct {
				KeyArn string `json:"keyArn"`
			} `json:"provider"`
		} `json:"encryptionConfig"`
	} `json:"cluster"`
}

// eks asks DescribeCluster. The EKS service client is not a dependency of
// this module and one GET does not need it, so the request is signed with
// the SDK's own SigV4 signer and the credentials its default chain resolves:
// the same chain, profile and environment every other AWS client here uses.
func (r *Reader) eks(ctx context.Context, cp staterecord.ManagedControlPlane) (staterecord.ControlPlaneEncryption, error) {
	var out staterecord.ControlPlaneEncryption
	auth, err := r.eksAuth(ctx, cp.Region)
	if err != nil {
		return out, err
	}

	base := r.EKSEndpoint
	if base == "" {
		base = os.Getenv("AWS_ENDPOINT_URL_EKS")
	}
	if base == "" {
		base = auth.baseEndpoint
	}
	if base == "" {
		base = "https://eks." + auth.region + "." + awsDNSSuffix(auth.region)
	}
	u := strings.TrimSuffix(base, "/") + "/clusters/" + url.PathEscape(cp.Name)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return out, fmt.Errorf("building the DescribeCluster request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	emptyHash := sha256.Sum256(nil)
	if err := v4.NewSigner().SignHTTP(ctx, auth.creds, req, hex.EncodeToString(emptyHash[:]), "eks", auth.region, time.Now().UTC()); err != nil {
		return out, fmt.Errorf("signing the DescribeCluster request: %w", err)
	}
	resp, err := r.httpClient().Do(req)
	if err != nil {
		return out, fmt.Errorf("DescribeCluster: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return out, fmt.Errorf("DescribeCluster: reading the response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return out, eksError(resp, body)
	}

	var d eksDescribeResponse
	if err := json.Unmarshal(body, &d); err != nil {
		return out, fmt.Errorf("DescribeCluster: decoding the response: %w", err)
	}
	return eksEncryption(d), nil
}

// eksEncryption is the verdict a DescribeCluster response carries. Apart
// from [Reader.eks] so it can be held to its cases without a server.
func eksEncryption(d eksDescribeResponse) staterecord.ControlPlaneEncryption {
	c := d.Cluster
	out := staterecord.ControlPlaneEncryption{
		Cluster:   c.Arn,
		Endpoints: appendNonEmpty(nil, c.Endpoint),
	}
	if out.Cluster == "" {
		out.Cluster = c.Name
	}
	for _, ec := range c.EncryptionConfig {
		for _, res := range ec.Resources {
			if res == "secrets" && ec.Provider.KeyArn != "" {
				out.Verdict = staterecord.EncryptionOn
				out.Detail = fmt.Sprintf("encryptionConfig envelope-encrypts secrets with KMS key %s", ec.Provider.KeyArn)
				return out
			}
		}
	}
	major, minor, ok := kubeMinor(c.Version)
	switch {
	case !ok:
		out.Verdict = staterecord.EncryptionUndetermined
		out.Detail = fmt.Sprintf("encryptionConfig names no key for secrets, and the cluster's version %q could not be read, so whether EKS's default envelope encryption (Kubernetes 1.%d and later) applies is not known", c.Version, eksDefaultEncryptionMinor)
	case major > 1 || minor >= eksDefaultEncryptionMinor:
		out.Verdict = staterecord.EncryptionOn
		out.Detail = fmt.Sprintf("encryptionConfig names no customer key, and at Kubernetes %s EKS envelope-encrypts all Kubernetes API data, secrets included, with an AWS owned KMS key by default; name a key of your own with encryptionConfig to control and audit it", c.Version)
	default:
		out.Verdict = staterecord.EncryptionOff
		out.Detail = fmt.Sprintf("encryptionConfig names no KMS key for secrets, and at Kubernetes %s the cluster predates EKS's default envelope encryption (1.%d and later); upgrade it, or associate a key with `aws eks associate-encryption-config`", c.Version, eksDefaultEncryptionMinor)
	}
	return out
}

// kubeMinor reads "1.30" or "1.30.2" or "v1.30" as (1, 30).
func kubeMinor(v string) (major, minor int, ok bool) {
	parts := strings.SplitN(strings.TrimPrefix(v, "v"), ".", 3)
	if len(parts) < 2 {
		return 0, 0, false
	}
	maj, err1 := strconv.Atoi(parts[0])
	mi, err2 := strconv.Atoi(strings.TrimRight(parts[1], "+"))
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return maj, mi, true
}

// eksError turns a non-200 DescribeCluster answer into an error, telling a
// denial and an absent cluster apart from everything else.
func eksError(resp *http.Response, body []byte) error {
	var e struct {
		Message  string `json:"message"`
		Message2 string `json:"Message"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Message
	if msg == "" {
		msg = e.Message2
	}
	if msg == "" {
		msg = strings.TrimSpace(string(body))
	}
	kind := resp.Header.Get("X-Amzn-Errortype")
	if i := strings.IndexByte(kind, ':'); i >= 0 {
		kind = kind[:i]
	}
	err := fmt.Errorf("DescribeCluster: %s (HTTP %d): %s", orStatus(kind, resp.StatusCode), resp.StatusCode, msg)
	switch {
	case kind == "AccessDeniedException":
		return &staterecord.ControlPlaneDeniedError{Action: eksDescribeAction, Err: err}
	case kind == "ResourceNotFoundException" || resp.StatusCode == http.StatusNotFound:
		return &staterecord.ControlPlaneNotFoundError{Err: err}
	}
	return err
}

func orStatus(kind string, code int) string {
	if kind != "" {
		return kind
	}
	return http.StatusText(code)
}

// eksAuth resolves credentials and region through the SDK's default chain,
// or the test hook.
func (r *Reader) eksAuth(ctx context.Context, region string) (eksAuth, error) {
	if r.eksCredentials != nil {
		return r.eksCredentials(ctx, region)
	}
	var opts []func(*awsconfig.LoadOptions) error
	if region != "" {
		opts = append(opts, awsconfig.WithRegion(region))
	}
	cfg, err := awsconfig.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		return eksAuth{}, fmt.Errorf("loading AWS configuration: %w", err)
	}
	if cfg.Region == "" {
		return eksAuth{}, fmt.Errorf("no AWS region: set region in the control_plane block, or AWS_REGION")
	}
	creds, err := cfg.Credentials.Retrieve(ctx)
	if err != nil {
		return eksAuth{}, fmt.Errorf("resolving AWS credentials: %w", err)
	}
	a := eksAuth{creds: creds, region: cfg.Region}
	if cfg.BaseEndpoint != nil {
		a.baseEndpoint = *cfg.BaseEndpoint
	}
	return a, nil
}

// awsDNSSuffix is the partition's DNS suffix for region. Only the partitions
// EKS runs in are told apart.
func awsDNSSuffix(region string) string {
	if strings.HasPrefix(region, "cn-") {
		return "amazonaws.com.cn"
	}
	return "amazonaws.com"
}
