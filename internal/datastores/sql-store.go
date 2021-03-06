package datastores

import (
	"context"
	"time"

	"github.com/doug-martin/goqu/v9"
	_ "github.com/doug-martin/goqu/v9/dialect/postgres"
	"github.com/jackc/pgx/v4/pgxpool"

	aclpb "github.com/jon-whit/zanzibar-poc/access-controller/gen/go/iam/accesscontroller/v1alpha1"
	ac "github.com/jon-whit/zanzibar-poc/access-controller/internal"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type SQLStore struct {
	ConnPool *pgxpool.Pool
}

func (s *SQLStore) SubjectSets(ctx context.Context, object ac.Object, relations ...string) ([]ac.SubjectSet, error) {

	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	sqlbuilder := goqu.Dialect("postgres").From(object.Namespace).Select("user").Where(
		goqu.Ex{
			"object":   object.ID,
			"relation": relations,
			"user":     goqu.Op{"like": "_%%:_%%#_%%"},
		},
	)

	sql, args, err := sqlbuilder.ToSQL()
	if err != nil {
		return nil, err
	}

	rows, err := s.ConnPool.Query(cctx, sql, args...)
	if err != nil {
		return nil, err
	}

	subjects := []ac.SubjectSet{}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}

		subjectSet, err := ac.SubjectSetFromString(s)
		if err != nil {
			return nil, err
		}

		subjects = append(subjects, subjectSet)
	}
	rows.Close()

	return subjects, nil
}

func (s *SQLStore) RowCount(ctx context.Context, query ac.RelationTupleQuery) (int64, error) {

	sqlbuilder := goqu.Dialect("postgres").From(query.Object.Namespace).Select(
		goqu.COUNT("*"),
	).Where(goqu.Ex{
		"object":   query.Object.ID,
		"relation": query.Relations,
		"user":     query.Subject.String(),
	})

	sql, args, err := sqlbuilder.ToSQL()
	if err != nil {
		return -1, err
	}

	row := s.ConnPool.QueryRow(ctx, sql, args...)

	var count int64
	if err := row.Scan(&count); err != nil {
		return -1, err
	}

	return count, nil
}

func (s *SQLStore) ListRelationTuples(ctx context.Context, query *aclpb.ListRelationTuplesRequest_Query, mask *fieldmaskpb.FieldMask) ([]ac.InternalRelationTuple, error) {

	// if len(mask.GetPaths()) > 0 {
	// 	sqlbuilder.Select(mask.GetPaths())
	// }

	sqlbuilder := goqu.Dialect("postgres").From(query.GetNamespace()).Prepared(true)

	if query.GetObject() != "" {
		sqlbuilder = sqlbuilder.Where(goqu.Ex{
			"object": query.GetObject(),
		})
	}

	if len(query.GetRelations()) > 0 {
		sqlbuilder = sqlbuilder.Where(goqu.Ex{
			"relation": query.GetRelations(),
		})
	}

	if query.GetSubject() != nil {
		sqlbuilder = sqlbuilder.Where(goqu.Ex{
			"user": query.GetSubject().String(),
		})
	}

	sql, args, err := sqlbuilder.ToSQL()
	if err != nil {
		return nil, err
	}

	rows, err := s.ConnPool.Query(ctx, sql, args...)
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
	rows.Close()

	return tuples, nil
}

func (s *SQLStore) TransactRelationTuples(ctx context.Context, tupleInserts []*ac.InternalRelationTuple, tupleDeletes []*ac.InternalRelationTuple) error {

	txn, err := s.ConnPool.Begin(ctx)
	if err != nil {
		return err
	}

	for _, tuple := range tupleInserts {
		sqlbuilder := goqu.Dialect("postgres").Insert(tuple.Namespace).Cols("object", "relation", "user").Vals(
			goqu.Vals{tuple.Object, tuple.Relation, tuple.Subject.String()},
		).OnConflict(goqu.DoNothing())

		sql, args, err := sqlbuilder.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(ctx, sql, args...)
		if err != nil {
			return err
		}

		sql, args, err = goqu.Dialect("postgres").Insert("changelog").Cols(
			"namespace", "operation", "relationtuple", "timestamp",
		).Vals(
			goqu.Vals{tuple.Namespace, "INSERT", tuple.String(), goqu.L("NOW()")},
		).OnConflict(
			goqu.DoNothing(),
		).ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(ctx, sql, args...)
		if err != nil {
			return err
		}
	}

	for _, tuple := range tupleDeletes {
		sqlbuilder := goqu.Dialect("postgres").Delete(tuple.Namespace).Where(goqu.Ex{
			"object":   tuple.Object,
			"relation": tuple.Relation,
			"user":     tuple.Subject.String(),
		})

		sql, args, err := sqlbuilder.ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(ctx, sql, args...)
		if err != nil {
			return err
		}

		sql, args, err = goqu.Dialect("postgres").Insert("changelog").Cols(
			"namespace", "operation", "relationtuple", "timestamp",
		).Vals(
			goqu.Vals{tuple.Namespace, "DELETE", tuple.String(), goqu.L("NOW()")},
		).OnConflict(
			goqu.DoNothing(),
		).ToSQL()
		if err != nil {
			return err
		}

		_, err = txn.Exec(ctx, sql, args...)
		if err != nil {
			return err
		}
	}

	return txn.Commit(ctx)
}
