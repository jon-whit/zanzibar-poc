package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v4/pgxpool"
	pb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	ac "github.com/jon-whit/zanzibar-poc/access-controller/internal"
	"github.com/jon-whit/zanzibar-poc/access-controller/internal/datastores"
	"github.com/jon-whit/zanzibar-poc/access-controller/internal/namespace-manager/inmem"
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
var namespaceConfigPath = flag.String("namespace-config", "/Users/Jonathan/github/jon-whit/zanzibar-poc/testdata/namespace-configs", "The path to the namespace configurations")

func main() {

	flag.Parse()

	dsn := "postgresql://jonwhitaker@localhost:5432/postgres"
	pool, err := pgxpool.Connect(context.TODO(), dsn)
	if err != nil {
		log.Fatalf("Failed to establish a connection to Postgres database: %v", err)
	}

	datastore := &datastores.SQLStore{
		ConnPool: pool,
	}

	m, err := inmem.NewNamespaceManager(*namespaceConfigPath)
	if err != nil {
		log.Fatalf("Failed to initialize in-memory NamespaceManager: %v", err)
	}

	ctrlOpts := []ac.AccessControllerOption{
		ac.WithStore(datastore),
		ac.WithNamespaceManager(m),
		ac.WithClusterNodeConfigs(ac.ClusterNodeConfigs{
			ServerID:   *serverID,
			Advertise:  *advertise,
			Join:       *join,
			NodePort:   *nodePort,
			ServerPort: *serverPort,
		}),
	}
	controller, err := ac.NewAccessController(ctrlOpts...)
	if err != nil {
		log.Fatalf("Failed to initialize the Access Controller: %v", err)
	}

	addr := fmt.Sprintf(":%d", *serverPort)
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to start the TCP listener for '%v': %v", addr, err)
	}

	grpcOpts := []grpc.ServerOption{}
	server := grpc.NewServer(grpcOpts...)
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
	if err := controller.Close(); err != nil {
		log.Errorf("Failed to gracefully close AccessController: %v", err)
	}

	log.Info("Server Shutdown..")
}
