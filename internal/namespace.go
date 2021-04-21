package accesscontroller

import "context"

type NamespaceManager interface {
	GetNamespaceConfig(ctx context.Context, namespace string) (*NamespaceConfig, error)
	GetRewriteRule(ctx context.Context, namespace, relation string) (*RewriteRule, error)
}
