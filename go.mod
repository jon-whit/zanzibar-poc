module github.com/jon-whit/zanzibar-poc/access-controller

replace github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1 => ./api/protos/iam/accesscontroller/v1alpha1

go 1.15

require (
	github.com/buraksezer/consistent v0.0.0-20191006190839-693edf70fd72
	github.com/cespare/xxhash v1.1.0
	github.com/google/uuid v1.2.0
	github.com/hashicorp/memberlist v0.2.2
	github.com/jackc/pgx/v4 v4.10.1
	github.com/pkg/errors v0.8.1
	github.com/sirupsen/logrus v1.4.2
	google.golang.org/grpc v1.35.0
	google.golang.org/protobuf v1.25.0
	gopkg.in/yaml.v2 v2.4.0
)
