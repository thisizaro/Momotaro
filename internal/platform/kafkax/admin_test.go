//go:build integration

// Test-local helpers for the integration-tagged tests in this package.

package kafkax

import "context"

// ensureTopic and ensureTopicWithPartitions are thin test-local wrappers
// around the exported EnsureTopic, kept so existing test call sites don't
// need to name partitions explicitly for the common single-partition case.
func ensureTopic(ctx context.Context, brokers []string, topic string) error {
	return EnsureTopic(ctx, brokers, topic, 1)
}

func ensureTopicWithPartitions(ctx context.Context, brokers []string, topic string, partitions int32) error {
	return EnsureTopic(ctx, brokers, topic, partitions)
}
