//go:build integration

package userprofile_test

import (
	"testing"

	"go-service-template/tests/harness"
)

var suite harness.Suite

func TestMain(m *testing.M) {
	suite.Run(m)
}
