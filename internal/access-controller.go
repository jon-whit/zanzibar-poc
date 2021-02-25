package accesscontroller

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-multierror"
	aclpb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v2"
)

type AccessController struct {
	aclpb.UnimplementedCheckServiceServer
	aclpb.UnimplementedWriteServiceServer
	aclpb.UnimplementedReadServiceServer

	*Node
	RelationTupleStore
	computedRules map[string]UsersetRewrite
}

func (a *AccessController) expandComputedUsersets(object Object, relation string) []Userset {

	rules, ok := a.computedRules[object.Namespace+"#"+relation]
	if !ok || len(rules.Union) == 0 {
		return []Userset{}
	}

	var computedUsersets []Userset
	for _, alias := range rules.Union {
		aliasRelation := alias.ComputedUserset.Relation
		if aliasRelation == "" {
			continue
		}

		computedUsersets = append(computedUsersets, Userset{
			Object:   object,
			Relation: aliasRelation,
		})
	}

	for _, computedSet := range computedUsersets {
		computedUsersets = append(computedUsersets, a.expandComputedUsersets(computedSet.Object, computedSet.Relation)...)
	}

	return computedUsersets
}

func (a *AccessController) expandTupleUsersets(object Object, relation string) []Userset {
	rules, ok := a.computedRules[object.Namespace+"#"+relation]
	if !ok || len(rules.Union) == 0 {
		return []Userset{}
	}

	var res, tupleUsersets []Userset
	for _, alias := range rules.Union {
		tuplesetRelation := alias.TupleToUserset.Tupleset.Relation

		if tuplesetRelation == "" {
			continue
		}

		usersets, err := a.RelationTupleStore.Usersets(context.TODO(), object, tuplesetRelation)
		if err != nil {
			log.Fatalf("Failed to get Usersets: %v", err)
		}

		aliasRelation := alias.TupleToUserset.ComputedUserset.Relation
		if aliasRelation == "" {
			continue
		}

		// filter by aliasRelation
		// if the userset doesn't have a relation(represented by `...`), asign aliasRelation, sample:
		//		`channel:audiobooks#...`
		//  transform to:
		//    `channel:audiobooks#editor`
		for i := range usersets {
			rel := usersets[i].Relation
			if rel == "..." {
				usersets[i].Relation = aliasRelation
			}

			if usersets[i].Relation == aliasRelation {
				res = append(res, usersets[i])
			}
		}
	}

	tupleUsersets = append(tupleUsersets, res...)
	for _, tupleSet := range res {
		tupleUsersets = append(tupleUsersets, a.expandTupleUsersets(tupleSet.Object, tupleSet.Relation)...)
	}

	return tupleUsersets
}

func NewAccessController(store RelationTupleStore, rewriteRulesPath string) (*AccessController, error) {
	rules, err := LoadRewriteRules(rewriteRulesPath)
	if err != nil {
		panic(err)
	}

	return &AccessController{
		computedRules:      rules,
		RelationTupleStore: store,
	}, nil
}

const configExt = "yaml"

// LoadRewriteRules gets rules from all yaml files inside the given directory
func LoadRewriteRules(root string) (map[string]UsersetRewrite, error) {
	res := make(map[string]UsersetRewrite)
	if root == "" {
		return res, nil
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		index := strings.LastIndex(path, ".")
		if index == -1 {
			return nil
		}

		ext := path[index+1:]
		if ext != configExt {
			return nil
		}

		blob, err := ioutil.ReadFile(path)
		if err != nil {
			return err
		}

		var t NamespaceConfig
		err = yaml.Unmarshal(blob, &t)
		if err != nil {
			return err
		}

		namespace := t.Name
		for _, rel := range t.Relations {
			res[namespace+"#"+rel.Name] = rel.UsersetRewrite
		}

		return nil
	})

	return res, err
}

func (a *AccessController) checkDirectRelations(ctx context.Context, object Object, relation string, subject Subject, allowed chan<- bool) error {

	done := make(chan struct{})
	errCh := make(chan error)
	go func() {
		defer func() { done <- struct{}{} }()

		computedUsersets := a.expandComputedUsersets(object, relation)

		relations := []string{relation}
		for _, computedSet := range computedUsersets {
			relations = append(relations, computedSet.Relation)
		}

		fmt.Printf("(%s:%s) relations '%v'\n", object.Namespace, object.ID, relations)

		count, err := a.RelationTupleStore.RowCount(ctx, RelationTupleQuery{
			Object:    object,
			Relations: relations,
			Subject:   subject,
		})
		if err != nil {
			errCh <- err
			return
		}

		if count > 0 {
			allowed <- true
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-done:
		return nil
	}
}

func (a *AccessController) checkIndirectRelations(ctx context.Context, object Object, relation string, subject Subject, allowed chan<- bool) error {

	done := make(chan struct{})
	errCh := make(chan error)
	go func() {
		defer func() { done <- struct{}{} }()

		computedUsersets := a.expandComputedUsersets(object, relation)

		relations := []string{relation}
		for _, computedSet := range computedUsersets {
			relations = append(relations, computedSet.Relation)
		}

		usersets, err := a.RelationTupleStore.Usersets(ctx, object, relations...)
		if err != nil {
			errCh <- err
			return
		}

		for _, userset := range usersets {
			// todo: these checks can be run concurrently
			resp, err := a.Check(ctx, &aclpb.CheckRequest{
				Namespace: userset.Object.Namespace,
				Object:    userset.Object.ID,
				Relation:  userset.Relation,
				Subject: &aclpb.Subject{
					Ref: &aclpb.Subject_Id{
						Id: subject.String(),
					},
				},
			})
			if err != nil {
				errCh <- err
				return
			}

			if resp.Allowed {
				allowed <- true
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-done:
		return nil
	}
}

func (a *AccessController) checkInheritedRelations(ctx context.Context, object Object, relation string, subject Subject, allowed chan<- bool) error {

	done := make(chan struct{})
	errCh := make(chan error)
	go func() {
		defer func() { done <- struct{}{} }()

		tupleUsersets := a.expandTupleUsersets(object, relation)

		for _, tupleSet := range tupleUsersets {
			resp, err := a.Check(ctx, &aclpb.CheckRequest{
				Namespace: tupleSet.Object.Namespace,
				Object:    tupleSet.Object.ID,
				Relation:  tupleSet.Relation,
				Subject: &aclpb.Subject{
					Ref: &aclpb.Subject_Id{
						Id: subject.String(),
					},
				},
			})
			if err != nil {
				if err == context.Canceled {
				} else {
					errCh <- err
					return
				}
			}

			if resp.Allowed {
				allowed <- true
				return
			}
		}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-errCh:
		return err
	case <-done:
		return nil
	}
}

func (a *AccessController) Check(ctx context.Context, req *aclpb.CheckRequest) (*aclpb.CheckResponse, error) {

	if peerChecksum, ok := FromContext(ctx); ok {
		// The hash ring checksum of the peer should always be present if the
		// request is proxied from another Access Controller. If the request
		// is made externally it won't be present.
		if a.Hashring.Checksum() != peerChecksum {
			return nil, status.Error(codes.Internal, "Hashring checksums don't match. Retry again soon!")
		}
	}

	objectStr := req.GetObject()

	forwardingNodeID := a.Hashring.LocateKey([]byte(objectStr))
	if forwardingNodeID != a.ID {

		c, err := a.RpcRouter.GetClient(forwardingNodeID)
		if err != nil {
			// todo: handle error better
		}

		client, ok := c.(aclpb.CheckServiceClient)
		if !ok {
			// todo: handle error better
		}

		modifiedCtx := context.WithValue(ctx, hashringChecksumKey, a.Hashring.Checksum())
		resp, err := client.Check(modifiedCtx, req)

		for retries := 0; err != nil; retries++ {
			if retries > 5 {
				goto LABEL // fallback to evaluating the query locally
			}

			forwardingNodeID := a.Hashring.LocateKey([]byte(objectStr))
			if forwardingNodeID == a.ID {
				goto LABEL
			}

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

		return resp, nil
	}

LABEL:
	object := Object{
		Namespace: req.GetNamespace(),
		ID:        objectStr,
	}
	relation := req.GetRelation()
	subject := SubjectFromProto(req.GetSubject())

	//cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()

	allowed := make(chan bool)

	group := multierror.Group{}
	group.Go(func() error {
		// evaluate leaf nodes CHECK(U, object#relation) including immediate
		// userset rewrites
		return a.checkDirectRelations(cctx, object, relation, subject, allowed)
	})

	group.Go(func() error {
		// evaluate indirect relations from the userset rewrites
		return a.checkIndirectRelations(cctx, object, relation, subject, allowed)
	})

	group.Go(func() error {
		// evaluate relations for inherited relations
		return a.checkInheritedRelations(cctx, object, relation, subject, allowed)
	})

	wgDone := make(chan struct{})
	var multierr *multierror.Error
	go func() {
		multierr = group.Wait()
		wgDone <- struct{}{}
	}()

	select {
	case <-allowed:
		return &aclpb.CheckResponse{
			Allowed: true,
		}, nil
	case <-wgDone:
		if err := multierr.ErrorOrNil(); err != nil {
			return nil, err
		}

		return &aclpb.CheckResponse{
			Allowed: false,
		}, nil
	case <-cctx.Done():
		return nil, cctx.Err()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (a *AccessController) WriteRelationTuplesTxn(ctx context.Context, req *aclpb.WriteRelationTuplesTxnRequest) (*aclpb.WriteRelationTuplesTxnResponse, error) {
	return nil, fmt.Errorf("Not Implemented")
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
