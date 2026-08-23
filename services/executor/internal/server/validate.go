package server

import (
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	executorv1 "github.com/thisizaro/Momotaro/proto/gen/executor/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// validateExecute rejects requests that are malformed rather than unlucky.
//
// The line matters more here than it looks: the Decision Engine's scheduler
// retries an error three times and then dead-letters the record. So this must
// only reject a genuine caller bug. An action that is simply going to fail,
// or a nudge with no text composed yet, is not a bug and must be executed
// (services/executor/SPEC.md section 5).
func validateExecute(req *executorv1.ExecuteRequest) error {
	if req.GetRecordId() == "" {
		return status.Error(codes.InvalidArgument, "record_id is required")
	}
	if req.GetAttemptNumber() <= 0 {
		// Also the DB's CHECK (attempt_number > 0); caught here so the caller
		// gets a useful message instead of a constraint violation.
		return status.Error(codes.InvalidArgument, "attempt_number must be positive")
	}
	if req.GetActionType() == commonv1.ActionType_ACTION_TYPE_UNSPECIFIED {
		return status.Error(codes.InvalidArgument, "action_type is required")
	}
	return nil
}
