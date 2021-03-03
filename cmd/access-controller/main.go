package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
	"github.com/google/uuid"
	"github.com/hashicorp/memberlist"
	"github.com/jackc/pgx/v4/pgxpool"
	pb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	accesscontroller "github.com/jon-whit/zanzibar-poc/access-controller/internal"
	"github.com/jon-whit/zanzibar-poc/access-controller/internal/datastores"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

var serverID = flag.String("id", uuid.New().String(), "A unique identifier for the server. Defaults to a new uuid.")
var nodePort = flag.Int("node-port", 7946, "The bind port for the cluster node")
var advertise = flag.String("advertise", "", "The address that this node advertises on within the cluster")
var serverPort = flag.Int("p", 50052, "The bind port for the grpc server")
var join = flag.String("join", "", "A comma-separated list of 'host:port' addresses for nodes in the cluster to join to")
var insecure = flag.Bool("insecure", false, "Run in insecure mode (no tls)")
var namespaceConfigPath = flag.String("namespace-config", "./testdata/namespace-configs", "The dir path to the namespace configurations")

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}

func main() {

	flag.Parse()

	dsn := "postgresql://jonwhitaker@localhost:5432/postgres"
	var pool *pgxpool.Pool
	pool, err := pgxpool.Connect(context.TODO(), dsn)
	if err != nil {
		log.Fatalf("Failed to establish a connection to Postgres: %v", err)
	}

	datastore := &datastores.SQLStore{
		ConnPool: pool,
	}

	ring := consistent.New(nil, consistent.Config{
		Hasher:            &hasher{},
		PartitionCount:    31,
		ReplicationFactor: 3,
		Load:              1.25,
	})

	node := accesscontroller.Node{
		ID:        *serverID,
		RpcRouter: accesscontroller.NewMapClientRouter(),
		Hashring: &accesscontroller.ConsistentHashring{
			Ring: ring,
		},
	}

	memberlistConfig := memberlist.DefaultLANConfig()
	memberlistConfig.Name = node.ID

	if *advertise != "" {
		memberlistConfig.AdvertiseAddr = *advertise
	}

	memberlistConfig.BindPort = *nodePort
	memberlistConfig.Events = &node

	list, err := memberlist.Create(memberlistConfig)
	if err != nil {
		panic("Failed to create memberlist: " + err.Error())
	}
	node.Memberlist = list

	controller, err := accesscontroller.NewAccessController(datastore, *namespaceConfigPath)
	if err != nil {
		log.Fatalf("Failed to initialize the Access Controller: %v", err)
	}
	controller.Node = &node

	m, err := json.Marshal(accesscontroller.NodeMetadata{
		Port: *serverPort,
	})
	if err != nil {
		log.Fatalf("Failed to json.Marshal the cluster node metadata: %v", err)
	}

	list.LocalNode().Meta = m

	if *join != "" {
		joinAddrs := strings.Split(*join, ",")
		if numJoined, err := list.Join(joinAddrs); err != nil {
			if numJoined < 1 {
				// todo: account for this node
				panic("Failed to join cluster: " + err.Error())
			}
		}
	}

	addr := fmt.Sprintf(":%d", *serverPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to start the TCP listener for '%v': %v", addr, err)
	}

	var opts []grpc.ServerOption
	server := grpc.NewServer(opts...)
	pb.RegisterCheckServiceServer(server, controller)
	pb.RegisterWriteServiceServer(server, controller)
	pb.RegisterReadServiceServer(server, controller)
	//pb.RegisterExpandServiceServer(server, controller)

	go func() {
		reflection.Register(server)

		if err := server.Serve(listener); err != nil {
			log.Fatalf("Failed to start the gRPC server: %v", err)
		}
	}()

	exit := make(chan os.Signal, 1)
	signal.Notify(exit, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	<-exit

	server.Stop()
	if err := list.Leave(5 * time.Second); err != nil {
		log.Error("Timed out waiting for this cluster member to leave the cluster..")
	}
}
