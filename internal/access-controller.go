package accesscontroller

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	"github.com/jackc/pgx/v4/pgxpool"
	pb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	log "github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/yaml.v2"
)

type AccessController struct {
	pb.UnimplementedCheckServiceServer
	pb.UnimplementedWriteServiceServer

	*Node
	db *pgxpool.Pool
	RelationTupleStore
	computedRules map[string]UsersetRewrite
}

func (a *AccessController) computedUserset(object Object, relation string) []Userset {

	rules, ok := a.computedRules[object.Namespace+"#"+relation]
	if !ok || len(rules.Union) == 0 {
		return []Userset{}
	}

	var res []Userset
	for _, alias := range rules.Union {
		aliasRelation := alias.ComputedUserset.Relation
		if aliasRelation == "" {
			continue
		}

		res = append(res, Userset{
			Object:   object,
			Relation: aliasRelation,
		})
	}

	return res
}

func (a *AccessController) tupleUserset(object Object, relation string) []Userset {
	rules, ok := a.computedRules[object.Namespace+"#"+relation]
	if !ok || len(rules.Union) == 0 {
		return []Userset{}
	}

	var res []Userset
	for _, alias := range rules.Union {
		tuplesetRelation := alias.TupleToUserset.Tupleset.Relation
		if tuplesetRelation == "" {
			continue
		}

		usersets, err := a.RelationTupleStore.Usersets(object, tuplesetRelation)
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

	return res
}

func NewAccessController(pool *pgxpool.Pool, store RelationTupleStore, rewriteRulesPath string) (*AccessController, error) {
	rules, err := LoadRewriteRules(rewriteRulesPath)
	if err != nil {
		panic(err)
	}

	return &AccessController{
		db:                 pool,
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

func (a *AccessController) Check(ctx context.Context, req *pb.CheckRequest) (*pb.CheckResponse, error) {

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

		client, ok := c.(pb.CheckServiceClient)
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

			client, ok := c.(pb.CheckServiceClient)
			if !ok {
				// todo: handle error better
			}

			modifiedCtx := context.WithValue(ctx, hashringChecksumKey, a.Hashring.Checksum())
			resp, err = client.Check(modifiedCtx, req)
		}

		return resp, nil
	}

LABEL:
	namespace := req.GetNamespace()
	relation := req.GetRelation()
	subject := SubjectFromProto(req.GetSubject())
	subjectStr := subject.String()

	object := Object{
		Namespace: namespace,
		ID:        objectStr,
	}

	// evaluate leaf nodes CHECK(U, object#relation) including immediate
	// userset rewrites
	computedUsersets := []Userset{}
	computedUsersets = append(computedUsersets, a.computedUserset(object, relation)...)

	for _, computedSet := range computedUsersets {
		computedUsersets = append(computedUsersets, a.computedUserset(computedSet.Object, computedSet.Relation)...)
	}
	computedUsersets = append(computedUsersets, Userset{
		Object:   object,
		Relation: relation,
	})

	relations := []string{}
	for _, computedSet := range computedUsersets {
		relations = append(relations, computedSet.Relation)
	}

	fmt.Printf("(%s:%s) relations '%v'\n", object.Namespace, object.ID, relations)

	conn, err := a.db.Acquire(context.Background())
	if err != nil {
		log.Fatalf("Failed to acquire db conn: %v", err)
	}

	args := []interface{}{object.ID, relations, subjectStr}
	query := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE object=$1 AND relation=any($2) AND "user"=$3`, object.Namespace)

	var count int
	row := conn.QueryRow(context.Background(), query, args...)
	if err := row.Scan(&count); err != nil {
		log.Fatalf("Failed to Scan the database row in: %v", err)
	}

	if count > 0 {
		return &pb.CheckResponse{
			Allowed: true,
		}, nil
	}

	// evaluate indirect relations from the userset rewrites
	args = []interface{}{object.ID, relations}
	query = fmt.Sprintf(`SELECT "user" FROM %s WHERE object=$1 AND relation=any($2) AND "user" LIKE '_%%:_%%#_%%'`, object.Namespace)

	rows, err := conn.Query(context.Background(), query, args...)
	if err != nil {
		log.Fatalf("Failed to query indirect ACLs: %v", err)
	}

	usersets := []Userset{}
	for rows.Next() {
		var s string
		err := rows.Scan(&s)
		if err != nil {
			log.Fatalf("Failed to Scan the database row in: %v", err)
		}

		subjectSet, err := SubjectSetFromString(s)
		if err != nil {
			return nil, err
		}

		userset := Userset{
			Object: Object{
				Namespace: subjectSet.Namespace,
				ID:        subjectSet.Object,
			},
			Relation: subjectSet.Relation,
		}

		usersets = append(usersets, userset)
	}
	rows.Close()
	conn.Release()

	for _, userset := range usersets {
		resp, err := a.Check(ctx, &pb.CheckRequest{
			Namespace: userset.Object.Namespace,
			Object:    userset.Object.ID,
			Relation:  userset.Relation,
			Subject: &pb.Subject{
				Ref: &pb.Subject_Id{
					Id: subjectStr,
				},
			},
		})
		if err != nil {
			log.Fatalf("Check failed: %v", err)
		}

		if resp.Allowed {
			return &pb.CheckResponse{
				Allowed: true,
			}, nil
		}
	}

	// evaluate relations for inherited relations
	tupleUsersets := []Userset{}
	tupleUsersets = append(tupleUsersets, a.tupleUserset(object, relation)...)

	for _, tupleSet := range tupleUsersets {
		tupleUsersets = append(tupleUsersets, a.tupleUserset(tupleSet.Object, tupleSet.Relation)...)
	}

	for _, tupleSet := range tupleUsersets {
		resp, err := a.Check(ctx, &pb.CheckRequest{
			Namespace: tupleSet.Object.Namespace,
			Object:    tupleSet.Object.ID,
			Relation:  tupleSet.Relation,
			Subject: &pb.Subject{
				Ref: &pb.Subject_Id{
					Id: subjectStr,
				},
			},
		})
		if err != nil {
			log.Fatalf("Check failed: %v", err)
		}

		if resp.Allowed {
			return &pb.CheckResponse{
				Allowed: true,
			}, nil
		}
	}

	return &pb.CheckResponse{
		Allowed: false,
	}, nil
}

func (a *AccessController) WriteRelationTuplesTxn(ctx context.Context, req *pb.WriteRelationTuplesTxnRequest) (*pb.WriteRelationTuplesTxnResponse, error) {
	return nil, fmt.Errorf("Not Implemented")
}
