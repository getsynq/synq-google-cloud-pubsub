package main

import (
	"testing"

	"github.com/stretchr/testify/suite"
)

func TestFilterSuite(t *testing.T) {
	suite.Run(t, new(FilterSuite))
}

type FilterSuite struct {
	suite.Suite
}

func (s *FilterSuite) TestFilter() {

	ignorePerPodSubscriptions, err := NewRegexFilter(`-[a-z0-9]{9,10}-[a-z0-9]{5}\.subscription$`)
	s.Require().NoError(err)
	s.True(ignorePerPodSubscriptions.Accept("prod.kernel-accounts.events.v1.kernel-anomalies-kernel-anomalies-api-b659696d7-ksjjl.subscription"))

	includeExcludeFilter := NewIncludeExcludeFilter(nil, []Filter{ignorePerPodSubscriptions})
	s.False(includeExcludeFilter.Accept("prod.kernel-accounts.events.v1.kernel-anomalies-kernel-anomalies-api-b659696d7-ksjjl.subscription"))

}
