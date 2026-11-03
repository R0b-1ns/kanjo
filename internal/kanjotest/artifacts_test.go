package kanjotest_test

import (
	"testing"

	"github.com/R0b-1ns/kanjo/internal/kanjotest"
)

func TestMemArtifactsContract(t *testing.T) {
	kanjotest.ArtifactsContract(t, func(t *testing.T) kanjotest.ArtifactsFixture {
		m := kanjotest.NewMemArtifacts()
		return kanjotest.ArtifactsFixture{Artifacts: m, Read: m.Read}
	})
}
