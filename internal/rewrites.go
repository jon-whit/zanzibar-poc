package accesscontroller

type RewriteOperator string

const (
	Intersection RewriteOperator = "INTERSECTION"
	Union        RewriteOperator = "UNION"
)

/*
 * RewriteRule
 */
type RewriteRule struct {
	Operand  interface{} // todo: make me more elegant
	Operator RewriteOperator
	Children []*RewriteRule
}

type ThisRelation struct {
	Relation string
}

type ComputedUserset struct {
	Relation string
}

type Tupleset struct {
	Relation string
}

type TupleToUserset struct {
	Tupleset        Tupleset
	ComputedUserset ComputedUserset
}

func ExpandRewriteOperand(relation string, rewrite *RewriteOperand) RewriteRule {

	if rewrite.ThisRelation != nil {
		return RewriteRule{Operand: ThisRelation{relation}}
	}

	if rewrite.ComputedUserset != nil {
		return RewriteRule{Operand: ComputedUserset{Relation: rewrite.ComputedUserset.Relation}}
	}

	if rewrite.TupleToUserset != nil {
		return RewriteRule{Operand: TupleToUserset{
			Tupleset:        Tupleset{Relation: rewrite.TupleToUserset.Tupleset.Relation},
			ComputedUserset: ComputedUserset{Relation: rewrite.TupleToUserset.ComputedUserset.Relation},
		}}
	}

	var op []RewriteOperand

	var operator RewriteOperator
	if rewrite.Intersection != nil {
		op = rewrite.Intersection
		operator = Intersection
	}

	if rewrite.Union != nil {
		op = rewrite.Union
		operator = Union
	}

	var rule RewriteRule
	children := []*RewriteRule{}
	for _, operand := range op {
		rule.Operator = operator

		r := ExpandRewriteOperand(relation, &operand)
		children = append(children, &r)

	}
	rule.Children = children

	return rule
}
