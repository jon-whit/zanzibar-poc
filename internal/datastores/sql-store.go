package datastores

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v4"
	"github.com/jackc/pgx/v4/pgxpool"
	pb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	accesscontroller "github.com/jon-whit/zanzibar-poc/access-controller/internal"
	log "github.com/sirupsen/logrus"
)

type SQLStore struct {
	ConnPool *pgxpool.Pool
}

func (s *SQLStore) Usersets(object accesscontroller.Object, relation string) ([]accesscontroller.Userset, error) {
	cctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := s.ConnPool.Acquire(cctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	args := []interface{}{object.ID, relation}
	query := fmt.Sprintf(`SELECT "user" FROM %s WHERE object=$1 AND relation=$2`, object.Namespace)

	rows, err := conn.Query(context.TODO(), query, args...)
	if err != nil {
		log.Fatalf("Failed to query Usersets: %v", err)
	}

	usersets := []accesscontroller.Userset{}
	for rows.Next() {
		var user string
		err := rows.Scan(&user)
		if err != nil {
			return nil, err
		}

		parts := strings.Split(user, "#")
		if len(parts) != 2 {
			continue
		}

		objectParts := strings.Split(parts[0], ":")

		userset := accesscontroller.Userset{
			Object: accesscontroller.Object{
				Namespace: objectParts[0],
				ID:        objectParts[1],
			},
			Relation: parts[1],
		}

		usersets = append(usersets, userset)
	}
	rows.Close()

	return usersets, nil
}

func (s *SQLStore) WriteRelationTupleDeltas(ctx context.Context, deltas []pb.RelationTupleDelta) error {
	txn, err := s.ConnPool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	b := pgx.Batch{}
	for i := 0; i < len(deltas); i++ {
		action := deltas[i].Action
		tuple := deltas[i].GetRelationTuple()

		switch action {
		case pb.RelationTupleDelta_INSERT:
			statement := fmt.Sprintf("INSERT INTO %s(object,relation,user) VALUES ($1,$2,$3)", tuple.GetNamespace())
			_ = statement
			//b.Queue(statement, tuple.GetObject(), tuple.GetRelation(), something)
		case pb.RelationTupleDelta_DELETE:
			statement := fmt.Sprintf("DELETE FROM %s WHERE object=$1 AND relation=$2 AND user=$3", tuple.GetNamespace())
			_ = statement
			//b.Queue(statement, tuple.GetObject(), tuple.GetRelation(), something)
		}
	}

	batchResults := txn.SendBatch(ctx, &b)
	batchResults.Close()

	if err := txn.Commit(ctx); err != nil {
		return err
	}

	return nil
}
