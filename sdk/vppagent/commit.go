package vppagent

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/ligato/vpp-agent/api/configurator"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/local/connection"
	"github.com/networkservicemesh/networkservicemesh/controlplane/pkg/apis/local/networkservice"
	"github.com/networkservicemesh/networkservicemesh/pkg/tools"
	"github.com/networkservicemesh/networkservicemesh/sdk/common"
	"github.com/networkservicemesh/networkservicemesh/sdk/endpoint"
	"google.golang.org/grpc"
	"github.com/sirupsen/logrus"
)

const (
	createConnectionTimeout = 120 * time.Second
	createConnectionSleep   = 100 * time.Millisecond
)

// Commit is a VPP Agent Commit composite
type Commit struct {
	Endpoint           string
	shouldResetVpp     bool
	vppagentConnection *grpc.ClientConn
}

// Request implements the request handler
// Provides/Consumes from ctx context.Context:
//     VppAgentConfig
//	   Next
func (c *Commit) Request(ctx context.Context, request *networkservice.NetworkServiceRequest) (*connection.Connection, error) {
	ctx = WithConfig(ctx) // Guarantees we will retrieve a non-nil VppAgentConfig from context.Context
	vppAgentConfig := Config(ctx)
	if vppAgentConfig == nil {
		return nil, fmt.Errorf("received empty VppAgentConfig")
	}

	endpoint.Log(ctx).Infof("Sending VppAgentConfig to VPP Agent: %v", vppAgentConfig)

	if err := c.send(ctx, vppAgentConfig); err != nil {
		return nil, fmt.Errorf("failed to send vppAgentConfig to VPP Agent: %v", err)
	}
	if endpoint.Next(ctx) != nil {
		return endpoint.Next(ctx).Request(ctx, request)
	}
	return request.GetConnection(), nil
}

// Close implements the close handler
// Provides/Consumes from ctx context.Context:
//     VppAgentConfig
//	   Next
func (c *Commit) Close(ctx context.Context, connection *connection.Connection) (*empty.Empty, error) {
	ctx = WithConfig(ctx) // Guarantees we will retrieve a non-nil VppAgentConfig from context.Context
	vppAgentConfig := Config(ctx)

	if vppAgentConfig == nil {
		return nil, fmt.Errorf("received empty vppAgentConfig")
	}

	endpoint.Log(ctx).Infof("Sending vppAgentConfig to VPP Agent: %v", vppAgentConfig)

	if err := c.remove(ctx, vppAgentConfig); err != nil {
		return nil, fmt.Errorf("failed to send DataChange to VPP Agent: %v", err)
	}

	if endpoint.Next(ctx) != nil {
		return endpoint.Next(ctx).Close(ctx, connection)
	}
	return &empty.Empty{}, nil
}

// NewCommit creates a new Commit endpoint.  The Commit endpoint commits
// any changes accumulated in the vppagent.Config in the context.Context
// to vppagent
func NewCommit(configuration *common.NSConfiguration, endpoint string, shouldResetVpp bool) *Commit {
	// ensure the env variables are processed
	if configuration == nil {
		configuration = &common.NSConfiguration{}
	}
	configuration.CompleteNSConfiguration()

	self := &Commit{
		Endpoint:       endpoint,
		shouldResetVpp: shouldResetVpp,
	}
	return self
}

// Init will reset the vpp shouldResetVpp is true
func (c *Commit) Init(*endpoint.InitContext) error {
	conn, err := c.createConnection(context.TODO())
	if err != nil {
		return err
	}
	c.vppagentConnection = conn
	if c.shouldResetVpp {
		return c.init()
	}
	return nil
}

func (c *Commit) createConnection(ctx context.Context) (*grpc.ClientConn, error) {
	start := time.Now()
	logrus.Info("Creating connection to vppagent")
	if err := tools.WaitForPortAvailable(ctx, "tcp", c.Endpoint, createConnectionSleep); err != nil {
		return nil, err
	}

	rv, err := tools.DialTCP(c.Endpoint)
	if err != nil {
		return nil, fmt.Errorf("can't dial grpc server: %v", err)
	}
	logrus.Infof("Connection to vppagent created.  Elapsed time: %s", time.Since(start))

	return rv, nil
}

func (c *Commit) send(ctx context.Context, dataChange *configurator.Config) error {
	client := configurator.NewConfiguratorClient(c.vppagentConnection)

	if _, err := client.Update(ctx, &configurator.UpdateRequest{Update: dataChange}); err != nil {
		_, _ = client.Delete(ctx, &configurator.DeleteRequest{Delete: dataChange})
		return err
	}
	return nil
}

func (c *Commit) remove(ctx context.Context, dataChange *configurator.Config) error {
	client := configurator.NewConfiguratorClient(c.vppagentConnection)

	if _, err := client.Delete(ctx, &configurator.DeleteRequest{Delete: dataChange}); err != nil {
		return err
	}
	return nil
}

// Reset - Resets vppagent
func (c *Commit) init() error {
	client := configurator.NewConfiguratorClient(c.vppagentConnection)
	if c.shouldResetVpp {
		ctx, cancel := context.WithTimeout(context.Background(), createConnectionTimeout)
		defer cancel()
		_, err := client.Update(ctx, &configurator.UpdateRequest{
			Update:     &configurator.Config{},
			FullResync: true,
		})
		if err != nil {
			return err
		}
	}
	return nil
}