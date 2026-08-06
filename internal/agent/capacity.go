package agent

import (
	"context"
	"time"
)

// ensureCapacity enforces the worker cap by evicting the longest-idle
// eligible worker. Busy workers and workers with live subscribers are never
// evicted; when nothing is evictable the caller gets capacity_reached.
func (m *manager) ensureCapacity(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	loaded := 0
	for _, slot := range m.workers {
		if slot.worker != nil {
			loaded++
		}
	}
	if loaded < m.cfg.MaxWorkers {
		return nil
	}
	var (
		victimID string
		victim   *sessionSlot
		oldest   time.Time
	)
	for id, slot := range m.workers {
		if slot.worker == nil || slot.worker.IsBusy() {
			continue
		}
		if slot.stream != nil && slot.stream.SubscriberCount() > 0 {
			continue
		}
		idleSince := slot.worker.IdleSince()
		if victimID == "" || idleSince.Before(oldest) {
			victimID, victim, oldest = id, slot, idleSince
		}
	}
	if victim == nil {
		return fmtError(CodeCapacityReached, "agent worker capacity reached")
	}
	delete(m.workers, victimID)
	go func() {
		_ = victim.worker.Stop(ctx)
		victim.stream.Close()
	}()
	return nil
}

// evictionLoop periodically unloads workers idle past IdleTimeout.
func (m *manager) evictionLoop() {
	defer m.wg.Done()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.evictIdle()
		}
	}
}

func (m *manager) evictIdle() {
	m.mu.Lock()
	var victims []*sessionSlot
	now := time.Now()
	for id, slot := range m.workers {
		if slot.worker == nil || slot.worker.IsBusy() {
			continue
		}
		if slot.stream != nil && slot.stream.SubscriberCount() > 0 {
			continue
		}
		if now.Sub(slot.worker.IdleSince()) >= m.cfg.IdleTimeout {
			victims = append(victims, slot)
			delete(m.workers, id)
		}
	}
	m.mu.Unlock()
	for _, slot := range victims {
		_ = slot.worker.Stop(context.Background())
		slot.stream.Close()
	}
}
