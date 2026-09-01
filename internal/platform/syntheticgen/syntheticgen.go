// Package syntheticgen is the synthetic-record model used to seed a demo
// batch: given a source of randomness, it produces one record's visible
// shape plus its hidden ground-truth answer key. No I/O, so it is testable
// without Postgres (docs/ENGINEERING.md section 14).
//
// It lives in internal/platform for the same reason kafkax and hopcodec do:
// this is shared infrastructure, not one service's business logic.
// scripts/batchgen (the standalone CLI) and, from Phase 5.5 Unit W onward,
// the World Simulator both need the exact same generation logic, and having
// two independent copies is how they silently drift. Living here as
// package main previously meant nothing could import it at all; moving it
// is a pure relocation, not a redesign, so the two callers keep writing to
// Postgres and Kafka themselves. This package produces values, it never
// acquires a database handle or imports pgx: only the World Simulator may
// write GROUND_TRUTH (docs/ARCHITECTURE.md section 6), and extracting the
// generator does not extend that permission to whatever else imports it.
//
// docs/ARCHITECTURE.md section 6 names the design goal directly: "seeds
// each record with a hidden ground-truth profile at creation time (e.g.
// this transient failure resolves on retry with 80% probability, this hard
// decline only resolves if nudged and even then only 15% of the time, this
// one is genuinely unrecoverable)." bucketProfiles below encodes exactly
// those two named numbers (TRANSIENT_BANK 0.80, HARD_DECLINE 0.15) and
// extrapolates the rest on the same logic; unrecoverable is a per-record
// override applied on top, not its own bucket.
package syntheticgen

import (
	"math"
	"math/rand"

	"github.com/google/uuid"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// bucketProfile is one root-cause bucket's hidden recovery model.
type bucketProfile struct {
	Bucket commonv1.RootCauseBucket
	// RecoveryProbability: chance of recovery given the CORRECT
	// intervention for this bucket (ground_truth.recovery_probability).
	RecoveryProbability float64
	// WrongActionProbability: chance of recovery given a WRONG
	// intervention. Usually near zero; this is what makes choosing
	// correctly actually matter (migration 00001's own column comment).
	WrongActionProbability float64
	// ResponseDelayRange bounds how long a customer takes to react to a
	// nudge, in seconds (ground_truth.response_delay_seconds). Only
	// exercised by World Simulator for nudge-type actions, but every
	// record gets a value regardless of which action it will actually
	// route to, since the schema has no conditional NULL for it.
	ResponseDelayRange [2]int32
}

// bucketProfiles is deliberately not derived from
// services/classifier/internal/rules' action table: that table maps a
// bucket to the SINGLE action the rules engine recommends, this maps a
// bucket to how reality actually responds, which is a modelling choice
// independent of what the agent decides to do. The two are correlated by
// design (a TRANSIENT_BANK record really does mostly resolve on retry) but
// are not the same table, and must not become the same table: that would
// make classification accuracy tautological rather than a real
// measurement (docs/ARCHITECTURE.md section 6's whole point in having a
// separate, hidden ground_truth table at all).
var bucketProfiles = []bucketProfile{
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK,
		RecoveryProbability:    0.80, // ARCHITECTURE.md section 6's own example, verbatim
		WrongActionProbability: 0.05,
		ResponseDelayRange:     [2]int32{60, 900},
	},
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS,
		RecoveryProbability:    0.55, // the salary-window retry timing (schedule.go) exists because this is real
		WrongActionProbability: 0.05,
		ResponseDelayRange:     [2]int32{3600, 21600}, // 1-6h: waiting for the balance to arrive
	},
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE,
		RecoveryProbability:    0.15,                  // ARCHITECTURE.md section 6's own example, verbatim
		WrongActionProbability: 0.02,                  // a retry essentially never fixes a dead instrument
		ResponseDelayRange:     [2]int32{1800, 86400}, // 30min-24h: customer has to go update the instrument
	},
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED,
		RecoveryProbability:    0.35,
		WrongActionProbability: 0.03,
		ResponseDelayRange:     [2]int32{900, 14400}, // 15min-4h
	},
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD,
		RecoveryProbability:    0.05, // must never be auto-acted on regardless (rules table: always escalate)
		WrongActionProbability: 0.0,
		ResponseDelayRange:     [2]int32{0, 0},
	},
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT,
		RecoveryProbability:    0.25,
		WrongActionProbability: 0.05,
		ResponseDelayRange:     [2]int32{300, 7200}, // 5min-2h
	},
	{
		Bucket:                 commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE,
		RecoveryProbability:    0.30,
		WrongActionProbability: 0.05,
		ResponseDelayRange:     [2]int32{3600, 259200}, // 1h-3d: B2B invoices move slower
	},
}

// unrecoverableChance is the "this one is genuinely unrecoverable" example
// from ARCHITECTURE.md section 6, applied per record on top of whichever
// bucket it landed in, not as a bucket of its own: a genuinely lost cause
// can occur in any category. Overrides both probabilities to near-zero.
const unrecoverableChance = 0.06

// misleadingCodeChance is the fraction of records whose failure_code
// naively suggests one bucket (via the same lookup
// services/classifier/internal/rules/buckets.go uses) but whose hidden
// true_bucket is deliberately a different one in the same record-type
// family. Without this, every record's ground truth would exactly match
// what the rules engine's own lookup table would say, and classification
// accuracy would be trivially 100% by construction rather than a real
// measurement.
const misleadingCodeChance = 0.12

// codeEntry is one record type's pool of plausible failure codes, each
// tagged with the bucket a naive code->bucket lookup would assign it (the
// same values services/classifier/internal/rules/buckets.go's own table
// uses), so codePoolFor can both pick a realistic code and compute what the
// "obvious" bucket would be, for misleadingCodeChance to deliberately
// diverge from.
type codeEntry struct {
	Code          string
	ObviousBucket commonv1.RootCauseBucket
}

// paymentAndMandateCodes mixes the original Phase 1 codes with a sample of
// Razorpay's own published error codes (docs/PHASE5_IMPLEMENTATION.md Unit
// I), so a generated demo batch looks like real Razorpay traffic rather
// than the invented vocabulary. Not every code in
// services/classifier/internal/rules/buckets.go's now-larger table needs a
// mirror here, a representative sample per bucket is enough; the point is
// realism, not exhaustive parity.
//
// GATEWAY_TIMEOUT's ObviousBucket moved to RISK_HOLD in the same pass as
// the classifier's own table (Unit I): both must agree, since ObviousBucket
// exists specifically to mirror what the real classifier would say for
// this code, and misleadingCodeChance's divergence measurement is only
// meaningful if the "obvious" answer really is what the classifier
// produces.
var paymentAndMandateCodes = []codeEntry{
	{"BANK_TIMEOUT", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
	{"RAIL_CONGESTION", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
	{"ISSUER_UNAVAILABLE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
	{"BANK_NOT_AVAILABLE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
	{"GATEWAY_TECHNICAL_ERROR", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
	{"UPI_APP_TECHNICAL_ERROR", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_TRANSIENT_BANK},
	{"GATEWAY_TIMEOUT", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD},
	{"INSUFFICIENT_FUNDS", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS},
	{"LOW_BALANCE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS},
	{"TRANSACTION_LIMIT_EXCEEDED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_INSUFFICIENT_FUNDS},
	{"HARD_DECLINE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"EXPIRED_INSTRUMENT", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"EXPIRED_CARD", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"BLOCKED_INSTRUMENT", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"DO_NOT_HONOUR", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"CARD_DECLINED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"INVALID_VPA", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_HARD_DECLINE},
	{"RISK_HOLD", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD},
	{"FRAUD_REVIEW", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD},
	{"PAYMENT_RISK_CHECK_FAILED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_RISK_HOLD},
	{"AUTH_REQUIRED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
	{"REAUTH_REQUIRED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
	{"AUTHENTICATION_FAILED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
	{"INCORRECT_OTP", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
}

// mandateOnlyCodes only make sense once a mandate exists to revoke or
// pause, so they are added to paymentAndMandateCodes only for
// RECORD_TYPE_MANDATE, not RECORD_TYPE_PAYMENT.
var mandateOnlyCodes = []codeEntry{
	{"MANDATE_REVOKED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
	{"MANDATE_PAUSED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
	{"MANDATE_CREATION_FAILED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_USER_ACTION_NEEDED},
}

var checkoutCodes = []codeEntry{
	{"CHECKOUT_ABANDONED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT},
	{"ABANDONED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT},
	{"PAYMENT_CANCELLED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT},
	{"PAYMENT_SESSION_EXPIRED", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT},
}

var invoiceCodes = []codeEntry{
	{"INVOICE_OVERDUE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE},
	{"PAST_DUE", commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE},
}

// recordTypeWeights is the mix of record types a real revenue-at-risk feed
// would show: most volume is one-off payments, checkout abandonment is
// common but each instance is lower-value, invoices and mandates are
// comparatively rare (PRD.md's feature list treats mandates/invoices as
// present but secondary to the core payment-failure narrative).
var recordTypeWeights = []struct {
	Type   commonv1.RecordType
	Weight int
}{
	{commonv1.RecordType_RECORD_TYPE_PAYMENT, 55},
	{commonv1.RecordType_RECORD_TYPE_CHECKOUT, 25},
	{commonv1.RecordType_RECORD_TYPE_MANDATE, 12},
	{commonv1.RecordType_RECORD_TYPE_INVOICE, 8},
}

// GeneratedRecord is one synthetic record: the shape Ingestion's own
// tables need (BATCH/RECORD) plus the hidden answer key
// (GROUND_TRUTH) that only World Simulator and Reporting's accuracy
// scorer may ever read.
type GeneratedRecord struct {
	Type        commonv1.RecordType
	FailureCode string
	AmountPaise int64

	TrueBucket             commonv1.RootCauseBucket
	RecoveryProbability    float64
	WrongActionProbability float64
	ResponseDelaySeconds   int32
}

// GenerateRecord produces one record. rng is the caller's, not a package
// global, so a fixed seed makes an entire batch reproducible
// (docs/ENGINEERING.md section 2's spirit: no hidden, untestable
// randomness).
func GenerateRecord(rng *rand.Rand) GeneratedRecord {
	recordType := pickRecordType(rng)
	entry := pickCode(rng, recordType)
	amount := pickAmountPaise(rng)

	trueBucket := entry.ObviousBucket
	if rng.Float64() < misleadingCodeChance {
		trueBucket = pickDivergentBucket(rng, recordType, entry.ObviousBucket)
	}

	profile := profileFor(trueBucket)
	recovery, wrongAction := profile.RecoveryProbability, profile.WrongActionProbability
	if rng.Float64() < unrecoverableChance {
		// A genuinely lost cause: recovers essentially never, regardless
		// of which action is tried.
		recovery = rng.Float64() * 0.03
		wrongAction = recovery
	}

	return GeneratedRecord{
		Type:                   recordType,
		FailureCode:            entry.Code,
		AmountPaise:            amount,
		TrueBucket:             trueBucket,
		RecoveryProbability:    recovery,
		WrongActionProbability: wrongAction,
		ResponseDelaySeconds:   pickInRange(rng, profile.ResponseDelayRange),
	}
}

func pickRecordType(rng *rand.Rand) commonv1.RecordType {
	total := 0
	for _, w := range recordTypeWeights {
		total += w.Weight
	}
	roll := rng.Intn(total)
	for _, w := range recordTypeWeights {
		if roll < w.Weight {
			return w.Type
		}
		roll -= w.Weight
	}
	return commonv1.RecordType_RECORD_TYPE_PAYMENT // unreachable, weights sum to total
}

func codePoolFor(recordType commonv1.RecordType) []codeEntry {
	switch recordType {
	case commonv1.RecordType_RECORD_TYPE_PAYMENT:
		return paymentAndMandateCodes
	case commonv1.RecordType_RECORD_TYPE_MANDATE:
		return append(append([]codeEntry{}, paymentAndMandateCodes...), mandateOnlyCodes...)
	case commonv1.RecordType_RECORD_TYPE_CHECKOUT:
		return checkoutCodes
	case commonv1.RecordType_RECORD_TYPE_INVOICE:
		return invoiceCodes
	default:
		return paymentAndMandateCodes
	}
}

func pickCode(rng *rand.Rand, recordType commonv1.RecordType) codeEntry {
	pool := codePoolFor(recordType)
	return pool[rng.Intn(len(pool))]
}

// pickDivergentBucket picks a bucket other than avoid, from the same
// record-type family avoid's own pool draws from: a CHECKOUT record's
// true bucket must stay a plausible checkout outcome (ABANDONMENT), not
// jump to something like HARD_DECLINE that only makes sense for a
// transactional failure.
func pickDivergentBucket(rng *rand.Rand, recordType commonv1.RecordType, avoid commonv1.RootCauseBucket) commonv1.RootCauseBucket {
	pool := codePoolFor(recordType)
	candidates := make([]commonv1.RootCauseBucket, 0, len(pool))
	seen := map[commonv1.RootCauseBucket]bool{}
	for _, e := range pool {
		if e.ObviousBucket == avoid || seen[e.ObviousBucket] {
			continue
		}
		seen[e.ObviousBucket] = true
		candidates = append(candidates, e.ObviousBucket)
	}
	if len(candidates) == 0 {
		return avoid
	}
	return candidates[rng.Intn(len(candidates))]
}

func profileFor(bucket commonv1.RootCauseBucket) bucketProfile {
	for _, p := range bucketProfiles {
		if p.Bucket == bucket {
			return p
		}
	}
	return bucketProfiles[0]
}

func pickInRange(rng *rand.Rand, r [2]int32) int32 {
	if r[1] <= r[0] {
		return r[0]
	}
	return r[0] + rng.Int31n(r[1]-r[0])
}

// instrumentRefShareChance is the fraction of PAYMENT/MANDATE records that
// reuse a shared instrument_ref rather than having none: this is what
// gives services/classifier/internal/server (ClassifyRequest.
// instrument_history, Phase 3 Unit F: "signal for distinguishing this rail
// is flaky right now from this card is dead") real, varied data to reason
// about in a demo, instead of every record looking instrument-isolated.
// The rest get "" (no instrument tracked), matching what Ingestion itself
// stores for an omitted instrument_ref (services/ingestion/internal/server
// /store.go's insertRecord: unlike idempotency_key, instrument_ref is
// never NULL-wrapped).
const instrumentRefShareChance = 0.30

// InstrumentRefPool returns a fixed set of synthetic instrument handles
// sized to roughly a tenth of the batch, so instruments that ARE reused
// tend to actually repeat a few times each rather than each being drawn
// once and looking unshared anyway.
func InstrumentRefPool(count int) []string {
	size := count / 10
	if size < 1 {
		size = 1
	}
	pool := make([]string, size)
	for i := range pool {
		pool[i] = "instr_" + uuid.NewString()
	}
	return pool
}

// PickInstrumentRef only assigns one to PAYMENT/MANDATE records: a
// checkout or invoice failure isn't tied to a payment instrument in the
// same sense, so ABANDONMENT/OVERDUE records always get "".
func PickInstrumentRef(rng *rand.Rand, recordType commonv1.RecordType, pool []string) string {
	if recordType != commonv1.RecordType_RECORD_TYPE_PAYMENT && recordType != commonv1.RecordType_RECORD_TYPE_MANDATE {
		return ""
	}
	if rng.Float64() >= instrumentRefShareChance {
		return ""
	}
	return pool[rng.Intn(len(pool))]
}

// pickAmountPaise skews toward smaller, common transaction sizes with a
// long tail of larger ones (log-uniform over roughly Rs.50 to Rs.75,000),
// closer to a real payments distribution than a flat uniform draw would be.
func pickAmountPaise(rng *rand.Rand) int64 {
	const minPaise, maxPaise = 5000, 7500000 // Rs.50 to Rs.75,000
	logMin, logMax := math.Log(minPaise), math.Log(maxPaise)
	logAmount := logMin + rng.Float64()*(logMax-logMin)
	return int64(math.Exp(logAmount))
}
