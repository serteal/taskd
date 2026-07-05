// Package adminserver implements AdminService: daemon administration kept
// deliberately out of the frozen task.TaskService. It contains no domain
// logic of its own — it reports and mutates the extension host's state and
// persists the result through a caller-supplied hook, so it stays free of
// the daemon's config plumbing (and any import cycle with it).
package adminserver

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"

	adminpb "github.com/serteal/taskd/gen/admin"
	"github.com/serteal/taskd/gen/admin/adminconnect"
	"github.com/serteal/taskd/internal/extension"
)

// Server implements AdminService over an extension.Host. ListExtensions
// reports the host's extensions and their enabled state; SetExtensionEnabled
// applies the change to the running host, then persists the resulting
// disabled set via persist.
type Server struct {
	host    *extension.Host
	persist func(disabled []string) error
}

// New wraps host. persist is called with the full disabled set after every
// applied change; it should write it to the daemon's config.
func New(host *extension.Host, persist func(disabled []string) error) *Server {
	return &Server{host: host, persist: persist}
}

// Handler returns the mount path and HTTP handler for the service.
func (s *Server) Handler() (string, http.Handler) {
	return adminconnect.NewAdminServiceHandler(s)
}

var _ adminconnect.AdminServiceHandler = (*Server)(nil)

func (s *Server) ListExtensions(_ context.Context, _ *connect.Request[adminpb.ListExtensionsRequest]) (*connect.Response[adminpb.ListExtensionsResponse], error) {
	infos := s.host.List()
	out := make([]*adminpb.ExtensionInfo, 0, len(infos))
	for _, in := range infos {
		out = append(out, &adminpb.ExtensionInfo{
			Name:      in.Name,
			HasSyncer: in.HasSyncer,
			HasWeb:    in.HasWeb,
			Enabled:   in.Enabled,
		})
	}
	return connect.NewResponse(&adminpb.ListExtensionsResponse{Extensions: out}), nil
}

func (s *Server) SetExtensionEnabled(_ context.Context, req *connect.Request[adminpb.SetExtensionEnabledRequest]) (*connect.Response[adminpb.SetExtensionEnabledResponse], error) {
	name := req.Msg.GetName()
	if name == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("name is required"))
	}
	// Apply first: the running state is what actually stops/starts a syncer
	// and (un)serves a bundle. Persisting the config is durability on top.
	if err := s.host.SetEnabled(name, req.Msg.GetEnabled()); err != nil {
		return nil, mapErr(err)
	}
	if err := s.persist(s.host.Disabled()); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Hot-applied: the syncer is already stopped/started and the bundle is
	// already (un)served, so no restart is needed.
	return connect.NewResponse(&adminpb.SetExtensionEnabledResponse{RestartRequired: false}), nil
}

// mapErr translates extension sentinels into Connect codes, mirroring
// internal/server.mapErr.
func mapErr(err error) error {
	switch {
	case errors.Is(err, extension.ErrUnknownExtension):
		return connect.NewError(connect.CodeNotFound, err)
	default:
		return connect.NewError(connect.CodeInternal, err)
	}
}
