package docker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/moby/moby/client"

	"github.com/contember/edvabe/internal/runtime"
)

// statsDoc is a minimal decoder for /containers/{id}/stats. Declared
// locally so the docker SDK's internal stats type churn doesn't reach
// our code.
type statsDoc struct {
	MemoryStats struct {
		Usage uint64 `json:"usage"`
		Limit uint64 `json:"limit"`
	} `json:"memory_stats"`
	CPUStats    cpuStats `json:"cpu_stats"`
	PreCPUStats cpuStats `json:"precpu_stats"`
}

type cpuStats struct {
	CPUUsage struct {
		TotalUsage  uint64   `json:"total_usage"`
		PercpuUsage []uint64 `json:"percpu_usage"`
	} `json:"cpu_usage"`
	SystemUsage uint64 `json:"system_cpu_usage"`
	OnlineCPUs  uint32 `json:"online_cpus"`
}

// Stats returns resource usage for a running sandbox. Asks the daemon
// to include a prior sample so CPU percentages can be computed from
// the delta without the caller having to stream and diff samples
// themselves.
func (r *Runtime) Stats(ctx context.Context, sandboxID string) (*runtime.Stats, error) {
	if sandboxID == "" {
		return nil, fmt.Errorf("docker runtime: Stats: sandboxID is required")
	}
	resp, err := r.cli.ContainerStats(ctx, sandboxID, client.ContainerStatsOptions{
		Stream:                false,
		IncludePreviousSample: true,
	})
	if err != nil {
		return nil, fmt.Errorf("docker runtime: stats %q: %w", sandboxID, err)
	}
	defer resp.Body.Close()

	var doc statsDoc
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("docker runtime: decode stats %q: %w", sandboxID, err)
	}

	const mib = 1024 * 1024
	return &runtime.Stats{
		CPUUsedPercent: calcCPUPercent(&doc),
		MemoryUsedMB:   int64(doc.MemoryStats.Usage / mib),
		MemoryLimitMB:  int64(doc.MemoryStats.Limit / mib),
	}, nil
}

func calcCPUPercent(s *statsDoc) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	cores := float64(s.CPUStats.OnlineCPUs)
	if cores == 0 {
		cores = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	if cores == 0 {
		cores = 1
	}
	return (cpuDelta / sysDelta) * cores * 100.0
}
