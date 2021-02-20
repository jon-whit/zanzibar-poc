package accesscontroller

type RelationTupleStore interface {
	Usersets(object Object, relation string) ([]Userset, error)
}
