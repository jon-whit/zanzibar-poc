package accesscontroller

import (
	"context"

	aclpb "github.com/jon-whit/zanzibar-poc/access-controller/gen/go/iam/accesscontroller/v1alpha1"
)

type NamespaceManager interface {
	WriteConfig(ctx context.Context, cfg *aclpb.NamespaceConfig) error
	GetConfig(ctx context.Context, namespace string) (*aclpb.NamespaceConfig, error)
	GetRewrite(ctx context.Context, namespace, relation string) (*aclpb.Rewrite, error)
}
