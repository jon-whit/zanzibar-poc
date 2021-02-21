package accesscontroller

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/hashicorp/memberlist"
	pb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
)

// Node represents a single node in a cluster. It contains the list
// of other members in the cluster and clients for communicating with
// other nodes.
type Node struct {
	rw sync.RWMutex

	// The unique identifier of the node within the cluster.
	ID string

	Memberlist *memberlist.Memberlist
	RpcRouter  ClientRouter
	Hashring   Hashring
}

type NodeMetadata struct {
	Port int `json:"port"`
}

// NotifyJoin is invoked when a new node has joined the cluster.
// The `member` argument must not be modified.
func (n *Node) NotifyJoin(member *memberlist.Node) {

	log.Infof("A new cluster member '%s' joined the cluster with address '%s'", member.String(), member.FullAddress().Addr)

	nodeID := member.String()
	if nodeID != n.ID {
		var meta NodeMetadata
		if err := json.Unmarshal(member.Meta, &meta); err != nil {
			// todo: handle error better
			log.Errorf("Failed to json.Unmarshal the Node metadata: %v", err)
		}

		remoteAddr := fmt.Sprintf("%s:%d", member.Addr, meta.Port)

		opts := []grpc.DialOption{
			grpc.WithInsecure(),
		}
		conn, err := grpc.Dial(remoteAddr, opts...)
		if err != nil {
			// todo: handle error better
			log.Errorf("Failed to establish a grpc client connection to cluster member '%s' at address '%s'", nodeID, remoteAddr)
			return
		}

		client := pb.NewCheckServiceClient(conn)

		n.RpcRouter.AddClient(nodeID, client)
	}

	n.Hashring.Add(member)
	log.Infof("hashring checksum: %d\n", n.Hashring.Checksum())
}

// NotifyLeave is invoked when a node leaves the cluster. The
// `member` argument must not be modified.
func (n *Node) NotifyLeave(member *memberlist.Node) {

	log.Infof("A cluster member '%v' at address '%v' left the cluster", member.String(), member.FullAddress().Addr)

	nodeID := member.String()
	if nodeID != n.ID {
		n.RpcRouter.RemoveClient(nodeID)
	}

	n.Hashring.Remove(member)
	log.Infof("hashring checksum: %d\n", n.Hashring.Checksum())
}

// NotifyUpdate is invoked when a node in the cluster is updated,
// usually involving the meta-data of the node. The `member` argument
// must not be modified.
func (n *Node) NotifyUpdate(member *memberlist.Node) {}
