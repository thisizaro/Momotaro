// Package rules implements the classifier's deterministic rules engine: the
// always-answers final rung of the provider chain (SPEC.md section 4).
package rules

import (
	"context"
	"log/slog"

	"github.com/thisizaro/Momotaro/internal/platform/logger"
	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	"github.com/thisizaro/Momotaro/services/classifier/internal/provider"
)

// Provider is the deterministic rules engine. It has no database, no clock,
// and no outbound calls (SPEC.md section 8), so it cannot fail: this is the
// property that lets the provider chain always terminate in a valid answer
// as long as this rung is last (SPEC.md section 4.7).
type Provider struct {
	log *slog.Logger
}

// New returns a rules Provider. log must not be nil.
func New(log *slog.Logger) *Provider {
	return &Provider{log: log}
}

// Name implements provider.Provider.
func (p *Provider) Name() string { return provider.RulesName }

// Classify maps a record's failure code (and, on the unknown-code path, its
// record type) to a root cause bucket, a recommended action, an honest
// confidence, and a rationale that names the actual input. It never errors:
// an empty or unrecognised failure code takes the unknown-code path
// (SPEC.md section 4.3) rather than failing the request.
func (p *Provider) Classify(ctx context.Context, req *classifierv1.ClassifyRequest) (*classifierv1.ClassifyResponse, error) {
	rec := req.GetRecord()
	rawCode := rec.GetFailureCode()
	normalized := normalizeFailureCode(rawCode)

	bucket, recognized := bucketForCode(normalized)
	if !recognized {
		bucket = fallbackBucket(rec.GetType())
		p.log.Warn("unrecognised failure code, using record-type fallback",
			"failure_code", rawCode,
			logger.KeyBucket, bucket.String(),
		)
	}

	rule := actionFor(bucket)
	rationale := composeRationale(bucket, rule.Action, rawCode, recognized)

	return &classifierv1.ClassifyResponse{
		Bucket:            bucket,
		RecommendedAction: rule.Action,
		Rationale:         rationale,
		Confidence:        rule.Confidence,
	}, nil
}
