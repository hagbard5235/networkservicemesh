package heal

import (
	"context"
	"runtime"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/pkg/errors"
	"google.golang.org/grpc"

	"github.com/networkservicemesh/networkservicemesh/controlplane/api/connection"
	"github.com/networkservicemesh/networkservicemesh/controlplane/api/networkservice"
	"github.com/networkservicemesh/networkservicemesh/new/sdk/networkservicemesh/core/next"
	"github.com/networkservicemesh/networkservicemesh/new/sdk/tools/extended_context"
	"github.com/networkservicemesh/networkservicemesh/new/sdk/tools/serialize"
)

type healClient struct {
	requestors        map[string]func()
	closers           map[string]func()
	reported          map[string]*connection.Connection
	onHeal            networkservice.NetworkServiceClient
	client            connection.MonitorConnectionClient
	eventReceiver     connection.MonitorConnection_MonitorConnectionsClient
	updateExecutor    serialize.Executor
	recvEventExecutor serialize.Executor
	cancelFunc        context.CancelFunc
}

func NewClient(client connection.MonitorConnectionClient, onHeal networkservice.NetworkServiceClient) networkservice.NetworkServiceClient {
	rv := &healClient{
		onHeal:            onHeal,
		requestors:        make(map[string]func()),
		closers:           make(map[string]func()),
		reported:          make(map[string]*connection.Connection),
		client:            client,
		updateExecutor:    serialize.NewExecutor(),
		eventReceiver:     nil,                     // This is intentionally nil
		recvEventExecutor: serialize.NewExecutor(), // This is intentionally nil
	}
	if rv.onHeal == nil {
		rv.onHeal = rv
	}
	rv.updateExecutor.AsyncExec(func() {
		runtime.SetFinalizer(rv, func(f *healClient) {
			f.updateExecutor.AsyncExec(func() {
				if f.cancelFunc != nil {
					f.cancelFunc()
				}
			})
		})
		ctx, cancelFunc := context.WithCancel(context.Background())
		rv.cancelFunc = cancelFunc
		// TODO decide what to do about err here
		recv, _ := rv.client.MonitorConnections(ctx, nil)
		rv.eventReceiver = recv
		rv.recvEventExecutor.AsyncExec(rv.recvEvent)
	})

	return rv
}

func (f *healClient) recvEvent() {
	select {
	case <-f.eventReceiver.Context().Done():
		f.eventReceiver = nil
	default:
		event, err := f.eventReceiver.Recv()
		if err != nil {
			event = nil
		}
		f.updateExecutor.AsyncExec(func() {
			switch event.GetType() {
			case connection.ConnectionEventType_INITIAL_STATE_TRANSFER:
				f.reported = event.GetConnections()
			case connection.ConnectionEventType_UPDATE:
				for _, conn := range event.GetConnections() {
					f.reported[conn.GetId()] = conn
				}
			case connection.ConnectionEventType_DELETE:
				for _, conn := range event.GetConnections() {
					delete(f.reported, conn.GetId())
					if f.requestors[conn.GetId()] != nil {
						f.requestors[conn.GetId()]()
					}
				}
			}
			for id, request := range f.requestors {
				if _, ok := f.reported[id]; !ok {
					request()
				}
			}
		})
	}
	f.recvEventExecutor.AsyncExec(f.recvEvent)
}

func (f *healClient) Request(ctx context.Context, request *networkservice.NetworkServiceRequest, opts ...grpc.CallOption) (*connection.Connection, error) {
	rv, err := next.Server(ctx).Request(ctx, request)
	if err != nil {
		return nil, errors.Wrap(err, "Error calling next")
	}
	// Clone the request
	req := request.Clone()
	// Set its connection to the returned connection we received
	req.Connection = rv

	// TODO handle deadline err
	deadline, _ := ctx.Deadline()
	duration := deadline.Sub(time.Now())
	f.updateExecutor.AsyncExec(func() {
		f.requestors[req.GetConnection().GetId()] = func() {
			timeCtx, _ := context.WithTimeout(context.Background(), duration)
			ctx = extended_context.New(timeCtx, ctx)
			// TODO wrap another span around this
			f.onHeal.Request(ctx, req, opts...)
		}
		f.closers[req.GetConnection().GetId()] = func() {
			timeCtx, _ := context.WithTimeout(context.Background(), duration)
			ctx = extended_context.New(timeCtx, ctx)
			f.onHeal.Close(extended_context.New(timeCtx, ctx), req.GetConnection(), opts...)
		}
	})
	return rv, nil
}

func (f *healClient) Close(ctx context.Context, conn *connection.Connection, opts ...grpc.CallOption) (*empty.Empty, error) {
	rv, err := next.Server(ctx).Close(ctx, conn)
	if err != nil {
		return nil, errors.Wrap(err, "Error calling next")
	}
	f.updateExecutor.AsyncExec(func() {
		delete(f.requestors, conn.GetId())
	})
	return rv, nil
}