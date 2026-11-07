package api

import (
	"context"
	"os"
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/mem"

	tinkerv1 "github.com/tinkermc/tinker-agent/gen/tinker/v1"
	"github.com/tinkermc/tinker-agent/gen/tinker/v1/tinkerv1connect"
)

const Version = "0.1.0"

type Agent struct {
	tinkerv1connect.UnimplementedAgentServiceHandler
}

func (a *Agent) GetInfo(ctx context.Context, req *tinkerv1.GetInfoRequest) (*tinkerv1.GetInfoResponse, error) {
	host, _ := os.Hostname()
	return &tinkerv1.GetInfoResponse{
		AgentVersion: Version,
		Hostname:     host,
		Os:           runtime.GOOS,
	}, nil
}

func (a *Agent) WatchHostStats(ctx context.Context, req *tinkerv1.WatchHostStatsRequest, stream tinkerv1connect.AgentServiceWatchHostStatsServerStream) error {
	for {
		pct, err := cpu.PercentWithContext(ctx, time.Second, false) // waits 1s, then measures
		if err != nil {
			if ctx.Err() != nil {
				return nil // Dash disconnected — normal
			}
			return err
		}
		vm, err := mem.VirtualMemoryWithContext(ctx)
		if err != nil {
			return err
		}
		if err := stream.Send(&tinkerv1.WatchHostStatsResponse{
			CpuPercent:    pct[0],
			MemUsedBytes:  vm.Used,
			MemTotalBytes: vm.Total,
		}); err != nil {
			return err
		}
	}
}