package kafkax

import (
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/twmb/franz-go/pkg/kadm"
)

// newTestExporter builds a real LagExporter with no I/O: kgo.NewClient
// (inside NewLagExporter) connects lazily on first request, so this never
// dials a broker. record is exercised directly with a hand-built
// kadm.DescribedGroupLags instead of going through poll's network call,
// which is exactly the split lag.go's poll/record separation exists for.
func newTestExporter(t *testing.T) *LagExporter {
	t.Helper()
	e, err := NewLagExporter([]string{"127.0.0.1:1"}, "test-group", prometheus.NewRegistry(), slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("NewLagExporter: %v", err)
	}
	t.Cleanup(e.Close)
	return e
}

func TestLagExporterRecordSetsGaugePerPartition(t *testing.T) {
	e := newTestExporter(t)
	lags := kadm.DescribedGroupLags{
		"test-group": {
			Group: "test-group",
			Lag: kadm.GroupLag{
				"raw.events": {
					0: {Topic: "raw.events", Partition: 0, Lag: 5},
					1: {Topic: "raw.events", Partition: 1, Lag: 0},
				},
			},
		},
	}

	e.record(lags)

	if got := testutil.ToFloat64(e.gauge.WithLabelValues("raw.events", "0")); got != 5 {
		t.Errorf("partition 0 lag = %v, want 5", got)
	}
	if got := testutil.ToFloat64(e.gauge.WithLabelValues("raw.events", "1")); got != 0 {
		t.Errorf("partition 1 lag = %v, want 0", got)
	}
}

// A partition with a load error must not publish a fabricated lag value
// (kadm reports -1 for it): the gauge for that partition should stay
// unset rather than showing -1, which would misread as "ahead of the log".
func TestLagExporterRecordSkipsPartitionLoadErrors(t *testing.T) {
	e := newTestExporter(t)
	lags := kadm.DescribedGroupLags{
		"test-group": {
			Group: "test-group",
			Lag: kadm.GroupLag{
				"raw.events": {
					0: {Topic: "raw.events", Partition: 0, Lag: -1, Err: errors.New("could not list end offset")},
				},
			},
		},
	}

	e.record(lags)

	if got := testutil.ToFloat64(e.gauge.WithLabelValues("raw.events", "0")); got != 0 {
		t.Errorf("errored partition's gauge = %v, want 0 (unset, not -1)", got)
	}
}

// The group itself failing to describe (e.g. coordinator unreachable) must
// not panic looking up a group key that is not in the map.
func TestLagExporterRecordHandlesGroupNotDescribed(t *testing.T) {
	e := newTestExporter(t)
	e.record(kadm.DescribedGroupLags{})
}

// A describe/fetch error at the group level must be treated the same as
// the group being entirely missing: skipped, not partially trusted.
func TestLagExporterRecordHandlesGroupDescribeError(t *testing.T) {
	e := newTestExporter(t)
	lags := kadm.DescribedGroupLags{
		"test-group": {
			Group:       "test-group",
			DescribeErr: errors.New("coordinator unreachable"),
			Lag: kadm.GroupLag{
				"raw.events": {0: {Topic: "raw.events", Partition: 0, Lag: 5}},
			},
		},
	}

	e.record(lags)

	if got := testutil.ToFloat64(e.gauge.WithLabelValues("raw.events", "0")); got != 0 {
		t.Errorf("gauge = %v, want 0: a group-level describe error must not let its lag data through", got)
	}
}
