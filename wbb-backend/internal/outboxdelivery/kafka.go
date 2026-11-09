package outboxdelivery

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type KafkaProducer struct{ Client *kgo.Client }

func NewKafkaProducer(brokers string) (*KafkaProducer, error) {
	parts := strings.Split(brokers, ",")
	for i, p := range parts {
		parts[i] = strings.TrimSpace(p)
		if parts[i] == "" {
			return nil, errors.New("Kafka broker address required")
		}
	}
	cl, err := kgo.NewClient(
		kgo.SeedBrokers(parts...), kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.AllowIdempotentProduceCancellation(),
		kgo.RecordDeliveryTimeout(15*time.Second), kgo.DialTimeout(3*time.Second),
	)
	if err != nil {
		return nil, err
	}
	return &KafkaProducer{Client: cl}, nil
}

// The callback reports one record's broker outcome, not a batch enqueue.
func (p *KafkaProducer) Publish(ctx context.Context, key string, value []byte) error {
	result := make(chan error, 1)
	p.Client.Produce(ctx, &kgo.Record{Topic: Topic, Key: []byte(key), Value: value}, func(_ *kgo.Record, err error) { result <- err })
	select {
	case err := <-result:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (p *KafkaProducer) Close() { p.Client.Close() }
