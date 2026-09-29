// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"
)

// TestRecordReadParallelismSetting: unset passes no option (the store's own
// default of 8 applies), a whole number between 1 and the ceiling passes one,
// and anything else is an error that names the variable and the bound it
// broke - never a quiet fall back to the default, which would leave an
// operator measuring a setting they do not have.
func TestRecordReadParallelismSetting(t *testing.T) {
	for _, tc := range []struct {
		value   string
		options int
		wantErr string
	}{
		{value: "", options: 0},
		{value: "  ", options: 0},
		{value: "1", options: 1},
		{value: "32", options: 1},
		{value: "0", wantErr: "must be at least 1"},
		{value: "-4", wantErr: "must be at least 1"},
		{value: "eight", wantErr: "takes a whole number"},
		{value: "8.5", wantErr: "takes a whole number"},
		// The ceiling. A mistyped value used to be taken literally, and
		// 1000000 opens one GET per record at once. GitHub issue #1383.
		{value: "256", options: 1},
		{value: "257", wantErr: "must be at most 256"},
		{value: "1000000", wantErr: "must be at most 256"},
	} {
		t.Run("value="+tc.value, func(t *testing.T) {
			t.Setenv(recordReadParallelismEnvVar, tc.value)
			opts, err := recordStoreOpenOptions()
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("%q was accepted", tc.value)
				}
				if !strings.Contains(err.Error(), tc.wantErr) || !strings.Contains(err.Error(), recordReadParallelismEnvVar) {
					t.Errorf("the error does not say what is wrong and with which variable: %v", err)
				}
				if opts != nil {
					t.Errorf("an invalid value still produced %d option(s)", len(opts))
				}
				return
			}
			if err != nil {
				t.Fatalf("%q was refused: %v", tc.value, err)
			}
			if len(opts) != tc.options {
				t.Errorf("got %d option(s), want %d", len(opts), tc.options)
			}
		})
	}
}
