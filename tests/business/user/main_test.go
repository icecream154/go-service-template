//go:build business

package user_business_test

import (
	"testing"

	"go-service-template/tests/harness"
)

var suite harness.Suite

func TestMain(m *testing.M) {
	suite.Run(m)
}
