// Copyright 2015 PingCAP, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// See the License for the specific language governing permissions and
// limitations under the License.

package optimizer

import (
	"github.com/pingcap/tidb/ast"
	"github.com/pingcap/tidb/parser/opcode"
	"github.com/pingcap/tidb/terror"
)

// Optimizer error codes.
const (
	CodeOneColumn terror.ErrCode = iota + 1
	CodeRowColumns
	CodeSameColumns
	CodeMultiWildCard
)

// Optimizer base errors.
var (
	ErrOneColumn     = terror.ClassOptimizer.New(CodeOneColumn, "Operand should contain 1 column(s)")
	ErrRowColumns    = terror.ClassOptimizer.New(CodeRowColumns, "Operand should contain >= 2 columns for Row")
	ErrSameColumns   = terror.ClassOptimizer.New(CodeRowColumns, "Operands should contain same columns")
	ErrMultiWildCard = terror.ClassOptimizer.New(CodeMultiWildCard, "wildcard field exist more than once")
)

func validate(node ast.Node) error {
	var v validator
	node.Accept(&v)
	return v.err
}

// validator is an ast.Visitor that validates
// ast Nodes parsed from parser.
type validator struct {
	err           error
	wildCardCount int
}

func (v *validator) Enter(in ast.Node) (out ast.Node, skipChildren bool) {
	return in, false
}

func (v *validator) Leave(in ast.Node) (out ast.Node, ok bool) {
	switch x := in.(type) {
	case *ast.IsNullExpr:
		v.checkAllOneColumn(x.Expr)
	case *ast.IsTruthExpr:
		v.checkAllOneColumn(x.Expr)
	case *ast.BetweenExpr:
		v.checkAllOneColumn(x.Expr, x.Left, x.Right)
	case *ast.RowExpr:
		if len(x.Values) < 2 {
			v.err = ErrRowColumns
		}
	case *ast.BinaryOperationExpr:
		v.checkBinaryOperation(x)
	case *ast.PatternInExpr:
		v.checkSameColumns(append(x.List, x.Expr)...)
	case *ast.FieldList:
		v.checkFieldList(x)
	case *ast.ByItem:
		v.checkAllOneColumn(x.Expr)
	}
	return in, v.err == nil
}

func (v *validator) checkAllOneColumn(exprs ...ast.ExprNode) {
	for _, expr := range exprs {
		switch x := expr.(type) {
		case *ast.RowExpr:
			v.err = ErrOneColumn
		case *ast.SubqueryExpr:
			if len(x.Query.GetResultFields()) != 1 {
				v.err = ErrOneColumn
			}
		}
	}
	return
}

func (v *validator) checkBinaryOperation(x *ast.BinaryOperationExpr) {
	// row constructor only supports comparison operation.
	switch x.Op {
	case opcode.LT, opcode.LE, opcode.GE, opcode.GT, opcode.EQ, opcode.NE, opcode.NullEQ:
		v.checkSameColumns(x.L, x.R)
	default:
		v.checkAllOneColumn(x.L, x.R)
	}
}

func columnCount(ex ast.ExprNode) int {
	switch x := ex.(type) {
	case *ast.RowExpr:
		return len(x.Values)
	case *ast.SubqueryExpr:
		return len(x.Query.GetResultFields())
	default:
		return 1
	}
}

func (v *validator) checkSameColumns(exprs ...ast.ExprNode) {
	if len(exprs) == 0 {
		return
	}
	count := columnCount(exprs[0])
	for i := 1; i < len(exprs); i++ {
		if columnCount(exprs[i]) != count {
			v.err = ErrSameColumns
			return
		}
	}
}

func (v *validator) checkFieldList(x *ast.FieldList) {
	var hasWildCard bool
	for _, val := range x.Fields {
		if val.WildCard != nil && val.WildCard.Table.L == "" {
			if hasWildCard {
				v.err = ErrMultiWildCard
				return
			}
			hasWildCard = true
		}
		v.checkAllOneColumn(val.Expr)
		if v.err != nil {
			return
		}
	}
}
