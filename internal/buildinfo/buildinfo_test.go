package buildinfo

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProductVersionDevFallback(t *testing.T) {
	oldVersion, oldGitSHA := Version, GitSHA
	t.Cleanup(func() { Version, GitSHA = oldVersion, oldGitSHA })

	Version = ""
	GitSHA = ""

	assert.Equal(t, "nodecore/dev", ProductVersion())
}

func TestProductVersionDevWithGitSHA(t *testing.T) {
	oldVersion, oldGitSHA := Version, GitSHA
	t.Cleanup(func() { Version, GitSHA = oldVersion, oldGitSHA })

	Version = "dev"
	GitSHA = "abcdef"

	assert.Equal(t, "nodecore/dev-abcdef", ProductVersion())
}

func TestProductVersionRelease(t *testing.T) {
	oldVersion, oldGitSHA := Version, GitSHA
	t.Cleanup(func() { Version, GitSHA = oldVersion, oldGitSHA })

	Version = "1.2.3"
	GitSHA = "abcdef"

	assert.Equal(t, "nodecore/1.2.3", ProductVersion())
}
