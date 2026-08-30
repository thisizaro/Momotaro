package server

import (
	"context"
	"fmt"
	"strconv"

	"github.com/google/uuid"
	pgxpkg "github.com/thisizaro/Momotaro/internal/platform/pgx"
	commonv1 "github.com/thisizaro/Momotaro/proto/gen/common/v1"
	reportingv1 "github.com/thisizaro/Momotaro/proto/gen/reporting/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	defaultPageSize = 20
	maxPageSize     = 200
)

// Server implements reportingv1.ReportingServiceServer. StreamBatchUpdates
// is not implemented in this pass: the two unary RPCs close the rubric's
// one real gap ("measured money recovered across a batch") and are cheaper
// than adding a Kafka consumer and a gRPC-stream-to-WebSocket bridge for a
// live feed the dashboard's own polling already covers for every number
// that matters (docs/PHASE5_IMPLEMENTATION.md).
type Server struct {
	reportingv1.UnimplementedReportingServiceServer

	store *store
	clock func() *timestamppb.Timestamp
}

// New returns a Server reading from pool. now defaults to the real clock;
// tests inject a fixed one so GeneratedAt is assertable.
func New(pool *pgxpkg.Pool) *Server {
	return &Server{store: newStore(pool), clock: timestamppb.Now}
}

func (s *Server) GetBatchReport(ctx context.Context, req *reportingv1.GetBatchReportRequest) (*reportingv1.GetBatchReportResponse, error) {
	batchID := req.GetBatchId()
	if batchID == "" {
		return nil, status.Error(codes.InvalidArgument, "batch_id is required")
	}
	if _, err := uuid.Parse(batchID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "batch_id %q is not a valid UUID", batchID)
	}

	exists, err := s.store.batchExists(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("check batch %s: %w", batchID, err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "batch %s not found", batchID)
	}

	h, err := s.store.loadHeadline(ctx, batchID)
	if err != nil {
		return nil, err
	}
	spend, err := s.store.interventionSpend(ctx, batchID)
	if err != nil {
		return nil, err
	}
	bucketRows, err := s.store.byRootCause(ctx, batchID)
	if err != nil {
		return nil, err
	}
	interventionRows, err := s.store.byIntervention(ctx, batchID)
	if err != nil {
		return nil, err
	}
	confusionRows, err := s.store.confusionCounts(ctx, batchID)
	if err != nil {
		return nil, err
	}
	groundTruthRows, err := s.store.groundTruthForBaseline(ctx, batchID)
	if err != nil {
		return nil, err
	}

	report := &reportingv1.BatchReport{
		BatchId:                batchID,
		TotalRecords:           h.TotalRecords,
		InFlightCount:          h.InFlightCount,
		AtRiskPaise:            h.AtRiskPaise,
		RecoveredPaise:         h.RecoveredPaise,
		InterventionSpendPaise: spend,
		NetRecoveredPaise:      h.RecoveredPaise - spend,
		CostPerRupeeRecovered:  paisePerRupee(spend, h.RecoveredPaise),
		RecoveryRate:           ratio(h.RecoveredCount, h.TotalRecords),
		EscalatedCount:         h.EscalatedCount,
		ClosedUneconomicCount:  h.ClosedUneconomicCount,
		ClosedUneconomicPaise:  h.ClosedUneconomicPaise,
		// No persisted signal exists yet for a dead-lettered record: the
		// Decision Engine publishes it to Kafka's raw.events.dlq and
		// leaves RECORD_STATE exactly as it was claimed
		// (services/decision-engine/internal/engine/dlq.go), so Postgres,
		// Reporting's only source of numbers, has nothing to count.
		// Tracked in docs/BACKLOG.md rather than guessed at.
		ProcessingFailureCount: 0,
		ByRootCause:            bucketStatsMap(bucketRows),
		ByIntervention:         interventionStatsMap(interventionRows),
		Accuracy:               classificationAccuracy(confusionRows),
		BaselineComparison:     baselineComparison(groundTruthRows),
		GeneratedAt:            s.clock(),
	}

	return &reportingv1.GetBatchReportResponse{Report: report}, nil
}

func (s *Server) ListBatchRecords(ctx context.Context, req *reportingv1.ListBatchRecordsRequest) (*reportingv1.ListBatchRecordsResponse, error) {
	batchID := req.GetBatchId()
	if batchID == "" {
		return nil, status.Error(codes.InvalidArgument, "batch_id is required")
	}
	if _, err := uuid.Parse(batchID); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "batch_id %q is not a valid UUID", batchID)
	}

	exists, err := s.store.batchExists(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("check batch %s: %w", batchID, err)
	}
	if !exists {
		return nil, status.Errorf(codes.NotFound, "batch %s not found", batchID)
	}

	pageSize := req.GetPageSize()
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}
	if pageSize > maxPageSize {
		pageSize = maxPageSize
	}

	offset, err := decodePageToken(req.GetPageToken())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "invalid page_token: %v", err)
	}

	rows, total, err := s.store.listRecords(ctx, batchID,
		recordStateString(req.GetStateFilter()), rootCauseBucketString(req.GetBucketFilter()),
		pageSize, offset)
	if err != nil {
		return nil, err
	}

	records := make([]*reportingv1.RecordSummary, len(rows))
	for i, r := range rows {
		records[i] = &reportingv1.RecordSummary{
			RecordId:     r.RecordID,
			Type:         commonv1.RecordType(commonv1.RecordType_value[r.Type]),
			AmountPaise:  r.AmountPaise,
			CurrentState: commonv1.RecordState(commonv1.RecordState_value[r.CurrentState]),
			Bucket:       commonv1.RootCauseBucket(commonv1.RootCauseBucket_value[r.Bucket]),
			AttemptCount: r.AttemptCount,
			SpendPaise:   r.SpendPaise,
		}
	}

	nextToken := ""
	if offset+int32(len(rows)) < total {
		nextToken = encodePageToken(offset + pageSize)
	}

	return &reportingv1.ListBatchRecordsResponse{
		Records:       records,
		NextPageToken: nextToken,
		TotalCount:    total,
	}, nil
}

// decodePageToken treats "" as the first page. The token is deliberately
// just a stringified offset: docs/API_GATEWAY.md only requires it be
// opaque, not that it encode anything more clever, and batches in this
// system are demo-scale.
func decodePageToken(token string) (int32, error) {
	if token == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(token, 10, 32)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("page_token must be a non-negative integer offset")
	}
	return int32(n), nil
}

func encodePageToken(offset int32) string {
	return strconv.Itoa(int(offset))
}

func ratio(numerator, denominator int32) float64 {
	if denominator == 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

// paisePerRupee is intervention spend divided by recovered amount expressed
// in rupees, i.e. paise spent per rupee recovered (proto: "efficiency, not
// just volume"). Zero recovered means the ratio is undefined, not
// infinite; reported as 0 rather than +Inf, which cannot round-trip
// through JSON on the Gateway.
func paisePerRupee(spendPaise, recoveredPaise int64) float64 {
	if recoveredPaise == 0 {
		return 0
	}
	return float64(spendPaise) / (float64(recoveredPaise) / 100.0)
}

func bucketStatsMap(rows []bucketRow) map[string]*reportingv1.BucketStats {
	if len(rows) == 0 {
		return nil
	}
	m := make(map[string]*reportingv1.BucketStats, len(rows))
	for _, r := range rows {
		m[r.Bucket] = &reportingv1.BucketStats{
			RecordCount:    r.RecordCount,
			AtRiskPaise:    r.AtRiskPaise,
			RecoveredPaise: r.RecoveredPaise,
			RecoveryRate:   ratio(r.RecoveredCount, r.RecordCount),
		}
	}
	return m
}

func interventionStatsMap(rows []interventionRow) map[string]*reportingv1.InterventionStats {
	if len(rows) == 0 {
		return nil
	}
	m := make(map[string]*reportingv1.InterventionStats, len(rows))
	for _, r := range rows {
		m[r.Action] = &reportingv1.InterventionStats{
			AttemptCount:   r.AttemptCount,
			SuccessCount:   r.SuccessCount,
			SpendPaise:     r.SpendPaise,
			RecoveredPaise: r.RecoveredPaise,
			SuccessRate:    ratio(r.SuccessCount, r.AttemptCount),
		}
	}
	return m
}

// classificationAccuracy returns nil when rows is empty: the batch has no
// GROUND_TRUTH (real traffic, not synthetic), and docs/API_GATEWAY.md is
// explicit that a missing accuracy key means no answer key exists, never
// a zeroed-out one.
func classificationAccuracy(rows []confusionRow) *reportingv1.ClassificationAccuracy {
	if len(rows) == 0 {
		return nil
	}

	var scored, matches int32
	byBucketTotal := make(map[string]int32)
	byBucketMatches := make(map[string]int32)
	confusion := make(map[string]*reportingv1.BucketConfusion)

	for _, r := range rows {
		scored += r.Count
		byBucketTotal[r.Predicted] += r.Count
		if r.Predicted == r.True {
			matches += r.Count
			byBucketMatches[r.Predicted] += r.Count
		}
		if confusion[r.Predicted] == nil {
			confusion[r.Predicted] = &reportingv1.BucketConfusion{TrueBucketCounts: make(map[string]int32)}
		}
		confusion[r.Predicted].TrueBucketCounts[r.True] += r.Count
	}

	byBucket := make(map[string]float64, len(byBucketTotal))
	for bucket, total := range byBucketTotal {
		byBucket[bucket] = ratio(byBucketMatches[bucket], total)
	}

	return &reportingv1.ClassificationAccuracy{
		ScoredRecords:   scored,
		OverallAccuracy: ratio(matches, scored),
		ByBucket:        byBucket,
		Confusion:       confusion,
	}
}
