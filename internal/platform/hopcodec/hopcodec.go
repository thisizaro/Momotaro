// Package hopcodec is the on-disk encoding of a classification's provider
// hops, shared by the two services that must agree on it byte for byte.
//
// It lives in internal/platform for the same reason kafkax does: this is
// generic encoding infrastructure, not one service's business logic. The
// Decision Engine writes audit_entry.provider_hops and the Audit Service reads
// it back, and those are different functions in different services, so nothing
// forces them to stay in step. A round-trip test in one place does. A silently
// diverging delimiter would corrupt an audit row rather than fail a build,
// which is the worst kind of bug this project can have.
//
// Format is "provider:result" pairs joined by ",", e.g. "groq:timeout,gemini:ok".
// TEXT rather than JSONB, matching the schema's house style (every enum in it
// is stored as TEXT) and readable in psql without a JSON operator. Neither
// field may contain the delimiters; ProviderHop's own comment in
// common.proto says so, and provider.NewChain rejects offending provider names
// at startup. Encode still refuses them rather than trusting that, because the
// cost of being wrong is a corrupted trail.
package hopcodec

import (
	"fmt"
	"strings"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

const (
	pairSep  = ","
	fieldSep = ":"
)

// Encode renders hops for storage. An empty or nil slice encodes to the empty
// string, which callers store as NULL: "no classification happened here" and
// "a classification happened and tried nothing" are different facts, and the
// column should not blur them (migration 00005).
func Encode(hops []*commonv1.ProviderHop) (string, error) {
	if len(hops) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(hops))
	for i, h := range hops {
		provider, result := h.GetProvider(), h.GetResult()
		if provider == "" || result == "" {
			return "", fmt.Errorf("hop %d: provider and result must both be set, got %q/%q", i, provider, result)
		}
		if strings.ContainsAny(provider, pairSep+fieldSep) || strings.ContainsAny(result, pairSep+fieldSep) {
			return "", fmt.Errorf("hop %d (%s/%s): must not contain %q or %q", i, provider, result, fieldSep, pairSep)
		}
		parts = append(parts, provider+fieldSep+result)
	}
	return strings.Join(parts, pairSep), nil
}

// Decode parses a stored value back into hops, preserving order.
//
// Deliberately lenient in one direction and strict in the other: an empty
// string yields nil (the NULL case), but a malformed pair is an error rather
// than a silently dropped hop. A trail that quietly loses a hop is worse than
// one that admits it cannot be read, because the whole point of the field is
// to show what was tried.
func Decode(s string) ([]*commonv1.ProviderHop, error) {
	if s == "" {
		return nil, nil
	}
	parts := strings.Split(s, pairSep)
	hops := make([]*commonv1.ProviderHop, 0, len(parts))
	for _, p := range parts {
		provider, result, ok := strings.Cut(p, fieldSep)
		if !ok || provider == "" || result == "" {
			return nil, fmt.Errorf("malformed hop %q in %q, want provider%sresult", p, s, fieldSep)
		}
		hops = append(hops, &commonv1.ProviderHop{Provider: provider, Result: result})
	}
	return hops, nil
}
