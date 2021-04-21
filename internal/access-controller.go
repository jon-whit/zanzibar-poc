package accesscontroller

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/buraksezer/consistent"
	"github.com/cespare/xxhash"
	"github.com/hashicorp/memberlist"
	aclpb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func init() {
	log.SetLevel(log.TraceLevel)
}

type AccessController struct {
	aclpb.UnimplementedCheckServiceServer
	aclpb.UnimplementedWriteServiceServer
	aclpb.UnimplementedReadServiceServer

	*Node
	RelationTupleStore
	NamespaceManager
	ClusterNodeConfigs
}

type ClusterNodeConfigs struct {
	ServerID   string
	Advertise  string
	Join       string
	NodePort   int
	ServerPort int
}

type hasher struct{}

func (h hasher) Sum64(data []byte) uint64 {
	return xxhash.Sum64(data)
}

type AccessControllerOption func(*AccessController)

func WithStore(store RelationTupleStore) AccessControllerOption {
	return func(ac *AccessController) {
		ac.RelationTupleStore = store
	}
}

func WithNamespaceManager(m NamespaceManager) AccessControllerOption {
	return func(ac *AccessController) {
		ac.NamespaceManager = m
	}
}

func WithClusterNodeConfigs(cfg ClusterNodeConfigs) AccessControllerOption {
	return func(ac *AccessController) {
		ac.ClusterNodeConfigs = cfg
	}
}

func NewAccessController(opts ...AccessControllerOption) (*AccessController, error) {

	ac := AccessController{}

	for _, opt := range opts {
		opt(&ac)
	}

	ring := consistent.New(nil, consistent.Config{
		Hasher:            &hasher{},
		PartitionCount:    31,
		ReplicationFactor: 3,
		Load:              1.25,
	})

	node := &Node{
		ID:        ac.ServerID,
		RpcRouter: NewMapClientRouter(),
		Hashring: &ConsistentHashring{
			Ring: ring,
		},
	}
	ac.Node = node

	memberlistConfig := memberlist.DefaultLANConfig()
	memberlistConfig.Name = node.ID

	if ac.Advertise != "" {
		memberlistConfig.AdvertiseAddr = ac.Advertise
	}

	memberlistConfig.BindPort = ac.NodePort
	memberlistConfig.Events = node

	list, err := memberlist.Create(memberlistConfig)
	if err != nil {
		return nil, err
	}
	node.Memberlist = list

	meta, err := json.Marshal(NodeMetadata{
		Port: ac.ServerPort,
	})
	if err != nil {
		return nil, err
	}

	list.LocalNode().Meta = meta

	if ac.Join != "" {
		joinAddrs := strings.Split(ac.Join, ",")

		if numJoined, err := list.Join(joinAddrs); err != nil {
			if numJoined < 1 {
				// todo: account for this node
				return nil, err
			}
		}
	}

	return &ac, nil
}

func (a *AccessController) checkLeaf(ctx context.Context, operand interface{}, namespace, object, user string) (bool, error) {

	switch rewrite := operand.(type) {
	case ThisRelation:
		obj := Object{
			Namespace: namespace,
			ID:        object,
		}

		// do direct check here
		query := RelationTupleQuery{
			Object:    obj,
			Relations: []string{rewrite.Relation},
			Subject:   &SubjectID{ID: user},
		}
		count, _ := a.RelationTupleStore.RowCount(ctx, query)
		// todo: capture error

		if count > 0 {
			return true, nil
		}

		// compute indirect ACLs referenced by usersets from the tuples
		// SELECT * FROM namespace WHERE relation=<rewrite.relation> AND user LIKE '_%%:_%%#_%%'
		usersets, _ := a.RelationTupleStore.Usersets(ctx, obj, rewrite.Relation)
		// todo: capture error

		for _, userset := range usersets {

			permitted, err := a.check(ctx, userset.Object.Namespace, userset.Object.ID, userset.Relation, user)
			if err != nil {
				return false, err
			}

			if permitted {
				return true, nil
			}
		}

		return false, nil
	case ComputedUserset:
		return a.check(ctx, namespace, object, rewrite.Relation, user)
	case TupleToUserset:

		obj := Object{
			Namespace: namespace,
			ID:        object,
		}

		usersets, _ := a.RelationTupleStore.Usersets(ctx, obj, rewrite.Tupleset.Relation)

		for _, userset := range usersets {
			relation := userset.Relation

			if relation == "..." {
				relation = rewrite.ComputedUserset.Relation
			}

			permitted, err := a.check(ctx, userset.Object.Namespace, userset.Object.ID, relation, user)
			if err != nil {
				return false, err
			}

			if permitted {
				return true, nil
			}
		}

		return false, nil
	}

	return false, nil
}

func (a *AccessController) checkRewrite(ctx context.Context, rule *RewriteRule, namespace, object, user string) (bool, error) {

	if rule.Children == nil || len(rule.Children) < 1 {
		return a.checkLeaf(ctx, rule.Operand, namespace, object, user)
	}

	checkOutcomeCh := make(chan bool)
	errCh := make(chan error)

	var wg sync.WaitGroup

	switch rule.Operator {
	case Intersection:

		for _, child := range rule.Children {
			wg.Add(1)

			go func(child *RewriteRule) {
				defer wg.Done()

				permitted, err := a.checkRewrite(ctx, child, namespace, object, user)
				if err != nil {
					errCh <- err
					return
				}

				if !permitted {
					checkOutcomeCh <- false
				}
			}(child)
		}

		go func() {
			wg.Wait()
			checkOutcomeCh <- true
		}()

		select {
		case err := <-errCh:
			return false, err
		case outcome := <-checkOutcomeCh:
			return outcome, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	case Union:

		for _, child := range rule.Children {
			wg.Add(1)

			// evaluate each child rule of the expression concurrently
			go func(child *RewriteRule) {
				defer wg.Done()

				permitted, err := a.checkRewrite(ctx, child, namespace, object, user)
				if err != nil {
					errCh <- err
					return
				}

				if permitted {
					checkOutcomeCh <- true
				}
			}(child)
		}

		go func() {
			wg.Wait()
			checkOutcomeCh <- false
		}()

		select {
		case err := <-errCh:
			return false, err
		case outcome := <-checkOutcomeCh:
			return outcome, nil
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}

	return false, nil
}

func (a *AccessController) check(ctx context.Context, namespace, object, relation, user string) (bool, error) {

	if peerChecksum, ok := FromContext(ctx); ok {
		// The hash ring checksum of the peer should always be present if the
		// request is proxied from another access-controller. If the request
		// is made externally it won't be present.
		if a.Hashring.Checksum() != peerChecksum {
			return false, status.Error(codes.Internal, "Hashring checksums don't match. Retry again soon!")
		}
	}

	forwardingNodeID := a.Hashring.LocateKey([]byte(object))
	if forwardingNodeID != a.ID {

		log.Tracef("Proxying Check RPC request to node '%v'..", forwardingNodeID)

		c, err := a.RpcRouter.GetClient(forwardingNodeID)
		if err != nil {
			// todo: handle error better
		}

		client, ok := c.(aclpb.CheckServiceClient)
		if !ok {
			// todo: handle error better
		}

		modifiedCtx := context.WithValue(ctx, hashringChecksumKey, a.Hashring.Checksum())

		subject := SubjectID{ID: user}

		req := &aclpb.CheckRequest{
			Namespace: namespace,
			Object:    object,
			Relation:  relation,
			Subject:   subject.ToProto(),
		}

		var resp *aclpb.CheckResponse
		resp, err = client.Check(modifiedCtx, req)

		for retries := 0; err != nil && status.Code(err) != codes.Canceled; retries++ {
			log.Tracef("Check proxy RPC failed with error: %v. Retrying..", err)

			if retries > 5 {
				goto EVAL // fallback to evaluating the query locally
			}

			forwardingNodeID := a.Hashring.LocateKey([]byte(object))
			if forwardingNodeID == a.ID {
				goto EVAL
			}

			log.Tracef("Proxying Check RPC request to node '%v'..", forwardingNodeID)

			c, err = a.RpcRouter.GetClient(forwardingNodeID)
			if err != nil {
				continue
			}

			client, ok := c.(aclpb.CheckServiceClient)
			if !ok {
				// todo: handle error better
			}

			modifiedCtx := context.WithValue(ctx, hashringChecksumKey, a.Hashring.Checksum())
			resp, err = client.Check(modifiedCtx, req)
		}

		if resp != nil {
			return resp.GetAllowed(), nil
		}
	}

EVAL:
	rewrite, err := a.NamespaceManager.GetRewriteRule(ctx, namespace, relation)
	if err != nil {
		return false, err
	}

	if rewrite == nil {
		message := fmt.Sprintf("No namespace configuration for relation '%s#%s' exists", namespace, relation)
		return false, status.Error(codes.InvalidArgument, message)
	}

	return a.checkRewrite(ctx, rewrite, namespace, object, user)
}

func (a *AccessController) Check(ctx context.Context, req *aclpb.CheckRequest) (*aclpb.CheckResponse, error) {
	subject := SubjectFromProto(req.GetSubject())

	response := aclpb.CheckResponse{}

	permitted, err := a.check(ctx, req.GetNamespace(), req.GetObject(), req.GetRelation(), subject.String())
	if err != nil {
		return nil, err
	}

	response.Allowed = permitted

	return &response, nil
}

func (a *AccessController) WriteRelationTuplesTxn(ctx context.Context, req *aclpb.WriteRelationTuplesTxnRequest) (*aclpb.WriteRelationTuplesTxnResponse, error) {

	inserts := []*InternalRelationTuple{}
	deletes := []*InternalRelationTuple{}

	for _, delta := range req.GetRelationTupleDeltas() {
		action := delta.GetAction()
		rt := delta.GetRelationTuple()

		irt := InternalRelationTuple{
			Namespace: rt.GetNamespace(),
			Object:    rt.GetObject(),
			Relation:  rt.GetRelation(),
			Subject:   SubjectFromProto(rt.GetSubject()),
		}

		switch action {
		case aclpb.RelationTupleDelta_INSERT:
			inserts = append(inserts, &irt)
		case aclpb.RelationTupleDelta_DELETE:
			deletes = append(deletes, &irt)
		}
	}

	if err := a.RelationTupleStore.TransactRelationTuples(ctx, inserts, deletes); err != nil {
		return nil, err
	}

	return &aclpb.WriteRelationTuplesTxnResponse{}, nil
}

func (a *AccessController) ListRelationTuples(ctx context.Context, req *aclpb.ListRelationTuplesRequest) (*aclpb.ListRelationTuplesResponse, error) {

	tuples, err := a.RelationTupleStore.ListRelationTuples(ctx, req.GetQuery(), req.GetExpandMask())
	if err != nil {
		return nil, err
	}

	protoTuples := []*aclpb.RelationTuple{}
	for _, tuple := range tuples {
		protoTuples = append(protoTuples, tuple.ToProto())
	}

	response := aclpb.ListRelationTuplesResponse{
		RelationTuples: protoTuples,
	}

	return &response, nil
}

func (a *AccessController) Close() error {
	return a.Node.Memberlist.Leave(5 * time.Second)
}
