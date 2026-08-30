package rules

import (
	"context"
	"strings"
	"testing"
	"unicode"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
)

// realisticMaxChars is the SMS-realistic cap docs/ARCHITECTURE.md section 5b
// describes ("Output is length-capped (SMS-realistic)"), matching this
// project's own NUDGE_MAX_CHARS default. The template rung must be the one
// rung that always terminates the chain (SPEC.md section 4.7's guarantee,
// applied to ComposeNudge): its templates are guaranteed to fit under any
// realistic caller-supplied max_chars, but not under an arbitrarily small
// one, the same limitation the rules engine's Classify has against a
// pathological caller (it is guaranteed schema-valid, not guaranteed to fit
// a cap that does not exist for Classify at all).
const realisticMaxChars = 160

// hasStrayDigit is a minimal, test-local check mirroring
// provider.validateNudge's "no digit outside the placeholder" rule.
// Duplicated here deliberately rather than exported and reused: this test
// asserts the template's own data is well-formed by construction, not that
// provider.validateNudge accepts it (that integration is covered by
// nudge_chain_test.go once TemplateNudgeProvider is wired into a chain in
// server_test.go).
func hasStrayDigit(msg string) bool {
	stripped := strings.ReplaceAll(msg, provider.AmountPlaceholder, "")
	for _, r := range stripped {
		if unicode.IsDigit(r) {
			return true
		}
	}
	return false
}

func TestNudgeTemplateForEveryBucket(t *testing.T) {
	for v, name := range commonv1.RootCauseBucket_name {
		bucket := commonv1.RootCauseBucket(v)
		tmpl := nudgeTemplateFor(bucket)
		if tmpl == "" {
			t.Errorf("bucket %s (%d): empty template", name, v)
			continue
		}
		if len(tmpl) > realisticMaxChars {
			t.Errorf("bucket %s (%d): template is %d chars, exceeds realistic max_chars %d", name, v, len(tmpl), realisticMaxChars)
		}
		if occurrences := strings.Count(tmpl, provider.AmountPlaceholder); occurrences > 1 {
			t.Errorf("bucket %s (%d): template contains %s %d times, want at most once", name, v, provider.AmountPlaceholder, occurrences)
		}
		if hasStrayDigit(tmpl) {
			t.Errorf("bucket %s (%d): template %q contains a digit outside %s", name, v, tmpl, provider.AmountPlaceholder)
		}
	}
}

func TestTemplateNudgeProviderName(t *testing.T) {
	p := NewTemplateNudgeProvider()
	if p.Name() != provider.RulesName {
		t.Errorf("Name() = %q, want %q", p.Name(), provider.RulesName)
	}
}

func TestTemplateNudgeProviderNeverErrors(t *testing.T) {
	p := NewTemplateNudgeProvider()
	for v := range commonv1.RootCauseBucket_name {
		bucket := commonv1.RootCauseBucket(v)
		req := &classifierv1.ComposeNudgeRequest{
			Record: &commonv1.Record{AmountPaise: 75000},
			Bucket: bucket,
		}
		resp, err := p.ComposeNudge(context.Background(), req)
		if err != nil {
			t.Errorf("bucket %s: ComposeNudge returned an error: %v", bucket, err)
		}
		if resp.GetMessage() == "" {
			t.Errorf("bucket %s: empty message", bucket)
		}
	}
}
