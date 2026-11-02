package selfbuild

import (
	"fmt"
	"net/url"
)

// ValidateSelfBuildURL parses the URL and rejects obviously invalid values.
// It does not restrict which hosts are allowed — operators choose their own
// endpoints — but it ensures the URL is at least syntactically valid
// and uses http or https, preventing accidental file:// or other scheme abuse.
// It is the single implementation of this rule, shared by the daemon flags
// and the update values validation.
func ValidateSelfBuildURL(raw, mechanism string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("%s URL parse error: %w", mechanism, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("%s URL has unsupported scheme %q (must be http or https)", mechanism, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%s URL has no host", mechanism)
	}
	return nil
}
