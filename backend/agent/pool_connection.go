package agent

import (
	"context"
	"errors"
	"math/rand/v2"
	"sync"
	"time"

	"github.com/lehlud/zentrale/domain"
)

// Pool checks sample ten percent, rounded up, bounded by both the probe limits
// and the number of available members. Each selected member is checked once.
func poolConnectionSampleSize(total int) int {
	return min(total, max(3, min(8, (total+9)/10)))
}

func (engine *Engine) CheckPoolConnection(ctx context.Context, poolID string) (domain.PoolConnectionCheck, error) {
	engine.configMu.Lock()
	var pool domain.Pool
	err := engine.Store.Load("pool", poolID, &pool)
	engine.configMu.Unlock()
	if err != nil {
		return domain.PoolConnectionCheck{}, err
	}

	if len(pool.Machines) == 0 {
		return domain.PoolConnectionCheck{}, errors.New("pool must contain at least one machine")
	}

	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()

	stop := context.AfterFunc(engine.ctx, cancel)
	defer stop()

	if err := ctx.Err(); err != nil {
		return domain.PoolConnectionCheck{}, err
	}

	count := poolConnectionSampleSize(len(pool.Machines))
	sample := rand.Perm(len(pool.Machines))[:count]
	result := domain.PoolConnectionCheck{
		PoolID:        pool.ID,
		TotalMachines: len(pool.Machines),
		Machines:      make([]domain.PoolMachineConnectionCheck, count),
	}

	// At most eight independent SSH probes run concurrently. Checks do not claim
	// agent/job slots, and one failed member must not hide the remaining results.
	var probes sync.WaitGroup
	for index, memberIndex := range sample {
		member := pool.Machines[memberIndex]
		probes.Go(func() {
			id := pool.ID + "/" + member.ID
			check := engine.CheckMachineConnection(ctx, id)
			result.Machines[index] = domain.PoolMachineConnectionCheck{
				MachineID: id,
				Name:      member.Name,
				Host:      member.Host,
				Port:      member.Port,
				Connected: check.Connected,
				Error:     check.Error,
				HostKey:   check.HostKey,
				Sandbox:   check.Sandbox,
			}
		})
	}
	probes.Wait()

	return result, nil
}
