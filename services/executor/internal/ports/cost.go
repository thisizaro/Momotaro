package ports

import notifierv1 "github.com/thisizaro/Momotaro/proto/gen/notifier/v1"

// Per-action direct costs in paise, the `direct_cost` term
// docs/ARCHITECTURE.md section 5a describes.
//
// These constants are REQUIRED TO MATCH `configs/intervention_costs.yaml`,
// the checked-in cost model the Decision Engine's economics scorer uses to
// decide what an action is EXPECTED to cost. This file logs what an attempt
// ACTUALLY cost. If the two disagree, "net recovered" (docs/PRD.md section
// 9) is computed against a different cost model than the one that
// authorised the spend, and the headline metric is silently wrong. See
// `configs/intervention_costs.yaml`'s `executor_reconciliation` block for
// the full reasoning and sourcing behind each value, and
// `cost_reconciliation_test.go` for the test that enforces this.
//
// Still constants in code, not read from the YAML at runtime: the
// `indirect_cost` term that prices authorization-rate damage from repeated
// card retries is Phase 2's economics scorer's concern, not the Executor's,
// so the Executor only ever needs the marginal `direct_cost` figures below.
const (
	// retryCostPaise is the unconditional per-attempt cost of re-presenting a
	// debit: the NPCI NACH switching fee (charged win or lose), not a
	// gateway fee (Razorpay charges only on success). Matches
	// `configs/intervention_costs.yaml` actions.RETRY.direct_cost_paise
	// [SOURCED].
	retryCostPaise = 25
	// smsCostPaise is one transactional SMS: aggregator rate plus TRAI DLT
	// scrubbing charge plus GST. Matches
	// `configs/intervention_costs.yaml` channels.sms_paise [SOURCED].
	smsCostPaise = 25
	// whatsappCostPaise is one WhatsApp Business Platform Utility-category
	// template message, India, per-message pricing. On sourced Indian rates
	// this is CHEAPER than SMS, not dearer: an earlier version of this
	// constant assumed WhatsApp was the pricier, better-read-rate channel,
	// which `configs/intervention_costs.yaml`'s reconciliation section shows
	// is false for India. Matches
	// `configs/intervention_costs.yaml` channels.whatsapp_utility_paise
	// [SOURCED, but see caveat there on the rate card citation].
	whatsappCostPaise = 14
)

// channelCostPaise prices one notification by channel.
func channelCostPaise(channel notifierv1.Channel) int64 {
	switch channel {
	case notifierv1.Channel_CHANNEL_WHATSAPP:
		return whatsappCostPaise
	default:
		// SMS is the fallback channel: always available, cheapest, and what
		// an unspecified channel should cost rather than nothing. A free
		// intervention would quietly make Phase 2's expected-value maths
		// meaningless.
		return smsCostPaise
	}
}
