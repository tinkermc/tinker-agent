package api

import (
	"context"

	tinkerv1 "github.com/tinkermc/tinker-agent/gen/tinker/v1"
	"github.com/tinkermc/tinker-agent/gen/tinker/v1/tinkerv1connect"
	"github.com/tinkermc/tinker-agent/internal/mc"
)

type Servers struct {
	tinkerv1connect.UnimplementedServerServiceHandler
	Manager *mc.Manager
}

func (s *Servers) GetServerStatus(ctx context.Context, req *tinkerv1.GetServerStatusRequest) (*tinkerv1.GetServerStatusResponse, error) {
	running, err := s.Manager.Running(req.GetServerId())
	if err != nil {
		return nil, err
	}
	return &tinkerv1.GetServerStatusResponse{ServerId: req.GetServerId(), IsRunning: running}, nil
}

func (s *Servers) StartServer(ctx context.Context, req *tinkerv1.StartServerRequest) (*tinkerv1.StartServerResponse, error) {
	if err := s.Manager.Start(req.GetServerId()); err != nil {
		return nil, err
	}
	return &tinkerv1.StartServerResponse{}, nil
}

func (s *Servers) StopServer(ctx context.Context, req *tinkerv1.StopServerRequest) (*tinkerv1.StopServerResponse, error) {
	if err := s.Manager.Stop(req.GetServerId()); err != nil {
		return nil, err
	}
	return &tinkerv1.StopServerResponse{}, nil
}

func (s *Servers) SendCommand(ctx context.Context, req *tinkerv1.SendCommandRequest) (*tinkerv1.SendCommandResponse, error) {
	if err := s.Manager.Send(req.GetServerId(), req.GetCommand()); err != nil {
		return nil, err
	}
	return &tinkerv1.SendCommandResponse{}, nil
}

func (s *Servers) WatchConsole(ctx context.Context, req *tinkerv1.WatchConsoleRequest, stream tinkerv1connect.ServerServiceWatchConsoleServerStream) error {
	history, lines, stop, err := s.Manager.Watch(req.GetServerId())
	if err != nil {
		return err
	}
	defer stop()

	sendLine := func(line string) error {
		return stream.Send(&tinkerv1.WatchConsoleResponse{ServerId: req.GetServerId(), Line: line})
	}
	for _, line := range history {
		if err := sendLine(line); err != nil {
			return err
		}
	}
	for {
		select {
		case <-ctx.Done():
			return nil
		case line := <-lines:
			if err := sendLine(line); err != nil {
				return err
			}
		}
	}
}
