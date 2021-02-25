package accesscontroller

import (
	"encoding/json"
	"fmt"
	"strings"

	pb "github.com/jon-whit/zanzibar-poc/access-controller/api/protos/iam/accesscontroller/v1alpha1"
	"github.com/pkg/errors"
)

var ErrInvalidSubjectSetString = fmt.Errorf("The provided SubjectSet string is malformed.")

// Object representas a namespace and id in the form of `namespace:object_id`
type Object struct {
	Namespace string
	ID        string
}

// Userset represents an object and relation in the form of `object#relation`
type Userset struct {
	Relation string
	Object   Object
}

func (u Userset) String() string {
	return fmt.Sprintf("%s:%s#%s", u.Object.Namespace, u.Object.ID, u.Relation)
}

// User can be either an Userset or an UserID
type User struct {
	Userset Userset
	UserID  string
}

// RelationTuple is a relation between an user and an object
// `group:eng#member@11``
type RelationTuple struct {
	Object   Object
	Relation string
	User     User
}

type Subject interface {
	json.Marshaler

	String() string
	FromString(string) (Subject, error)
	Equals(interface{}) bool
	ToProto() *pb.Subject
}

type SubjectID struct {
	ID string `json:"id"`
}

func (s SubjectID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

func (s *SubjectID) Equals(v interface{}) bool {
	uv, ok := v.(*SubjectID)
	if !ok {
		return false
	}
	return uv.ID == s.ID
}

func (s *SubjectID) FromString(str string) (Subject, error) {
	s.ID = str
	return s, nil
}

func (s *SubjectID) String() string {
	return s.ID
}

func (s *SubjectID) ToProto() *pb.Subject {
	return &pb.Subject{
		Ref: &pb.Subject_Id{
			Id: s.ID,
		},
	}
}

type SubjectSet struct {
	Namespace string `json:"namespace"`
	Object    string `json:"object"`
	Relation  string `json:"relation"`
}

func (s *SubjectSet) Equals(v interface{}) bool {
	uv, ok := v.(*SubjectSet)
	if !ok {
		return false
	}
	return uv.Relation == s.Relation && uv.Object == s.Object && uv.Namespace == s.Namespace
}

func (s *SubjectSet) String() string {
	return fmt.Sprintf("%s:%s#%s", s.Namespace, s.Object, s.Relation)
}

func (s SubjectSet) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}

func (s *SubjectSet) ToProto() *pb.Subject {
	return &pb.Subject{
		Ref: &pb.Subject_Set{
			Set: &pb.SubjectSet{
				Namespace: s.Namespace,
				Object:    s.Object,
				Relation:  s.Relation,
			},
		},
	}
}

func (s *SubjectSet) FromString(str string) (Subject, error) {
	parts := strings.Split(str, "#")
	if len(parts) != 2 {
		return nil, errors.WithStack(ErrInvalidSubjectSetString)
	}

	innerParts := strings.Split(parts[0], ":")
	if len(innerParts) != 2 {
		return nil, errors.WithStack(ErrInvalidSubjectSetString)
	}

	s.Namespace = innerParts[0]
	s.Object = innerParts[1]
	s.Relation = parts[1]

	return s, nil
}

type InternalRelationTuple struct {
	Namespace string  `json:"namespace"`
	Object    string  `json:"object"`
	Relation  string  `json:"relation"`
	Subject   Subject `json:"subject"`
}

// String returns r as a relation tuple in string format.
func (r *InternalRelationTuple) String() string {
	return fmt.Sprintf("%s:%s#%s@%s", r.Namespace, r.Object, r.Relation, r.Subject)
}

// ToProto serializes r in it's equivalent protobuf format.
func (r *InternalRelationTuple) ToProto() *pb.RelationTuple {
	return &pb.RelationTuple{
		Namespace: r.Namespace,
		Object:    r.Object,
		Relation:  r.Relation,
		Subject:   r.Subject.ToProto(),
	}
}

// SubjectSetFromString takes a string `s` and attempts to decode it into
// a SubjectSet (namespace:object#relation). If the string is not formatted
// as a SubjectSet then an error is returned.
func SubjectSetFromString(s string) (SubjectSet, error) {
	subjectSet := SubjectSet{}

	parts := strings.Split(s, "#")
	if len(parts) != 2 {
		return subjectSet, errors.WithStack(ErrInvalidSubjectSetString)
	}

	innerParts := strings.Split(parts[0], ":")
	if len(innerParts) != 2 {
		return subjectSet, errors.WithStack(ErrInvalidSubjectSetString)
	}

	subjectSet.Namespace = innerParts[0]
	subjectSet.Object = innerParts[1]
	subjectSet.Relation = parts[1]

	return subjectSet, nil
}

// SubjectFromString parses the string s and returns a Subject - either
// a SubjectSet or an explicit SubjectID.
func SubjectFromString(s string) (Subject, error) {
	if strings.Contains(s, "#") {
		return (&SubjectSet{}).FromString(s)
	}
	return (&SubjectID{}).FromString(s)
}

// SubjectFromProto deserializes the protobuf subject `sub` into
// it's equivalent Subject structure.
func SubjectFromProto(sub *pb.Subject) Subject {
	switch s := sub.GetRef().(type) {
	case *pb.Subject_Id:
		return &SubjectID{
			ID: s.Id,
		}
	case *pb.Subject_Set:
		return &SubjectSet{
			Namespace: s.Set.Namespace,
			Object:    s.Set.Object,
			Relation:  s.Set.Relation,
		}
	}

	return nil
}

type UsersetRewrite struct {
	Union []struct {
		ComputedUserset struct {
			Relation string `yaml:"relation"`
		} `yaml:"computed_userset,omitempty"`
		TupleToUserset struct {
			Tupleset struct {
				Relation string `yaml:"relation"`
			} `yaml:"tupleset"`
			ComputedUserset struct {
				Object   string `yaml:"object"`
				Relation string `yaml:"relation"`
			} `yaml:"computed_userset"`
		} `yaml:"tuple_to_userset,omitempty"`
	} `yaml:"union"`
}

type NamespaceConfig struct {
	Name      string `yaml:"name"`
	Relations []struct {
		Name           string         `yaml:"name"`
		UsersetRewrite UsersetRewrite `yaml:"userset_rewrite,omitempty"`
	} `yaml:"relations"`
}

type RelationTupleQuery struct {
	Object    Object
	Relations []string
	Subject   Subject
}
