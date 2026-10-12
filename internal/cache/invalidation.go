package cache

import (
	"context"
	"encoding/json"
	"log"
)

const InvalidationChannel = "cache:invalidate"

type InvalidationMessage struct {
	TenantID string `json:"tenant_id"`
	NormalizedQuery string `json:"query_normalized"`
	Pattern string `json:"pattern,omitempty"` // for pattern-based invalidation
}

// StartInvalidationListener listens for cache invalidation events from Redis.
func (c *Coordinator) StartInvalidationListener(ctx context.Context) {
	if c.l2a == nil {
		log.Println("Invalidation listener: Redis (L2a) not configured, skipping.")
		return
	}

	pubsub := c.l2a.Client.Subscribe(ctx, InvalidationChannel)
	defer pubsub.Close()

	ch := pubsub.Channel()
	log.Println("Invalidation listener: subscribed to", InvalidationChannel)

	for {
		select {
		case <-ctx.Done():
			return
		case msg := <-ch:
			var inv InvalidationMessage
			if err := json.Unmarshal([]byte(msg.Payload), &inv); err != nil {
				log.Printf("Invalidation listener: error decoding message: %v", err)
				continue
			}

			// Invalidate L1
			if inv.NormalizedQuery != "" {
				c.l1.Remove(inv.TenantID, inv.NormalizedQuery)
			}
			if inv.Pattern != "" {
				c.l1.Remove(inv.TenantID, inv.Pattern)
			}
			log.Printf("Invalidation: removed entry for tenant %s from L1.", inv.TenantID)
		}
	}
}
