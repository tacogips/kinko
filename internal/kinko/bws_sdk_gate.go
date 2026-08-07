//go:build bws_sdk

package kinko

import "errors"

// The SDK-enabled build remains deliberately unavailable until distribution
// licensing, CGO, and the supported target matrix have been accepted. Keeping
// this gate dependency-free prevents an unapproved SDK entering go.mod.
func newBWSSDKProvider() (syncProvider, error) {
	return nil, errors.New("BWS SDK transport gate is not accepted for this build")
}
