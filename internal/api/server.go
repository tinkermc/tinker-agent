package api

import (
	"context"
	"errors"

	"connectrpc.com/connect/v2"

	tinkerv1 "github.com/tinkermc/tinker-agent/gen/tinker/v1"
	"github.com/tinkermc/tinker-agent/gen/tinker/v1/tinkerv1connect"
	"github.com/tinkermc/tinker-agent/internal/mc"
)

type Servers struct {
	tinkerv1connect.UnimplementedServerServiceHandler
	Manager *mc.Manager
}

// ---------- NEW in Part 2 ----------

func (s *Servers) ListServers(ctx context.Context, req *tinkerv1.ListServersRequest) (*tinkerv1.ListServersResponse, error) {
	list, err := s.Manager.List()
	if err != nil {
		return nil, toConnect(err)
	}
	resp := &tinkerv1.ListServersResponse{}
	for _, info := range list {
		resp.Servers = append(resp.Servers, toProto(info))
	}
	return resp, nil
}

func (s *Servers) CreateServer(ctx context.Context, req *tinkerv1.CreateServerRequest) (*tinkerv1.CreateServerResponse, error) {
	info, err := s.Manager.Create(ctx, mc.CreateOptions{
		Name:       req.Name,
		Type:       typeToString(req.Type),
		Version:    req.Version,
		MemoryMB:   int(req.MemoryMb),
		Port:       int(req.Port),
		AcceptEULA: req.AcceptEula,
	})
	if err != nil {
		return nil, toConnect(err)
	}
	return &tinkerv1.CreateServerResponse{Server: toProto(info)}, nil
}

func (s *Servers) FindImportableServers(ctx context.Context, req *tinkerv1.FindImportableServersRequest) (*tinkerv1.FindImportableServersResponse, error) {
	found, err := s.Manager.FindImportable(req.Folder)
	if err != nil {
		return nil, toConnect(err)
	}
	resp := &tinkerv1.FindImportableServersResponse{}
	for _, f := range found {
		resp.Servers = append(resp.Servers, &tinkerv1.ImportableServer{
			Path:          f.Path,
			SuggestedName: f.SuggestedName,
			Jar:           f.Jar,
			Port:          int32(f.Port),
			Source:        f.Source,
		})
	}
	return resp, nil
}

func (s *Servers) ImportServer(ctx context.Context, req *tinkerv1.ImportServerRequest) (*tinkerv1.ImportServerResponse, error) {
	info, err := s.Manager.Import(req.Path, req.Name, req.Jar, int(req.MemoryMb))
	if err != nil {
		return nil, toConnect(err)
	}
	return &tinkerv1.ImportServerResponse{Server: toProto(info)}, nil
}

// ---------- From Part 1 (unchanged) ----------

func (s *Servers) GetServerStatus(ctx context.Context, req *tinkerv1.GetServerStatusRequest) (*tinkerv1.GetServerStatusResponse, error) {
	running, err := s.Manager.Running(req.ServerId)
	if err != nil {
		return nil, toConnect(err)
	}
	return &tinkerv1.GetServerStatusResponse{Running: running}, nil
}

func (s *Servers) StartServer(ctx context.Context, req *tinkerv1.StartServerRequest) (*tinkerv1.StartServerResponse, error) {
	if err := s.Manager.Start(req.ServerId); err != nil {
		return nil, toConnect(err)
	}
	return &tinkerv1.StartServerResponse{}, nil
}

func (s *Servers) StopServer(ctx context.Context, req *tinkerv1.StopServerRequest) (*tinkerv1.StopServerResponse, error) {
	if err := s.Manager.Stop(req.ServerId); err != nil {
		return nil, toConnect(err)
	}
	return &tinkerv1.StopServerResponse{}, nil
}

func (s *Servers) SendCommand(ctx context.Context, req *tinkerv1.SendCommandRequest) (*tinkerv1.SendCommandResponse, error) {
	if err := s.Manager.Send(req.ServerId, req.Command); err != nil {
		return nil, toConnect(err)
	}
	return &tinkerv1.SendCommandResponse{}, nil
}

func (s *Servers) WatchConsole(ctx context.Context, req *tinkerv1.WatchConsoleRequest, stream tinkerv1connect.ServerServiceWatchConsoleServerStream) error {
	history, lines, stop, err := s.Manager.Watch(req.ServerId)
	if err != nil {
		return toConnect(err)
	}
	defer stop()

	for _, line := range history { // first, catch Dash up on what it missed
		if err := stream.Send(&tinkerv1.WatchConsoleResponse{Line: line}); err != nil {
			return err
		}
	}
	for { // then send every new line as it happens
		select {
		case <-ctx.Done():
			return nil // Dash disconnected
		case line := <-lines:
			if err := stream.Send(&tinkerv1.WatchConsoleResponse{Line: line}); err != nil {
				return err
			}
		}
	}
}

// ---------- Helpers ----------

// toProto turns the mc package's ServerInfo into the message Dash receives.
func toProto(info mc.ServerInfo) *tinkerv1.Server {
	return &tinkerv1.Server{
		Id:       info.ID,
		Name:     info.Name,
		Type:     typeFromString(info.Type),
		Version:  info.Version,
		MemoryMb: int32(info.MemoryMB),
		Port:     int32(info.Port),
		Running:  info.Running,
	}
}

func typeToString(t tinkerv1.ServerType) string {
	switch t {
	case tinkerv1.ServerType_SERVER_TYPE_PAPER:
		return "paper"
	case tinkerv1.ServerType_SERVER_TYPE_FABRIC:
		return "fabric"
	case tinkerv1.ServerType_SERVER_TYPE_OTHER:
		return "other"
	default:
		return "vanilla"
	}
}

func typeFromString(s string) tinkerv1.ServerType {
	switch s {
	case "vanilla":
		return tinkerv1.ServerType_SERVER_TYPE_VANILLA
	case "paper":
		return tinkerv1.ServerType_SERVER_TYPE_PAPER
	case "fabric":
		return tinkerv1.ServerType_SERVER_TYPE_FABRIC
	default:
		return tinkerv1.ServerType_SERVER_TYPE_OTHER
	}
}

// toConnect turns our errors into error codes Dash can understand.
func toConnect(err error) error {
	switch {
	case errors.Is(err, mc.ErrBadID), errors.Is(err, mc.ErrEULA), errors.Is(err, mc.ErrBadName),
		errors.Is(err, mc.ErrBadType), errors.Is(err, mc.ErrBadVersion), errors.Is(err, mc.ErrBadMemory),
		errors.Is(err, mc.ErrBadPort), errors.Is(err, mc.ErrBadPath):
		return connect.NewError(connect.CodeInvalidArgument, err.Error())
	case errors.Is(err, mc.ErrNoJar), errors.Is(err, mc.ErrNotFound):
		return connect.NewError(connect.CodeNotFound, err.Error())
	case errors.Is(err, mc.ErrNoAccess):
		return connect.NewError(connect.CodePermissionDenied, err.Error())
	case errors.Is(err, mc.ErrAlreadyRunning), errors.Is(err, mc.ErrNotRunning), errors.Is(err, mc.ErrSourceRunning):
		return connect.NewError(connect.CodeFailedPrecondition, err.Error())
	default:
		return connect.NewError(connect.CodeInternal, err.Error())
	}
}
