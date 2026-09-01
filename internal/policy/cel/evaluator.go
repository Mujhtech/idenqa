// Package policycel adapts the selected CEL implementation to Idenqa-owned
// policy contracts. CEL types never cross this package boundary.
package policycel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"slices"
	"sort"

	policyv1 "github.com/Mujhtech/idenqa/contracts/policy/v1"
	"github.com/Mujhtech/idenqa/internal/policy"
	celgo "github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/operators"
	exprpb "google.golang.org/genproto/googleapis/api/expr/v1alpha1"
)

const (
	maximumExpressionNodes = 64
	maximumExpressionDepth = 32
	maximumEvaluationCost  = 1000
	evaluatorIdentity      = "idenqa.policy.cel.v1;cel-go=v0.31.0;vars=facts,region;ops=and,or,not,eq,neq,index;macros=none;nodes=64;depth=32;cost=1000"
)

var (
	// ErrCompile means the public document cannot compile under the closed subset.
	ErrCompile = errors.New("policy cel: compile failed")
	// ErrEvaluate means deterministic evaluation could not produce owned output.
	ErrEvaluate = errors.New("policy cel: evaluation failed")
	// ErrPolicyMismatch means the snapshot does not pin this compiled document.
	ErrPolicyMismatch = errors.New("policy cel: snapshot policy mismatch")
)

type compiledRule struct {
	name    string
	result  policyv1.Result
	program celgo.Program
}

// Evaluator is a concurrency-safe compiled policy evaluator.
type Evaluator struct {
	document  policyv1.Document
	digest    string
	reference policy.EvaluatorReference
	rules     []compiledRule
}

// Compiler validates canonical documents under the selected CEL subset.
type Compiler struct{}

var _ policy.RevisionCompiler = Compiler{}
var _ policy.SimulationCompiler = Compiler{}

// Reference returns the exact selected evaluator implementation identity.
func (Compiler) Reference() policy.EvaluatorReference { return evaluatorReference() }

// CompileCanonical compiles one document and returns the exact evaluator identity.
func (Compiler) CompileCanonical(ctx context.Context, input []byte) (policy.EvaluatorReference, error) {
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorReference{}, err
	}
	evaluator, err := ParseCanonical(input)
	if err != nil {
		return policy.EvaluatorReference{}, err
	}
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorReference{}, err
	}
	return evaluator.Reference(), nil
}

// CompileSimulation compiles supplied canonical meaning without registering
// or activating it and returns only the owned simulation program contract.
func (Compiler) CompileSimulation(ctx context.Context, input []byte) (policy.SimulationProgram, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	evaluator, err := ParseCanonical(input)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return evaluator, nil
}

var _ policy.Evaluator = (*Evaluator)(nil)

// ParseCanonical compiles one byte-exact canonical public policy document.
func ParseCanonical(input []byte) (*Evaluator, error) {
	document, err := policyv1.ParseCanonical(input)
	if err != nil {
		return nil, fmt.Errorf("%w: public document: %w", ErrCompile, err)
	}
	return New(document)
}

// New validates and compiles one public document under the closed v1 subset.
func New(document policyv1.Document) (*Evaluator, error) {
	canonical, err := policyv1.Canonical(document)
	if err != nil {
		return nil, fmt.Errorf("%w: canonical document: %w", ErrCompile, err)
	}
	normalized, err := policyv1.ParseCanonical(canonical)
	if err != nil {
		return nil, fmt.Errorf("%w: restore canonical document: %w", ErrCompile, err)
	}
	digest, err := policyv1.Digest(normalized)
	if err != nil {
		return nil, fmt.Errorf("%w: digest document: %w", ErrCompile, err)
	}
	environment, err := celgo.NewEnv(
		celgo.ClearMacros(),
		celgo.ParserExpressionSizeLimit(policyv1.MaximumExpressionBytes),
		celgo.ParserRecursionLimit(maximumExpressionDepth),
		celgo.ExpressionNestingDepthLimit(maximumExpressionDepth),
		celgo.ExpressionNodeLimit(maximumExpressionNodes),
		celgo.Variable("facts", celgo.MapType(celgo.StringType, celgo.StringType)),
		celgo.Variable("region", celgo.StringType),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: construct environment: %w", ErrCompile, err)
	}
	rules := make([]compiledRule, len(normalized.Rules))
	for index, rule := range normalized.Rules {
		compiled, compileErr := compileRule(environment, rule)
		if compileErr != nil {
			return nil, fmt.Errorf("%w: rule %q: %w", ErrCompile, rule.Name, compileErr)
		}
		rules[index] = compiled
	}
	return &Evaluator{
		document:  normalized,
		digest:    digest,
		reference: evaluatorReference(),
		rules:     rules,
	}, nil
}

func evaluatorReference() policy.EvaluatorReference {
	identitySum := sha256.Sum256([]byte(evaluatorIdentity))
	return policy.EvaluatorReference{
		Major:  policy.SnapshotSchemaMajor,
		Minor:  policy.SnapshotSchemaMinor,
		Digest: hex.EncodeToString(identitySum[:]),
	}
}

// Reference pins the adapter and exact v1 subset, independently of a policy.
func (evaluator *Evaluator) Reference() policy.EvaluatorReference {
	if evaluator == nil {
		return policy.EvaluatorReference{}
	}
	return evaluator.reference
}

// PolicyDigest returns the canonical public document digest.
func (evaluator *Evaluator) PolicyDigest() string {
	if evaluator == nil {
		return ""
	}
	return evaluator.digest
}

// Evaluate translates a snapshot into bounded engine-neutral rule results.
func (evaluator *Evaluator) Evaluate(ctx context.Context, snapshot policy.Snapshot) (policy.EvaluatorOutput, error) {
	if evaluator == nil {
		return policy.EvaluatorOutput{}, ErrEvaluate
	}
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorOutput{}, err
	}
	if !evaluator.matches(snapshot.Policy()) || snapshot.Evaluator() != evaluator.reference {
		return policy.EvaluatorOutput{}, ErrPolicyMismatch
	}
	facts := snapshot.Facts()
	activationFacts := make(map[string]string, len(facts))
	for _, fact := range facts {
		activationFacts[string(fact.Key)] = string(fact.State)
	}
	activation := map[string]any{"facts": activationFacts, "region": snapshot.Region()}
	results := make([]policy.RequirementResult, 0, len(evaluator.rules))
	for _, rule := range evaluator.rules {
		if err := ctx.Err(); err != nil {
			return policy.EvaluatorOutput{}, err
		}
		output, _, err := rule.program.Eval(activation)
		if err != nil {
			return policy.EvaluatorOutput{}, fmt.Errorf("%w: rule %q: %w", ErrEvaluate, rule.name, err)
		}
		matched, ok := output.Value().(bool)
		if !ok {
			return policy.EvaluatorOutput{}, fmt.Errorf("%w: rule %q returned non-boolean", ErrEvaluate, rule.name)
		}
		if !matched {
			continue
		}
		result, err := ownedResult(rule)
		if err != nil {
			return policy.EvaluatorOutput{}, err
		}
		results = append(results, result)
	}
	if err := ctx.Err(); err != nil {
		return policy.EvaluatorOutput{}, err
	}
	if len(results) == 0 {
		return policy.EvaluatorOutput{}, fmt.Errorf("%w: no rule matched", ErrEvaluate)
	}
	return policy.EvaluatorOutput{Results: results, Assurance: evaluator.document.VerifiedAssurance}, nil
}

func (evaluator *Evaluator) matches(reference policy.Reference) bool {
	return reference.ID.String() == evaluator.document.PolicyID &&
		reference.Revision == evaluator.document.Revision &&
		reference.SchemaMajor == evaluator.document.SchemaMajor &&
		reference.SchemaMinor == evaluator.document.SchemaMinor &&
		reference.Digest == evaluator.digest
}

func compileRule(environment *celgo.Env, rule policyv1.Rule) (compiledRule, error) {
	ast, issues := environment.Compile(rule.When)
	if issues != nil && issues.Err() != nil {
		return compiledRule{}, issues.Err()
	}
	if ast.OutputType() != celgo.BoolType {
		return compiledRule{}, errors.New("expression must return bool")
	}
	checked, err := celgo.AstToCheckedExpr(ast)
	if err != nil {
		return compiledRule{}, fmt.Errorf("convert checked expression: %w", err)
	}
	referenced := make(map[string]struct{})
	if err := inspectExpression(checked.GetExpr(), referenced); err != nil {
		return compiledRule{}, err
	}
	actual := make([]string, 0, len(referenced))
	for fact := range referenced {
		actual = append(actual, fact)
	}
	sort.Strings(actual)
	if !slices.Equal(actual, rule.Result.ContributingFacts) {
		return compiledRule{}, errors.New("referenced facts must exactly match contributing_facts")
	}
	program, err := environment.Program(ast, celgo.CostLimit(maximumEvaluationCost))
	if err != nil {
		return compiledRule{}, fmt.Errorf("construct program: %w", err)
	}
	return compiledRule{name: rule.Name, result: rule.Result, program: program}, nil
}

func inspectExpression(expression *exprpb.Expr, referenced map[string]struct{}) error {
	if expression == nil {
		return errors.New("empty expression")
	}
	if identifier := expression.GetIdentExpr(); identifier != nil {
		if identifier.GetName() != "facts" && identifier.GetName() != "region" {
			return fmt.Errorf("identifier %q is not allowed", identifier.GetName())
		}
		return nil
	}
	if expression.GetConstExpr() != nil {
		switch expression.GetConstExpr().ConstantKind.(type) {
		case *exprpb.Constant_BoolValue, *exprpb.Constant_StringValue:
			return nil
		default:
			return errors.New("only bool and string literals are allowed")
		}
	}
	call := expression.GetCallExpr()
	if call == nil || call.Target != nil {
		return errors.New("only closed boolean operators and static fact indexing are allowed")
	}
	if call.Function == operators.Index {
		return inspectFactIndex(call, referenced)
	}
	switch call.Function {
	case operators.LogicalAnd, operators.LogicalOr, operators.Equals, operators.NotEquals:
		if len(call.Args) != 2 {
			return errors.New("invalid binary operator arity")
		}
	case operators.LogicalNot:
		if len(call.Args) != 1 {
			return errors.New("invalid unary operator arity")
		}
	default:
		return fmt.Errorf("operator or function %q is not allowed", call.Function)
	}
	for _, argument := range call.Args {
		if err := inspectExpression(argument, referenced); err != nil {
			return err
		}
	}
	return nil
}

func inspectFactIndex(call *exprpb.Expr_Call, referenced map[string]struct{}) error {
	if len(call.Args) != 2 {
		return errors.New("only facts[\"static.key\"] indexing is allowed")
	}
	identifier := call.Args[0].GetIdentExpr()
	if identifier == nil || identifier.GetName() != "facts" {
		return errors.New("only facts[\"static.key\"] indexing is allowed")
	}
	constant := call.Args[1].GetConstExpr()
	if constant == nil {
		return errors.New("fact index must be a string literal")
	}
	value, ok := constant.ConstantKind.(*exprpb.Constant_StringValue)
	if !ok {
		return errors.New("fact index must be a string literal")
	}
	key, err := policy.NewFactKey(value.StringValue)
	if err != nil {
		return errors.New("fact index is invalid")
	}
	referenced[string(key)] = struct{}{}
	return nil
}

func ownedResult(rule compiledRule) (policy.RequirementResult, error) {
	facts := make([]policy.FactKey, len(rule.result.ContributingFacts))
	for index, encoded := range rule.result.ContributingFacts {
		fact, err := policy.NewFactKey(encoded)
		if err != nil {
			return policy.RequirementResult{}, fmt.Errorf("%w: invalid compiled fact", ErrEvaluate)
		}
		facts[index] = fact
	}
	return policy.RequirementResult{
		Name:              rule.name,
		State:             policy.RequirementState(rule.result.State),
		ContributingFacts: facts,
		Candidate:         policy.Directive(rule.result.Directive),
		Priority:          rule.result.Priority,
		ReasonCodes:       slices.Clone(rule.result.ReasonCodes),
	}, nil
}
