module github.com/jon-whit/zanzibar-poc/access-controller

replace github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1 => ./api/protos/iam/accesscontroller/v1alpha1

go 1.15

require (
	github.com/buraksezer/consistent v0.0.0-20191006190839-693edf70fd72
	github.com/cespare/xxhash v1.1.0
	github.com/doug-martin/goqu/v9 v9.10.0
	github.com/google/btree v1.0.0 // indirect
	github.com/google/uuid v1.2.0
	github.com/hashicorp/go-multierror v1.1.0
	github.com/hashicorp/go-uuid v1.0.1 // indirect
	github.com/hashicorp/golang-lru v0.5.1 // indirect
	github.com/hashicorp/memberlist v0.2.2
	github.com/jackc/pgx/v4 v4.10.1
	github.com/kr/pretty v0.2.0 // indirect
	github.com/pkg/errors v0.9.1
	github.com/sirupsen/logrus v1.4.2
	golang.org/x/net v0.0.0-20200226121028-0de0cce0169b // indirect
	golang.org/x/sync v0.0.0-20190911185100-cd5d95a43a6e // indirect
	google.golang.org/grpc v1.35.0
	google.golang.org/protobuf v1.25.0
	gopkg.in/check.v1 v1.0.0-20190902080502-41f04d3bba15 // indirect
	gopkg.in/yaml.v2 v2.4.0
)
