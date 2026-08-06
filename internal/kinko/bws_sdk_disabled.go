//go:build !bws_sdk

package kinko

import "errors"

func newBWSSDKProvider() (syncProvider, error) {
	return nil, errors.New("BWS SDK transport is not compiled; license acceptance and target validation are required")
}
