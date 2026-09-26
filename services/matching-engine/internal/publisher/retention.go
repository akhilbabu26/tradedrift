package publisher

import (
	"context"
	"log"
	"time"
)

func (p *Publisher) startRetentionJob(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := p.runRetention(ctx); err != nil {
				log.Printf("[publisher] snapshot retention job failed: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (p *Publisher) runRetention(ctx context.Context) error {
	const query = `
		WITH ranked AS (
			SELECT market_id, sequence,
			       ROW_NUMBER() OVER (PARTITION BY market_id ORDER BY sequence DESC) as rn
			FROM market_snapshots
		),
		anchors AS (
			SELECT DISTINCT ON (ms.market_id) ms.market_id, ms.sequence
			FROM market_snapshots ms
			JOIN kafka_checkpoints kc ON kc.partition = ms.partition AND kc.topic = 'orders.commands'
			WHERE ms.offset <= kc.offset
			ORDER BY ms.market_id, ms.offset DESC
		)
		DELETE FROM market_snapshots ms
		WHERE NOT EXISTS (
			SELECT 1 FROM ranked r
			WHERE r.market_id = ms.market_id AND r.sequence = ms.sequence AND r.rn <= 3
		)
		AND NOT EXISTS (
			SELECT 1 FROM anchors a
			WHERE a.market_id = ms.market_id AND a.sequence = ms.sequence
		)`
	_, err := p.db.Exec(ctx, query)
	return err
}
