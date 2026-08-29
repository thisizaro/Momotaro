package main

import (
	"math/rand"
	"testing"

	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// codeIsInPool reports whether code appears in recordType's own pool, the
// thing pickCode must never violate: a CHECKOUT record must never end up
// with a transactional failure code like HARD_DECLINE.
func codeIsInPool(code string, recordType commonv1.RecordType) bool {
	for _, e := range codePoolFor(recordType) {
		if e.Code == code {
			return true
		}
	}
	return false
}

func TestGenerateRecordProducesValidValues(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 2000; i++ {
		rec := generateRecord(rng)

		if rec.Type == commonv1.RecordType_RECORD_TYPE_UNSPECIFIED {
			t.Fatalf("record %d: Type is UNSPECIFIED", i)
		}
		if !codeIsInPool(rec.FailureCode, rec.Type) {
			t.Fatalf("record %d: FailureCode %q is not in %v's own pool", i, rec.FailureCode, rec.Type)
		}
		if rec.AmountPaise <= 0 {
			t.Fatalf("record %d: AmountPaise = %d, want positive (record table's own CHECK constraint)", i, rec.AmountPaise)
		}
		if rec.TrueBucket == commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_UNSPECIFIED {
			t.Fatalf("record %d: TrueBucket is UNSPECIFIED", i)
		}
		if rec.RecoveryProbability < 0 || rec.RecoveryProbability > 1 {
			t.Fatalf("record %d: RecoveryProbability = %v, want [0,1] (ground_truth's own CHECK constraint)", i, rec.RecoveryProbability)
		}
		if rec.WrongActionProbability < 0 || rec.WrongActionProbability > 1 {
			t.Fatalf("record %d: WrongActionProbability = %v, want [0,1]", i, rec.WrongActionProbability)
		}
		if rec.ResponseDelaySeconds < 0 {
			t.Fatalf("record %d: ResponseDelaySeconds = %d, want >= 0 (ground_truth's own CHECK constraint)", i, rec.ResponseDelaySeconds)
		}
	}
}

// A CHECKOUT or INVOICE record's true_bucket must stay a plausible outcome
// for that product concept: pickDivergentBucket must never let a checkout
// abandonment's hidden ground truth become something like HARD_DECLINE,
// which only makes sense for an actual payment attempt.
func TestGenerateRecordKeepsTrueBucketWithinTypeFamily(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	for i := 0; i < 2000; i++ {
		rec := generateRecord(rng)
		switch rec.Type {
		case commonv1.RecordType_RECORD_TYPE_CHECKOUT:
			if rec.TrueBucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_ABANDONMENT {
				t.Fatalf("record %d: CHECKOUT record has TrueBucket = %v, want ABANDONMENT only", i, rec.TrueBucket)
			}
		case commonv1.RecordType_RECORD_TYPE_INVOICE:
			if rec.TrueBucket != commonv1.RootCauseBucket_ROOT_CAUSE_BUCKET_OVERDUE {
				t.Fatalf("record %d: INVOICE record has TrueBucket = %v, want OVERDUE only", i, rec.TrueBucket)
			}
		}
	}
}

// Without misleadingCodeChance, a naive code->bucket lookup would always
// agree with the hidden ground truth, and classification accuracy would be
// tautological. This proves real divergence actually occurs, and that it
// is a minority, not the norm.
func TestGenerateRecordSometimesDivergesFromTheObviousBucket(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	const trials = 5000
	diverged := 0
	for i := 0; i < trials; i++ {
		rec := generateRecord(rng)
		entry := findCode(rec.FailureCode, rec.Type)
		if entry.ObviousBucket != rec.TrueBucket {
			diverged++
		}
	}
	rate := float64(diverged) / trials
	// misleadingCodeChance is 0.12; a wide tolerance band, this is a
	// randomness sanity check, not a precise statistical test.
	if rate < 0.05 || rate > 0.25 {
		t.Errorf("divergence rate = %.3f over %d trials, want roughly misleadingCodeChance=%.2f", rate, trials, misleadingCodeChance)
	}
}

func findCode(code string, recordType commonv1.RecordType) codeEntry {
	for _, e := range codePoolFor(recordType) {
		if e.Code == code {
			return e
		}
	}
	return codeEntry{}
}

// A fixed seed must produce the exact same batch every time: reproducing a
// specific demo run, or a bug report against a specific batch, depends on
// this (docs/ENGINEERING.md section 2's no-hidden-randomness spirit).
func TestGenerateRecordIsDeterministicForAFixedSeed(t *testing.T) {
	gen := func(seed int64) []generatedRecord {
		rng := rand.New(rand.NewSource(seed))
		out := make([]generatedRecord, 50)
		for i := range out {
			out[i] = generateRecord(rng)
		}
		return out
	}

	a := gen(42)
	b := gen(42)
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("record %d differs between two runs with the same seed: %+v vs %+v", i, a[i], b[i])
		}
	}
}

func TestPickInstrumentRefOnlyAssignsToPaymentAndMandate(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	pool := instrumentRefPool(100)

	for _, rt := range []commonv1.RecordType{commonv1.RecordType_RECORD_TYPE_CHECKOUT, commonv1.RecordType_RECORD_TYPE_INVOICE} {
		for i := 0; i < 200; i++ {
			if got := pickInstrumentRef(rng, rt, pool); got != "" {
				t.Fatalf("%v: pickInstrumentRef = %q, want \"\" always", rt, got)
			}
		}
	}
}

func TestPickInstrumentRefSometimesSharesAcrossPaymentRecords(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	pool := instrumentRefPool(100)

	seen := map[string]int{}
	for i := 0; i < 2000; i++ {
		ref := pickInstrumentRef(rng, commonv1.RecordType_RECORD_TYPE_PAYMENT, pool)
		if ref != "" {
			seen[ref]++
		}
	}
	if len(seen) == 0 {
		t.Fatal("no PAYMENT record was ever assigned an instrument_ref")
	}
	sharedAtLeastTwice := false
	for _, count := range seen {
		if count >= 2 {
			sharedAtLeastTwice = true
			break
		}
	}
	if !sharedAtLeastTwice {
		t.Error("no instrument_ref was reused across records; the pool exists specifically so instrument_history has real repeats to reason about")
	}
}
