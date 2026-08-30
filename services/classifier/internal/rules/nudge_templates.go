package rules

import (
	"context"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
)

// nudgeTemplates is the static Hinglish fallback per root-cause bucket
// (docs/ARCHITECTURE.md section 5b: "Fallback is a static Hinglish template
// per root-cause bucket. If the provider chain is exhausted, the nudge
// still goes out, just in boilerplate."). Uses provider.AmountPlaceholder
// wherever the real amount belongs, exactly like a model-generated message
// must (server.go substitutes it after a rung's response passes
// validation, unifying LLM and template output through one substitution
// path). Kept as data, mirroring actions.go's bucketToAction, so a bucket
// added to the proto without an entry here fails
// TestNudgeTemplateForEveryBucket instead of silently sending an empty
// message.
var nudgeTemplates = map[commonv1.RootCauseBucket]string{
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK: "Aapka " + provider.AmountPlaceholder +
		" ka payment ek technical issue ki wajah se fail ho gaya tha. Hum dobara try kar rahe hain, chinta na karein!",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS: "Aapka " + provider.AmountPlaceholder +
		" ka payment kam balance ki wajah se fail ho gaya. Balance aane par hum dobara try karenge.",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE: "Aapka " + provider.AmountPlaceholder +
		" ka payment fail ho gaya kyunki aapka card kaam nahi kar raha. Please apna payment method update karein.",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED: "Aapke " + provider.AmountPlaceholder +
		" ke payment ko poora karne ke liye thoda action chahiye. Please apni details check aur update karein.",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD: "Aapka payment security check ke liye hold par hai. " +
		"Hamari team jald aapse contact karegi.",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT: "Aapne apna " + provider.AmountPlaceholder +
		" ka order abhi complete nahi kiya. Wapas aaiye aur apna payment poora karein!",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE: "Aapka " + provider.AmountPlaceholder +
		" ka invoice overdue hai. Please jaldi se payment complete karein.",
	commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED: "Aapke payment mein kuch issue aaya hai. " +
		"Hamari team jald aapse sampark karegi.",
}

// nudgeTemplateFor returns the static message for bucket, or "" for a
// bucket with no entry (should not happen: TestNudgeTemplateForEveryBucket
// iterates the generated enum's full name map, same guard actions_test.go
// uses for bucketToAction).
func nudgeTemplateFor(bucket commonv1.RootCauseBucket) string {
	return nudgeTemplates[bucket]
}

// TemplateNudgeProvider is the always-answers terminal rung of the
// nudge-composition chain (provider.NudgeChain), the ComposeNudge
// equivalent of this package's own Provider for Classify. It has no
// database, no clock and no outbound calls, so it cannot fail.
type TemplateNudgeProvider struct{}

// NewTemplateNudgeProvider returns a TemplateNudgeProvider.
func NewTemplateNudgeProvider() *TemplateNudgeProvider {
	return &TemplateNudgeProvider{}
}

// Name implements provider.NudgeProvider.
func (p *TemplateNudgeProvider) Name() string { return provider.RulesName }

// ComposeNudge returns the static Hinglish template for req's bucket. It
// never errors: this is the property that lets provider.NudgeChain always
// terminate in a valid answer, as long as this rung is last
// (provider.NewNudgeChain enforces that, mirroring NewChain).
func (p *TemplateNudgeProvider) ComposeNudge(ctx context.Context, req *classifierv1.ComposeNudgeRequest) (*classifierv1.ComposeNudgeResponse, error) {
	return &classifierv1.ComposeNudgeResponse{
		Message: nudgeTemplateFor(req.GetBucket()),
	}, nil
}
