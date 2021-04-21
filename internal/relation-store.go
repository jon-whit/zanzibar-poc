package accesscontroller

import (
	"context"

	aclpb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type RelationTupleStore interface {
	Usersets(ctx context.Context, object Object, relations ...string) ([]Userset, error)
	ListRelationTuples(ctx context.Context, query *aclpb.ListRelationTuplesRequest_Query, mask *fieldmaskpb.FieldMask) ([]InternalRelationTuple, error)
	RowCount(ctx context.Context, query RelationTupleQuery) (int64, error)
	TransactRelationTuples(ctx context.Context, insert []*InternalRelationTuple, delete []*InternalRelationTuple) error
}
