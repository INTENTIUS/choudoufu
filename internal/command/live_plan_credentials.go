// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/credentials/stscreds"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
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
