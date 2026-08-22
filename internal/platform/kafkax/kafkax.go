package kafkax

import (
	"context"
	"errors"
	"fmt"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Message is one record delivered to a consumer handler.
type Message struct {
	Topic     string
	Partition int32
	Offset    int64
	Key       string
	Value     []byte
}

// Producer publishes to Kafka, always keying by record_id so per-record
// ordering holds by construction (docs/ARCHITECTURE.md section 8).
type Producer struct {
	cl *kgo.Client
}

// NewProducer dials the given brokers. Topics are not auto-created (the
// cluster has that disabled deliberately), so the topic must already exist.
func NewProducer(brokers []string) (*Producer, error) {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return nil, fmt.Errorf("new kafka producer: %w", err)
	}
	return &Producer{cl: cl}, nil
}

// Close releases the underlying client.
func (p *Producer) Close() { p.cl.Close() }

// Publish sends value to topic with key set to recordID. Blocks until the
// broker acknowledges, so a caller's own transaction/side-effect ordering
// is preserved: the caller decides what "published" means for it.
func (p *Producer) Publish(ctx context.Context, topic, recordID string, value []byte) error {
	rec := &kgo.Record{Topic: topic, Key: []byte(recordID), Value: value}
	result := p.cl.ProduceSync(ctx, rec)
	if err := result.FirstErr(); err != nil {
		return fmt.Errorf("publish to %s (key=%s): %w", topic, recordID, err)
	}
	return nil
}

// EnsureTopic creates topic with the given partition count if it does not
// already exist, and is a no-op if it does. The cluster has auto-creation
// disabled deliberately (docker-compose.yml: an auto-created topic masks a
// name typo as one that silently receives nothing), so anything that needs
// a topic to exist, most often a test using an isolated scratch topic, must
// provision it explicitly.
func EnsureTopic(ctx context.Context, brokers []string, topic string, partitions int32) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return fmt.Errorf("admin client: %w", err)
	}
	defer cl.Close()

	adm := kadm.NewClient(cl)
	defer adm.Close()

	resp, err := adm.CreateTopics(ctx, partitions, 1, nil, topic)
	if err != nil {
		return fmt.Errorf("create topic %s: %w", topic, err)
	}
	if r, ok := resp[topic]; ok && r.Err != nil && !errors.Is(r.Err, kerr.TopicAlreadyExists) {
		return fmt.Errorf("create topic %s: %w", topic, r.Err)
	}
	return nil
}

// Consumer reads from Kafka as part of a consumer group.
type Consumer struct {
	cl *kgo.Client
}

// NewConsumer dials the given brokers and joins group, subscribing to
// topics. Auto-commit is disabled: Consume commits explicitly after each
// record's handler succeeds.
func NewConsumer(brokers []string, group string, topics []string) (*Consumer, error) {
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumerGroup(group),
		kgo.ConsumeTopics(topics...),
		kgo.DisableAutoCommit(),
		kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()),
	)
	if err != nil {
		return nil, fmt.Errorf("new kafka consumer: %w", err)
	}
	return &Consumer{cl: cl}, nil
}

// Close releases the underlying client. Any records fetched but not yet
// committed will be redelivered on restart, which the idempotency guards at
// the point of action are built to handle (docs/ARCHITECTURE.md section 11).
func (c *Consumer) Close() { c.cl.Close() }

// Consume polls and hands records to handler ONE AT A TIME, in the order
// they were fetched, committing the offset only after handler returns nil.
//
// This is deliberately not the keyed worker pool from
// docs/ARCHITECTURE.md section 8a: that lands with the Decision Engine's
// depth work. This exists to prove the pipeline shape end to end
// (docs/PLAN.md walking skeleton). A handler error stops the loop rather
// than skipping the record, since there is no DLQ path yet either.
//
// Returns when ctx is cancelled, or when a fetch/handle/commit error occurs.
func (c *Consumer) Consume(ctx context.Context, handler func(context.Context, Message) error) error {
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		fetches := c.cl.PollFetches(ctx)

		if err := ctx.Err(); err != nil {
			return err
		}

		var fetchErr error
		fetches.EachError(func(topic string, partition int32, err error) {
			if fetchErr == nil {
				fetchErr = fmt.Errorf("fetch %s[%d]: %w", topic, partition, err)
			}
		})
		if fetchErr != nil {
			return fetchErr
		}

		iter := fetches.RecordIter()
		for !iter.Done() {
			rec := iter.Next()
			msg := Message{
				Topic:     rec.Topic,
				Partition: rec.Partition,
				Offset:    rec.Offset,
				Key:       string(rec.Key),
				Value:     rec.Value,
			}

			if err := handler(ctx, msg); err != nil {
				return fmt.Errorf("handle %s[%d]@%d: %w", rec.Topic, rec.Partition, rec.Offset, err)
			}

			if err := c.cl.CommitRecords(ctx, rec); err != nil {
				return fmt.Errorf("commit %s[%d]@%d: %w", rec.Topic, rec.Partition, rec.Offset, err)
			}
		}
	}
}
