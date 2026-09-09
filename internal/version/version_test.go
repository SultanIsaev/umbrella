package version_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/SultanIsaev/umbrella/internal/version"
)

func TestString(t *testing.T) {
	t.Cleanup(func() {
		version.Version, version.Commit, version.BuildDate = "dev", "none", "unknown"
	})

	tests := []struct {
		name      string
		ver       string
		commit    string
		buildDate string
		want      string
	}{
		{
			name:      "defaults",
			ver:       "dev",
			commit:    "none",
			buildDate: "unknown",
			want:      "dev (commit=none, built=unknown)",
		},
		{
			name:      "release build",
			ver:       "v1.2.3",
			commit:    "abc1234",
			buildDate: "2026-09-09T12:00:00Z",
			want:      "v1.2.3 (commit=abc1234, built=2026-09-09T12:00:00Z)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			version.Version, version.Commit, version.BuildDate = tt.ver, tt.commit, tt.buildDate

			require.Equal(t, tt.want, version.String())
		})
	}
}
