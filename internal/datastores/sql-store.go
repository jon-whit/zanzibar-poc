package datastores

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v4/pgxpool"
	aclpb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	ac "github.com/jon-whit/zanzibar-poc/access-controller/internal"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"gorm.io/gorm"
)

type SQLStore struct {
	ConnPool *pgxpool.Pool
	DB       *gorm.DB
}

func (s *SQLStore) Usersets(ctx context.Context, object ac.Object, relations ...string) ([]ac.Userset, error) {

	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	conn, err := s.ConnPool.Acquire(cctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()

	args := []interface{}{object.ID, relations}
	query := fmt.Sprintf(`SELECT "user" FROM %s WHERE object=$1 AND relation=any($2) AND "user" LIKE '_%%:_%%#_%%'`, object.Namespace)

	rows, err := conn.Query(context.TODO(), query, args...)
	if err != nil {
		return nil, err
	}

	usersets := []ac.Userset{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		subjectSet, err := ac.SubjectSetFromString(s)
		if err != nil {
			return nil, err
		}

		userset := ac.Userset{
			Object: ac.Object{
				Namespace: subjectSet.Namespace,
				ID:        subjectSet.Object,
			},
			Relation: subjectSet.Relation,
		}

		usersets = append(usersets, userset)
	}
	rows.Close()

	return usersets, nil
}

func (s *SQLStore) RowCount(ctx context.Context, query ac.RelationTupleQuery) (int64, error) {

	conn, err := s.ConnPool.Acquire(ctx)
	if err != nil {
		return -1, err
	}
	defer conn.Release()

	args := []interface{}{query.Object.ID, query.Relations, query.Subject.String()}
	dbQuery := fmt.Sprintf(`SELECT COUNT(*) FROM %s WHERE object=$1 AND relation=any($2) AND "user"=$3`, query.Object.Namespace)

	row := conn.QueryRow(ctx, dbQuery, args...)
	if err := row.Scan(&count); err != nil {
		return -1, err
	}

	return count, nil
}

func (s *SQLStore) ListRelationTuples(ctx context.Context, query *aclpb.ListRelationTuplesRequest_Query, mask *fieldmaskpb.FieldMask) ([]ac.InternalRelationTuple, error) {

	// if len(mask.GetPaths()) > 0 {
	// 	dbQuery.Select(mask.GetPaths())
	// }
	dbQuery := s.DB.Table(query.GetNamespace())

	if query.GetObject() != "" {
		dbQuery.Where("object=?", query.GetObject())
	}

	if len(query.GetRelations()) > 0 {
		dbQuery.Where("relation=?", query.GetRelations())
	}

	if query.GetSubject() != nil {
		dbQuery.Where("user=?", query.GetSubject().String())
	}

	rows, err := dbQuery.Rows()
	if err != nil {
		return nil, err
	}

	tuples := []ac.InternalRelationTuple{}
	for rows.Next() {
		var object, relation, s string
		if err := rows.Scan(&object, &relation, &s); err != nil {
			return nil, err
		}

		subject, err := ac.SubjectFromString(s)
		if err != nil {
			return nil, err
		}

		tuples = append(tuples, ac.InternalRelationTuple{
			Namespace: query.GetNamespace(),
			Object:    object,
			Relation:  relation,
			Subject:   subject,
		})
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	return tuples, nil
}

func (s *SQLStore) TransactRelationTuples(ctx context.Context, tupleInserts []*ac.InternalRelationTuple, tupleDeletes []*ac.InternalRelationTuple) error {

	return s.DB.WithContext(ctx).Transaction(func(txn *gorm.DB) error {
		for _, tuple := range tupleInserts {
			if err := txn.WithContext(ctx).Table(tuple.Namespace).Create(struct{}{}).Error; err != nil {
				return err
			}
		}

		for _, tuple := range tupleDeletes {
			if err := txn.WithContext(ctx).Table(tuple.Namespace).Delete(struct{}{}).Error; err != nil {
				return err
			}
		}

		return nil
	})
}
