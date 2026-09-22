// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"os"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/servicetags"
)

// credentials is what the sweep clients built for one provider
// configuration sign with: whatever that configuration's provider block
// resolves to, the way the provider plugin itself does. GitHub issue #957:
// the Cloud Control and Tagging clients were built with Credentials nil for
// every configuration, so region was the only thing read out of a provider
// block for them, and on a two-account estate (claim 19) the tag index was
// fetched once per pass as the SAME principal - the process environment's
// - and reported the same objects on both. The per-provider plugin legs
// covered for it, so nothing was wrong on the board and the second
// account's sweep was simply never measured.
//
// The block's three ways of naming a principal are honoured in the order
// the provider documents them: static keys (access_key, secret_key, and
// token), then a named profile, then an assume_role block layered on top
// of either or on the default chain. Nil means the block names none of
// them, and the client falls back to aws-sdk-go-v2's default chain exactly
// as before; the explicit result reports whether a provider was built, so
// the caller can decide to sign against an endpoint override.
//
// Everything that can touch the network or the filesystem (a profile's
// shared config, an assume-role call) is deferred to the first Retrieve
// and cached, so a run whose sweep never fires - an offline unit test, a
// plan with nothing to list - pays nothing for having read the block.
func (p *statelessProviders) credentials(addr addrs.AbsProviderConfig, endpoint string) (provider aws.CredentialsProvider, explicit bool) {
	val, ok := p.configVals[providerCacheKey(addr)]
	// The same guards [statelessProviders.region] applies, for the same
	// reason: a sensitive-marked value panics GetAttr, and an unknown or
	// null one has nothing to read.
	if !ok || val == cty.NilVal || val.ContainsMarked() || val.IsNull() || !val.Type().IsObjectType() {
		return nil, false
	}
	attr := func(name string) string {
		if !val.Type().HasAttribute(name) {
			return ""
		}
		v := val.GetAttr(name)
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return ""
		}
		return v.AsString()
	}

	region := p.region(addr)
	var base aws.CredentialsProvider
	switch {
	case attr("access_key") != "" && attr("secret_key") != "":
		base = credentials.NewStaticCredentialsProvider(attr("access_key"), attr("secret_key"), attr("token"))
	case attr("profile") != "":
		base = &lazyCredentials{load: func(ctx context.Context) (aws.CredentialsProvider, error) {
			cfg, err := config.LoadDefaultConfig(ctx, config.WithSharedConfigProfile(attr("profile")), config.WithRegion(region))
			if err != nil {
				return nil, err
			}
			return cfg.Credentials, nil
		}}
	}

	role := assumeRoleBlock(val)
	if role.roleARN == "" {
		return base, base != nil
	}
	return &lazyCredentials{load: func(ctx context.Context) (aws.CredentialsProvider, error) {
		opts := []func(*config.LoadOptions) error{config.WithRegion(region)}
		if base != nil {
			opts = append(opts, config.WithCredentialsProvider(base))
		}
		cfg, err := config.LoadDefaultConfig(ctx, opts...)
		if err != nil {
			return nil, err
		}
		client := sts.NewFromConfig(cfg, func(o *sts.Options) {
			if endpoint != "" {
				o.BaseEndpoint = aws.String(endpoint)
			}
		})
		return stscreds.NewAssumeRoleProvider(client, role.roleARN, func(o *stscreds.AssumeRoleOptions) {
			if role.sessionName != "" {
				o.RoleSessionName = role.sessionName
			}
			if role.externalID != "" {
				o.ExternalID = aws.String(role.externalID)
			}
		}), nil
	}}, true
}

// assumeRole is the subset of the provider's assume_role block the sweep
// needs to become that principal.
type assumeRole struct {
	roleARN, sessionName, externalID string
}

// assumeRoleBlock reads the first assume_role block out of a provider
// configuration value, whichever nesting the provider's schema gave it (a
// list in the AWS provider's own schema; a single object is tolerated for
// a schema that models it that way). Empty when the block is absent.
func assumeRoleBlock(val cty.Value) assumeRole {
	if !val.Type().HasAttribute("assume_role") {
		return assumeRole{}
	}
	block := val.GetAttr("assume_role")
	if block.IsNull() || !block.IsWhollyKnown() {
		return assumeRole{}
	}
	if block.CanIterateElements() && !block.Type().IsObjectType() {
		it := block.ElementIterator()
		if !it.Next() {
			return assumeRole{}
		}
		_, block = it.Element()
	}
	if block.IsNull() || !block.Type().IsObjectType() {
		return assumeRole{}
	}
	get := func(name string) string {
		if !block.Type().HasAttribute(name) {
			return ""
		}
		v := block.GetAttr(name)
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			return ""
		}
		return v.AsString()
	}
	return assumeRole{roleARN: get("role_arn"), sessionName: get("session_name"), externalID: get("external_id")}
}

// lazyCredentials builds its underlying provider on the first Retrieve and
// caches both the provider and, through [aws.CredentialsCache], the
// credentials it yields. A load failure is returned from every Retrieve
// rather than retried: the sweep client treats an unresolvable provider as
// "send unsigned", the same as the default chain failing.
type lazyCredentials struct {
	load func(context.Context) (aws.CredentialsProvider, error)

	once     sync.Once
	provider aws.CredentialsProvider
	err      error
}

func (l *lazyCredentials) Retrieve(ctx context.Context) (aws.Credentials, error) {
	l.once.Do(func() {
		p, err := l.load(ctx)
		if err != nil {
			l.err = fmt.Errorf("resolving the provider configuration's credentials: %w", err)
			return
		}
		l.provider = aws.NewCredentialsCache(p)
	})
	if l.err != nil {
		return aws.Credentials{}, l.err
	}
	return l.provider.Retrieve(ctx)
}

// sweepServiceCredentials is what an aws-sdk-go-v2 SERVICE client built for
// the sweep signs with, given whatever [statelessProviders.credentials]
// resolved out of the provider block.
//
// The two sweep clients that predate it ([cloudcontrol.Client]) accept a nil
// provider and send unsigned, which is what every emulator run wants. An
// aws-sdk-go-v2 service client does not: a nil Config.Credentials fails the
// request at signing time with "no EC2 IMDS role found"-shaped errors rather
// than sending anything. So a nil here becomes the SDK's own default chain -
// the behaviour internal/command/live_ls.go gets from LoadDefaultConfig -
// resolved lazily, so a run whose service leg never fires never touches the
// filesystem, the environment's shared config or IMDS for it.
//
// GitHub issue #1131.
func sweepServiceCredentials(fromBlock aws.CredentialsProvider, region string) aws.CredentialsProvider {
	if fromBlock != nil {
		return fromBlock
	}
	return &lazyCredentials{load: func(ctx context.Context) (aws.CredentialsProvider, error) {
		cfg, err := config.LoadDefaultConfig(ctx, config.WithRegion(region))
		if err != nil {
			return nil, err
		}
		return cfg.Credentials, nil
	}}
}

// serviceEndpoint resolves one AWS service client's endpoint override the
// way aws-sdk-go-v2's own configuration does: the service-specific variable
// first, then whatever the all-services resolution already settled on
// ([cloudControlTarget], which reads AWS_ENDPOINT_URL_CLOUDCONTROL then
// AWS_ENDPOINT_URL). Empty means real AWS.
//
// It exists because the #1131 leg's client is constructed from a hand-built
// aws.Config rather than through LoadDefaultConfig, so the SDK's own
// variable resolution does not run for it.
func serviceEndpoint(serviceVar, fallback string) string {
	if v := os.Getenv(serviceVar); v != "" {
		return v
	}
	return fallback
}

// newServiceTagsReader builds the per-service tag-read leg's reader: the
// fourth route to an ownership marker, for a live object an enumeration
// route reaches and no tag route can read a marker off. See
// internal/live/servicetags's package comment for what the other three are
// and why they come back empty for IAM.
//
// GitHub issue #1131 built it inline in [statelessDiscover]. GitHub issue
// #1274 is what that cost: live-mv needed the identical reader and did not
// have one, so it refused to rename an aws_iam_policy whose marker
// live-plan could read perfectly well through iam:ListPolicyTags. The
// construction is here, once, rather than in each command - the endpoint
// resolution alone is a rule about two environment variables that no second
// copy would have got right for long.
//
// ep is the all-services endpoint override the run already settled
// ([cloudControlTarget]); fromBlock is whatever the provider configuration's
// own credentials resolved to, and nil is allowed - see
// [sweepServiceCredentials] for what a nil becomes and why it cannot stay
// nil.
//
// Nothing here calls anything. The client resolves its credentials lazily,
// and the leg's own gate (internal/live/discovery/servicetagread.go) is what
// decides whether a call is ever made, so a run that builds this and never
// needs it pays for the struct and nothing else.
//
// The concrete type is returned rather than the [servicetags.Reader]
// interface because the same client is also the [servicetags.Lister]
// (GitHub issue #1477: iam:ListRoles for the service-linked role, whose
// markers the reader half then reads), and live-plan wires it as both.
func newServiceTagsReader(region, ep string, fromBlock aws.CredentialsProvider) *servicetags.IAM {
	return servicetags.NewIAM(iam.NewFromConfig(
		aws.Config{
			Region: region,
			// Same principal as the Cloud Control and Tagging clients
			// (#957), and the same fallback: a provider block naming no
			// credentials defers to aws-sdk-go-v2's default chain,
			// resolved lazily so a run whose leg never fires pays nothing
			// for it.
			Credentials: sweepServiceCredentials(fromBlock, region),
		},
		func(o *iam.Options) {
			// Built by hand rather than through LoadDefaultConfig, so the
			// SDK's own AWS_ENDPOINT_URL_IAM / AWS_ENDPOINT_URL resolution
			// does not happen for us and is done here instead. The
			// service-specific variable wins, exactly as the SDK orders
			// them.
			if iamEP := serviceEndpoint("AWS_ENDPOINT_URL_IAM", ep); iamEP != "" {
				o.BaseEndpoint = aws.String(iamEP)
			}
		},
	))
}
