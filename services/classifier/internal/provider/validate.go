package provider

import (
	"fmt"

	classifierv1 "github.com/thisizaro/Momotaro/proto/gen/classifier/v1"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
)

// validate checks a rung's response against the closed vocabularies before
// it is allowed to answer (SPEC.md section 4.7). A response naming a bucket
// or action outside the enum, or a confidence outside [0,1], is a rung
// failure, not an answer. This is what lets a future LLM rung fail schema
// validation and fall through to the next rung instead of putting an
// invalid value in the audit trail.
func validate(resp *classifierv1.ClassifyResponse) error {
	if resp == nil {
		return fmt.Errorf("nil response")
	}
	if _, ok := commonv1.RootCauseBucket_name[int32(resp.GetBucket())]; !ok {
		return fmt.Errorf("bucket %d is outside RootCauseBucket", resp.GetBucket())
	}
	if _, ok := commonv1.ActionType_name[int32(resp.GetRecommendedAction())]; !ok {
		return fmt.Errorf("action %d is outside ActionType", resp.GetRecommendedAction())
	}
	if resp.GetConfidence() < 0 || resp.GetConfidence() > 1 {
		return fmt.Errorf("confidence %v is outside [0,1]", resp.GetConfidence())
	}
	return nil
}
