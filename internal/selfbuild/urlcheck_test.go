package selfbuild_test

import (
	"strings"
	"testing"

	"github.com/clagentic/clagentic-directory/internal/selfbuild"
)

func TestValidateSelfBuildURL(t *testing.T) {
	cases := []struct {
		name, raw, wantErr string
	}{
		{"http ok", "http://example.invalid/x", ""},
		{"https ok", "https://example.invalid", ""},
		{"file scheme", "file:///etc/passwd", "unsupported scheme"},
		{"no host", "http://", "no host"},
		{"parse error", "http://[::1", "parse error"},
	}
	for _, c := range cases {
		err := selfbuild.ValidateSelfBuildURL(c.raw, "mech")
		if c.wantErr == "" {
			if err != nil {
				t.Errorf("%s: unexpected error %v", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.wantErr) || !strings.HasPrefix(err.Error(), "mech URL") {
			t.Errorf("%s: got %v, want error containing %q", c.name, err, c.wantErr)
		}
	}
}
