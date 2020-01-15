package mechanism_kernel_tap

import (
	"context"
	"fmt"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"

	"github.com/networkservicemesh/networkservicemesh/controlplane/api/connection"
	"github.com/networkservicemesh/networkservicemesh/controlplane/api/networkservice"
	"github.com/networkservicemesh/networkservicemesh/new/sdk/networkservicemesh/core/next"
)

type kernelTapServer struct {
	baseDir string
}

func NewServer(baseDir string) networkservice.NetworkServiceServer {
	return &kernelTapServer{baseDir: baseDir}
}

func (k *kernelTapServer) Request(ctx context.Context, request *networkservice.NetworkServiceRequest) (*connection.Connection, error) {
	conn, err := next.Server(ctx).Request(ctx, request)
	if err != nil {
		return nil, errors.WithStack(err)
	}
	if err := appendInterfaceConfig(ctx, conn, fmt.Sprintf("server-%s", conn.GetId())); err != nil {
		return nil, err
	}
	return conn, nil
}

func (k *kernelTapServer) Close(ctx context.Context, conn *connection.Connection) (*empty.Empty, error) {
	if err := appendInterfaceConfig(ctx, conn, fmt.Sprintf("server-%s", conn.GetId())); err != nil {
		return nil, err
	}
	return next.Server(ctx).Close(ctx, conn)
}