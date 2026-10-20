// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import "github.com/intentius/choudoufu/internal/configs"

// GCSEnvDefaultsForTest and GCSConnectionEnvForTest let
// gcs_store_stock_test.go, which is in package projection_test so that it may
// import stock's gcs backend, read this package's copy of stock's defaults.
var GCSEnvDefaultsForTest = gcsEnvDefaults

func GCSConnectionEnvForTest(rs *configs.LiveRecordStore) map[string]string {
	c := gcsConnectionFor(rs)
	return map[string]string{
		"access_token":                c.accessToken,
		"impersonate_service_account": c.impersonateServiceAccount,
		"encryption_key":              c.encryptionKey,
		"kms_encryption_key":          c.kmsEncryptionKey,
		"storage_custom_endpoint":     c.storageCustomEndpoint,
		"universe_domain":             c.universeDomain,
	}
}

// GCSClientForTest is newGCSClient, so gcs_store_stock_test.go can build the
// store's client the way the store does and watch which credential it sends.
var GCSClientForTest = newGCSClient
